// ttsd — 独立 TTS 播报守护进程
// 监听 Unix Socket，收到文本 → 字节跳动 TTS → afplay 播放
package main

import (
	"bufio"
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
)

const (
	playQueueSize   = 128
	ttsConcurrency  = 4
	ttsMaxAttempts  = 2
	ttsRetryBackoff = 400 * time.Millisecond
)

// 音色条目
type VoiceEntry struct {
	VoiceType string `json:"voice_type"`
	ResourceID string `json:"resourceId"`
}

// 音色轮换状态
var (
	voiceIndex int
	voiceList  []VoiceEntry
	voiceMu    sync.Mutex
)

// TTS 配置 — 从环境变量读取，兼容 iAgent 的命名
type Config struct {
	APIKey    string       `json:"apiKey"`
	Endpoint  string       `json:"endpoint"`
	VoiceList []VoiceEntry `json:"voiceList"` // 音色列表，用于轮换
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
			// 初始化音色列表
			voiceMu.Lock()
			voiceList = cfg.VoiceList
			voiceIndex = 0
			if len(voiceList) > 0 {
				log.Printf("音色轮换已启用，共 %d 个音色", len(voiceList))
			}
			voiceMu.Unlock()
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
	audio []byte
}

// 调用字节跳动 TTS API，返回 MP3 音频数据
func synthesize(cfg Config, text string) ([]byte, error) {
	// 获取当前音色（轮换逻辑）
	voiceMu.Lock()
	speaker := "zh_female_tianmeitaozi_uranus_bigtts"
	resourceID := "seed-tts-2.0"
	if len(voiceList) > 0 {
		speaker = voiceList[voiceIndex].VoiceType
		resourceID = voiceList[voiceIndex].ResourceID
		voiceIndex = (voiceIndex + 1) % len(voiceList)
		log.Printf("音色轮换: %s (resourceId: %s, 索引 %d/%d)", speaker, resourceID, voiceIndex, len(voiceList))
	}
	voiceMu.Unlock()

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

	req, err := http.NewRequest("POST", cfg.Endpoint, strings.NewReader(string(body)))
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
func play(data []byte) error {
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("ttsd-%d.mp3", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	defer os.Remove(tmpFile)

	cmd := exec.Command("/usr/bin/afplay", tmpFile)
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("afplay start: %w", err)
	}
	log.Printf("播放开始: %s", filepath.Base(tmpFile))
	if err := cmd.Wait(); err != nil {
		return err
	}
	log.Printf("播放完成: %s, 耗时=%s", filepath.Base(tmpFile), time.Since(startedAt).Round(time.Millisecond))
	return nil
}

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
	cfg := loadConfig()
	if cfg.APIKey == "" {
		log.Fatal("缺少 TTS 凭证: 请设置 IAGENT_TTS_API_KEY 环境变量")
	}

	socketPath := "/tmp/ispeak.sock"
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("监听 socket 失败: %v", err)
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
	ttsSem := make(chan struct{}, ttsConcurrency)
	go playbackWorker(playQueue)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return
			}
			continue
		}
		go handleConnection(conn, cfg, playQueue, ttsSem)
	}
}

func playbackWorker(queue <-chan playJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("播放 worker 崩溃: %v", r)
		}
	}()
	for job := range queue {
		if err := play(job.audio); err != nil {
			log.Printf("播放失败: %v", err)
		}
	}
}

func handleConnection(conn net.Conn, cfg Config, queue chan<- playJob, ttsSem chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("连接处理崩溃: %v", r)
		}
	}()
	defer conn.Close()

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

	cleaned := cleanText(text)
	if cleaned == "" {
		return
	}

	log.Printf("TTS: %s", cleaned)

	ttsSem <- struct{}{}
	audio, err := synthesizeWithRetry(cfg, cleaned)
	<-ttsSem
	if err != nil {
		log.Printf("TTS 失败: %v", err)
		return
	}

	select {
	case queue <- playJob{audio: audio}:
		log.Printf("已入队播报，队列长度=%d", len(queue))
	default:
		log.Printf("播放队列已满，丢弃一条消息")
	}
}

func synthesizeWithRetry(cfg Config, text string) ([]byte, error) {
	var lastErr error
	for i := 1; i <= ttsMaxAttempts; i++ {
		audio, err := synthesize(cfg, text)
		if err == nil {
			return audio, nil
		}
		lastErr = err
		if i < ttsMaxAttempts {
			time.Sleep(ttsRetryBackoff)
		}
	}
	return nil, lastErr
}
