// ttsd — 独立 TTS 播报守护进程
// 监听 Unix Socket，收到文本 → 字节跳动 TTS → afplay 播放
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	playQueueSize  = 128
	playBufferMax  = 64              // 缓冲队列上限
	playSeqTimeout = 60 * time.Second // seq 等待超时
	ttsMaxAttempts  = 2
	ttsRetryBackoff = 400 * time.Millisecond
)

// 全局 TTS 上下文管理（只有一个 TTS 在跑，新请求取消旧请求）
var (
	ttsCtxMu   sync.Mutex
	ttsCtx     context.Context
	ttsCancel  context.CancelFunc
)

// 全局序号分配器
var (
	seqMu   sync.Mutex
	nextSeq uint64 = 0
)

// 进程级 temp 目录（进程退出时清理）
var tempDir string

func nextSequence() uint64 {
	seqMu.Lock()
	defer seqMu.Unlock()
	nextSeq++
	return nextSeq
}

// 音色信息
type VoiceInfo struct {
	VoiceType string `json:"voice_type"`
	ResourceID string `json:"resourceId"`
}

// TTS 配置
type Config struct {
	APIKey       string                  `json:"apiKey"`
	Endpoint     string                  `json:"endpoint"`
	DefaultVoice *VoiceInfo              `json:"defaultVoice"`     // 默认音色
	SourceVoices map[string]*VoiceInfo   `json:"sourceVoices"`     // 来源 → 音色 映射
}

func loadConfig() Config {
	configPaths := []string{
		os.ExpandEnv("$HOME/.config/iSpeak/config.json"),
	}
	for _, p := range configPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg Config
		if json.Unmarshal(data, &cfg) == nil && cfg.APIKey != "" {
			log.Printf("配置文件: %s", p)
			if cfg.DefaultVoice != nil {
				log.Printf("默认音色: %s (%s)", cfg.DefaultVoice.VoiceType, cfg.DefaultVoice.ResourceID)
			}
			for source, v := range cfg.SourceVoices {
				log.Printf("来源 %s → %s (%s)", source, v.VoiceType, v.ResourceID)
			}
			return cfg
		}
	}

	// 回退到环境变量
	return Config{
		APIKey:   envOrDefault("IAGENT_TTS_API_KEY", ""),
		Endpoint: envOrDefault("IAGENT_TTS_ENDPOINT", "https://openspeech.bytedance.com/api/v3/tts/unidirectional"),
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
	Text        string        `json:"text"`
	Speaker     string        `json:"speaker"`
	AudioParams ttsAudioParams `json:"audio_params"`
}

type ttsAudioParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
}

type playJob struct {
	seq       uint64
	enqueuedAt time.Time
	audio     []byte
	voiceType string // 音色类型，用于日志
}

