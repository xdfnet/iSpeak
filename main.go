// ttsd — 独立 TTS 播报守护进程
// 监听 Unix Socket，收到文本 → 字节跳动 TTS SSE/PCM → 原生流式播放
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

var configDir = os.ExpandEnv("$HOME/.config/iSpeak")

var (
	configCacheMu      sync.Mutex
	configCachePath    string
	configCacheModTime time.Time
	configCache        Config
	configCacheValid   bool
)

var ttsHTTPClient = &http.Client{Timeout: 30 * time.Second}

var errAlreadyRunning = errors.New("iSpeak already running")

type StreamPlayer interface {
	Write(audio []byte) error
	CloseAndWait() error
}

// 最简单的播放器：channel 队列，串行播报
type Player struct {
	ch chan job
}

type job struct {
	text  string
	voice VoiceInfo
	cfg   Config
}

func NewPlayer() *Player {
	p := &Player{ch: make(chan job, 1)}
	go p.loop()
	return p
}

func (p *Player) Submit(text string, voice VoiceInfo, cfg Config) {
	log.Printf("TTS: %s", text)
	// 丢弃队列中的旧消息，只保留最新
	select {
	case <-p.ch:
	default:
	}
	p.ch <- job{text, voice, cfg}
}

func (p *Player) loop() {
	player, err := newDefaultStreamPlayer()
	if err != nil {
		log.Printf("启动播放器失败: %v", err)
		return
	}
	defer player.CloseAndWait()

	for j := range p.ch {
		p.play(j, player)
	}
}

func (p *Player) play(j job, player StreamPlayer) {
	startedAt := time.Now()
	onAudio := func(audio []byte) error {
		return player.Write(audio)
	}

	if err := synthesizeStream(context.Background(), j.cfg, j.text, &j.voice, onAudio); err != nil {
		log.Printf("TTS 合成失败: %v", err)
		return
	}
	log.Printf("TTS: 完成 elapsed=%s", time.Since(startedAt).Round(time.Millisecond))
}

// 音色信息
type VoiceInfo struct {
	VoiceType  string `json:"voice_type"`
	ResourceID string `json:"resourceId"`
}

// TTS 配置
type Config struct {
	APIKey       string                `json:"apiKey"`
	Endpoint     string                `json:"endpoint"`
	DefaultVoice *VoiceInfo            `json:"defaultVoice"` // 默认音色
	SourceVoices map[string]*VoiceInfo `json:"sourceVoices"` // 来源 → 音色 映射
}