// 调用字节跳动 TTS API，返回 MP3 音频数据
func synthesize(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
	speaker := voice.VoiceType
	resourceID := voice.ResourceID

	log.Printf("音色: %s (resourceId: %s)", speaker, resourceID)

	reqBody := ttsRequest{
		User:      ttsUser{UID: "ttsd-" + time.Now().Format("150405")},
		Namespace: "BidirectionalTTS",
		ReqParams: ttsReqParams{
			Text:    text,
			Speaker: speaker,
			AudioParams: ttsAudioParams{
				Format:     "mp3",
				SampleRate: 24000,
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", cfg.APIKey)
	req.Header.Set("X-Api-Resource-Id", resourceID)
	req.Header.Set("X-Api-Request-Id", fmt.Sprintf("ttsd-%d", time.Now().UnixNano()))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	return parseSSE(resp.Body)
}

// 解析 SSE 流，提取 base64 音频数据
func parseSSE(r io.Reader) ([]byte, error) {
	var chunks [][]byte
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		return processEvent(payload, &chunks)
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
			continue
		}
		// 非标准 JSON 直出
		if err := flush(); err != nil {
			return nil, err
		}
		if err := processEvent(line, &chunks); err != nil {
			return nil, err
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("no audio data")
	}

	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	result := make([]byte, 0, total)
	for _, c := range chunks {
		result = append(result, c...)
	}
	return result, nil
}

func processEvent(payload string, chunks *[][]byte) error {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return nil
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return nil
	}

	if b64 := extractAudioBase64(event); b64 != "" {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err == nil {
			*chunks = append(*chunks, data)
		}
	}

	return nil
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

// 用 afplay 播放 MP3
// 过滤格式符号，保留自然朗读文本
func cleanText(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "|:---") {
			continue
		}
		if strings.HasPrefix(line, "---") && strings.Count(line, "-") > 3 {
			continue
		}
		cleaned := strings.NewReplacer(
			"**", "",
			"*", "",
			"`", "",
			"#", "",
			">", "",
		).Replace(line)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			lines = append(lines, cleaned)
		}
	}
	return strings.Join(lines, "，")
}

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	// 日志轮转：最大 10MB，保留 3 份
	configDir := os.ExpandEnv("$HOME/.config/iSpeak")
	os.MkdirAll(configDir, 0755)
	log.SetOutput(&lumberjack.Logger{
		Filename:   configDir + "/ispeak.log",
		MaxSize:    10,
		MaxBackups: 3,
		Compress:   true,
	})

	// 创建进程级 temp 目录
	cleanupOldTempDirs()
	var err error
	tempDir, err = os.MkdirTemp("", "ttsd-*")
	if err != nil {
		log.Fatalf("创建 temp 目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := loadConfig()
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	socketPath := configDir + "/ispeak.sock"
	// 先尝试监听，若地址被占用说明已有实例在跑
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			log.Fatalf("iSpeak 已在运行，请先关闭旧实例或重启")
		}
		// socket 文件残留，清理后重试
		os.Remove(socketPath)
		listener, err = net.Listen("unix", socketPath)
		if err != nil {
			log.Fatalf("监听 socket 失败: %v", err)
		}
	}
	defer os.Remove(socketPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		listener.Close()
		os.Remove(socketPath)
		os.Exit(0)
	}()

	log.Printf("iSpeak 已启动，监听 %s", socketPath)
	playQueue := make(chan playJob, playQueueSize)
	interruptCh := make(chan struct{}, 1) // 新请求打断信号
	go playbackWorker(playQueue, interruptCh)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return
			}
			continue
		}
		go handleConnection(conn, playQueue, interruptCh)
	}
}

// 清理历史遗留的 temp 目录（进程崩溃时留下）
func cleanupOldTempDirs() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ttsd-") {
			os.RemoveAll(filepath.Join(os.TempDir(), e.Name()))
		}
	}
}

// 校验配置必填项
func validateConfig(cfg Config) error {
	if cfg.APIKey == "" {
		return fmt.Errorf("apiKey 未设置，编辑 ~/.config/iSpeak/config.json")
	}
	if cfg.Endpoint == "" {
		return fmt.Errorf("endpoint 未设置")
	}
	if cfg.DefaultVoice == nil || cfg.DefaultVoice.VoiceType == "" {
		return fmt.Errorf("defaultVoice 未设置")
	}
	return nil
}

func playbackWorker(queue <-chan playJob, interruptCh <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("播放 worker 崩溃: %v", r)
		}
	}()

	var (
		nextExpected uint64              = 1
		buffer      map[uint64]playJob  = make(map[uint64]playJob)
		currentCmd  *exec.Cmd           // 当前播放的 afplay 进程
		cmdMu       sync.Mutex           // 保护 currentCmd
	)

	for {
		select {
		case <-interruptCh:
			cmdMu.Lock()
			if currentCmd != nil && currentCmd.Process != nil {
			 log.Printf("打断当前播放")
			 currentCmd.Process.Kill()
			}
			cmdMu.Unlock()
		case job := <-queue:
			if job.seq == nextExpected {
				// 正好是下一个该播放的，直接播放
				log.Printf("播放序号=%d", job.seq)
				cmd := playAudio(job.audio)
				cmdMu.Lock()
				currentCmd = cmd
				cmdMu.Unlock()
			 cmd.Wait()
			 cmdMu.Lock()
			 currentCmd = nil
			 cmdMu.Unlock()
				nextExpected++
				// 吐出缓冲中积压的 job
				for {
					if buffered, ok := buffer[nextExpected]; ok {
						// 检查超时
						if time.Since(buffered.enqueuedAt) > playSeqTimeout {
							log.Printf("序号=%d 等待超时，跳过", nextExpected)
							delete(buffer, nextExpected)
							nextExpected++
							continue
						}
						log.Printf("播放序号=%d (缓冲)", buffered.seq)
						cmd := playAudio(buffered.audio)
						cmdMu.Lock()
						currentCmd = cmd
						cmdMu.Unlock()
						cmd.Wait()
						cmdMu.Lock()
						currentCmd = nil
						cmdMu.Unlock()
						delete(buffer, nextExpected)
						nextExpected++
					} else {
						break
					}
				}
			} else {
				// 提前到的，缓存起来
				if len(buffer) >= playBufferMax {
					log.Printf("序号=%d 到达，但缓冲已满(%d)，拒绝", job.seq, playBufferMax)
					continue
				}
				log.Printf("序号=%d 提前到达，缓冲中 (期待=%d, 缓冲=%d)", job.seq, nextExpected, len(buffer)+1)
				buffer[job.seq] = job
			}
		}
	}
}

// playAudio 返回 afplay 命令对象，由调用方控制 Wait
func playAudio(data []byte) *exec.Cmd {
	tmpFile := filepath.Join(tempDir, fmt.Sprintf("ttsd-%d.mp3", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		log.Printf("写入临时文件失败: %v", err)
		return &exec.Cmd{}
	}
	cmd := exec.Command("/usr/bin/afplay", tmpFile)
	cmd.Start()
	log.Printf("播放开始: %s", filepath.Base(tmpFile))
	go func() {
		cmd.Wait()
		os.Remove(tmpFile)
	}()
	return cmd
}

func handleConnection(conn net.Conn, queue chan<- playJob, interruptCh chan<- struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("连接处理崩溃: %v", r)
		}
	}()
	defer conn.Close()

	cfg := loadConfig()

	var sb strings.Builder
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
	}

	text := strings.TrimSpace(sb.String())
	if text == "" {
		return
	}

	// 解析音色前缀 {voice:桃子}文本 或 {source:claude}文本
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

	log.Printf("TTS: %s", cleaned)

	// 打断当前播放和 TTS 合成
	select {
	case interruptCh <- struct{}{}:
		log.Printf("打断信号已发送")
	default:
	}

	// 取消旧的 TTS 上下文
	ttsCtxMu.Lock()
	if ttsCancel != nil {
		ttsCancel()
		log.Printf("TTS 旧请求已取消")
	}
	ttsCtx, ttsCancel = context.WithCancel(context.Background())
	ttsCtxMu.Unlock()

	audio, err := synthesizeWithRetry(ttsCtx, cfg, cleaned, voice)
	if err != nil {
		if ttsCtx.Err() == context.Canceled {
			log.Printf("TTS 已取消（新请求）")
		} else {
			log.Printf("TTS 失败: %v", err)
		}
		return
	}

	seq := nextSequence()
	queue <- playJob{seq: seq, enqueuedAt: time.Now(), audio: audio, voiceType: voice.VoiceType}
	log.Printf("已入队播报，序号=%d", seq)
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

func synthesizeWithRetry(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
	var lastErr error
	for i := 1; i <= ttsMaxAttempts; i++ {
		audio, err := synthesize(ctx, cfg, text, voice)
		if err == nil {
			return audio, nil
		}
		// context canceled (by new request) → stop immediately
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
		if i < ttsMaxAttempts {
			time.Sleep(ttsRetryBackoff)
		}
	}
	return nil, lastErr
}