func loadConfig() Config {
	configPaths := []string{
		configDir + "/config.json",
	}
	for _, p := range configPaths {
		st, statErr := os.Stat(p)
		if statErr == nil {
			configCacheMu.Lock()
			if configCacheValid && configCachePath == p && st.ModTime().Equal(configCacheModTime) {
				cached := configCache
				configCacheMu.Unlock()
				return cached
			}
			configCacheMu.Unlock()
		}

		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg Config
		if json.Unmarshal(data, &cfg) == nil && cfg.APIKey != "" {
			if err := validateConfig(cfg); err != nil {
				log.Printf("配置文件无效: %s err=%v", p, err)
				configCacheMu.Lock()
				if configCacheValid {
					cached := configCache
					configCacheMu.Unlock()
					return cached
				}
				configCacheMu.Unlock()
				return cfg
			}
			log.Printf("配置文件: %s", p)
			if cfg.DefaultVoice != nil {
				log.Printf("默认音色: %s (%s)", cfg.DefaultVoice.VoiceType, cfg.DefaultVoice.ResourceID)
			}
			for source, v := range cfg.SourceVoices {
				log.Printf("来源 %s → %s (%s)", source, v.VoiceType, v.ResourceID)
			}
			if st, stErr := os.Stat(p); stErr == nil {
				configCacheMu.Lock()
				configCachePath = p
				configCacheModTime = st.ModTime()
				configCache = cfg
				configCacheValid = true
				configCacheMu.Unlock()
			}
			return cfg
		}
	}

	// 回退到环境变量
	return Config{
		APIKey:   envOrDefault("IAGENT_TTS_API_KEY", ""),
		Endpoint: envOrDefault("IAGENT_TTS_ENDPOINT", "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse"),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TTS 请求体
type ttsRequest struct {
	User      ttsUser      `json:"user"`
	Namespace string       `json:"namespace"`
	ReqParams ttsReqParams `json:"req_params"`
}

type ttsUser struct {
	UID string `json:"uid"`
}

type ttsReqParams struct {
	Text        string         `json:"text"`
	Speaker     string         `json:"speaker"`
	AudioParams ttsAudioParams `json:"audio_params"`
}

type ttsAudioParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
}

// 调用字节跳动 TTS API，边解析 SSE 边回调 MP3 音频块
func synthesizeStream(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
	speaker := voice.VoiceType
	resourceID := voice.ResourceID

	log.Printf("音色: %s (resourceId: %s)", speaker, resourceID)

	reqBody := ttsRequest{
		User:      ttsUser{UID: fmt.Sprintf("ttsd-%d", time.Now().UnixNano())},
		Namespace: "BidirectionalTTS",
		ReqParams: ttsReqParams{
			Text:    text,
			Speaker: speaker,
			AudioParams: ttsAudioParams{
				Format:     "pcm",
				SampleRate: 48000,
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", cfg.APIKey)
	req.Header.Set("X-Api-Resource-Id", resourceID)
	req.Header.Set("X-Api-Request-Id", fmt.Sprintf("ttsd-%d", time.Now().UnixNano()))

	resp, err := ttsHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	return parseSSEStream(resp.Body, onAudio)
}

func parseSSEStream(r io.Reader, onAudio func([]byte) error) error {
	audioChunks := 0
	reader := bufio.NewReaderSize(r, 64*1024)

	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		ok, err := processEvent(payload, onAudio)
		if ok {
			audioChunks++
		}
		return err
	}

	for {
		rawLine, err := reader.ReadString('\n')
		if err != nil && len(rawLine) == 0 {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read sse: %w", err)
		}

		line := strings.TrimSpace(rawLine)
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			// SSE metadata, ignored.
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		} else {
			// 非标准 JSON 直出
			if err := flush(); err != nil {
				return err
			}
			ok, err := processEvent(line, onAudio)
			if ok {
				audioChunks++
			}
			if err != nil {
				return err
			}
		}

		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read sse: %w", err)
		}
	}
	if err := flush(); err != nil {
		return err
	}

	if audioChunks == 0 {
		return fmt.Errorf("no audio data")
	}
	return nil
}

func processEvent(payload string, onAudio func([]byte) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return false, nil
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		log.Printf("SSE 数据解析失败: %v", err)
		return false, nil
	}

	if err := sseEventError(event); err != nil {
		return false, err
	}

	if b64 := extractAudioBase64(event); b64 != "" {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return false, fmt.Errorf("decode audio chunk: %w", err)
		}
		if err := onAudio(data); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func sseEventError(event map[string]any) error {
	codeValue, ok := event["code"]
	if !ok {
		return nil
	}

	var code int64
	switch v := codeValue.(type) {
	case float64:
		code = int64(v)
	case int:
		code = int64(v)
	case int64:
		code = v
	default:
		return nil
	}

	if code == 0 || code == 20000000 {
		return nil
	}

	message, _ := event["message"].(string)
	if message == "" {
		message = "unknown error"
	}
	return fmt.Errorf("tts sse error: code=%d message=%s", code, message)
}

func extractAudioBase64(event map[string]any) string {
	for _, key := range []string{"data", "audio", "audio_data"} {
		if v, ok := event[key].(string); ok && v != "" {
			return v
		}
	}
	for _, key := range []string{"data", "result", "payload"} {
		if nested, ok := event[key].(map[string]any); ok {
			if v := extractAudioBase64(nested); v != "" {
				return v
			}
		}
	}
	return ""
}

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	// 日志轮转：最大 10MB，保留 3 份
	os.MkdirAll(configDir, 0755)
	log.SetOutput(&lumberjack.Logger{
		Filename:   configDir + "/ispeak.log",
		MaxSize:    10,
		MaxBackups: 3,
		Compress:   true,
	})

	cfg := loadConfig()
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	socketPath := configDir + "/ispeak.sock"
	listener, err := listenUnixSocket(socketPath)
	if err != nil {
		if errors.Is(err, errAlreadyRunning) {
			log.Fatalf("iSpeak 已在运行，请先关闭旧实例或重启")
		}
		log.Fatalf("监听 socket 失败: %v", err)
	}
	defer os.Remove(socketPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		listener.Close()
	}()

	player := NewPlayer()

	log.Printf("iSpeak 已启动，监听 %s", socketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return
			}
			continue
		}
		go handleConnection(conn, player)
	}
}

func listenUnixSocket(socketPath string) (net.Listener, error) {
	listener, err := net.Listen("unix", socketPath)
	if err == nil {
		return listener, nil
	}

	if !errors.Is(err, syscall.EADDRINUSE) {
		_ = os.Remove(socketPath)
		listener, retryErr := net.Listen("unix", socketPath)
		if retryErr == nil {
			return listener, nil
		}
		return nil, retryErr
	}

	conn, dialErr := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return nil, errAlreadyRunning
	}

	if removeErr := os.Remove(socketPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return nil, removeErr
	}
	listener, err = net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	log.Printf("已清理残留 socket: %s", socketPath)
	return listener, nil
}

// 校验配置必填项
func validateConfig(cfg Config) error {
	if cfg.APIKey == "" {
		return fmt.Errorf("apiKey 未设置，编辑 ~/.config/iSpeak/config.json")
	}
	if cfg.Endpoint == "" {
		return fmt.Errorf("endpoint 未设置")
	}
	if err := validateVoiceInfo("defaultVoice", cfg.DefaultVoice); err != nil {
		return err
	}
	for source, voice := range cfg.SourceVoices {
		if err := validateVoiceInfo(fmt.Sprintf("sourceVoices.%s", source), voice); err != nil {
			return err
		}
	}
	return nil
}

func validateVoiceInfo(name string, voice *VoiceInfo) error {
	if voice == nil {
		return fmt.Errorf("%s 未设置", name)
	}
	if voice.VoiceType == "" {
		return fmt.Errorf("%s.voice_type 未设置", name)
	}
	if voice.ResourceID == "" {
		return fmt.Errorf("%s.resourceId 未设置", name)
	}
	return nil
}

func handleConnection(conn net.Conn, player *Player) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("连接处理崩溃: %v", r)
		}
	}()
	defer conn.Close()

	cfg := loadConfig()
	if err := validateConfig(cfg); err != nil {
		log.Printf("配置错误，跳过本次播报: %v", err)
		return
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1*1024*1024), 1*1024*1024)
	for scanner.Scan() {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Printf("读取 socket 消息失败: %v", err)
		return
	}

	text := strings.TrimSpace(sb.String())
	if text == "" {
		return
	}

	voice, content := extractVoicePrefix(text, cfg)
	if voice == nil {
		voice = cfg.DefaultVoice
	}
	if voice == nil {
		log.Printf("未配置默认音色")
		return
	}

	cleaned := cleanText(content)
	if cleaned == "" {
		return
	}

	player.Submit(cleaned, *voice, cfg)
}

// 解析消息中的音色前缀，返回 VoiceInfo
func extractVoicePrefix(text string, cfg Config) (voice *VoiceInfo, content string) {
	// 格式: {source:claude}文本
	const prefix = "{source:"
	if strings.HasPrefix(text, prefix) {
		if end := strings.Index(text, "}"); end > len(prefix) {
			if v, ok := cfg.SourceVoices[text[len(prefix):end]]; ok {
				voice = v
			}
			content = text[end+1:]
			return
		}
	}
	content = text
	return
}
