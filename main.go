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
	"syscall"
	"time"
)

// TTS 配置 — 从环境变量读取，兼容 iAgent 的命名
type Config struct {
	AppID       string `json:"appId"`
	AccessToken string `json:"accessToken"`
	Endpoint    string `json:"endpoint"`
	ResourceID  string `json:"resourceId"`
	VoiceType   string `json:"voiceType"`
}

func loadConfig() Config {
	// 从 iSpeak 自己的配置文件读取
	configPaths := []string{
		os.ExpandEnv("$HOME/.config/iSpeak/config.json"),
	}
	for _, p := range configPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg Config
		if json.Unmarshal(data, &cfg) == nil && cfg.AppID != "" {
			log.Printf("配置文件: %s", p)
			return cfg
		}
	}

	// 回退到环境变量
	return Config{
		AppID:       envOrDefault("IAGENT_TTS_APP_ID", ""),
		AccessToken: envOrDefault("IAGENT_TTS_ACCESS_TOKEN", ""),
		Endpoint:    envOrDefault("IAGENT_TTS_ENDPOINT", "https://openspeech.bytedance.com/api/v3/tts/unidirectional"),
		ResourceID:  envOrDefault("IAGENT_TTS_RESOURCE_ID", "seed-tts-2.0"),
		VoiceType:   envOrDefault("IAGENT_TTS_VOICE_TYPE", "zh_female_tianmeitaozi_uranus_bigtts"),
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

// 调用字节跳动 TTS API，返回 MP3 音频数据
func synthesize(cfg Config, text string) ([]byte, error) {
	reqBody := ttsRequest{
		User:      ttsUser{UID: "ttsd-" + time.Now().Format("150405")},
		Namespace: "BidirectionalTTS",
		ReqParams: ttsReqParams{
			Text:    text,
			Speaker: cfg.VoiceType,
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
	req.Header.Set("X-Api-App-Id", cfg.AppID)
	req.Header.Set("X-Api-Access-Key", cfg.AccessToken)
	req.Header.Set("X-Api-Resource-Id", cfg.ResourceID)
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
		return nil // 跳过无法解析的事件
	}

	// 先尝试提取音频（带业务 code 的正常事件也能取到音频）
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
	// 递归查找
	for _, key := range []string{"data", "result", "payload"} {
		if nested, ok := event[key].(map[string]any); ok {
			if v := extractAudioBase64(nested); v != "" {
				return v
			}
		}
	}
	return ""
}

// 通知 iAgent 暂停/恢复 VAD
const vadSock = "/tmp/iagent.vad.sock"

func vadMute() {
	if conn, err := net.Dial("unix", vadSock); err == nil {
		conn.Write([]byte("mute"))
		conn.Close()
	}
}

func vadUnmute() {
	if conn, err := net.Dial("unix", vadSock); err == nil {
		conn.Write([]byte("unmute"))
		conn.Close()
	}
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
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("afplay start: %w", err)
	}
	return cmd.Wait()
}

// 过滤格式符号，保留自然朗读文本
func cleanText(text string) string {
	// 去掉 markdown 表格 (| ... | ... |)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// 跳过纯表格分隔行 (|---|---|)
		if strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "|:---") {
			continue
		}
		// 跳过分隔线
		if strings.HasPrefix(line, "---") && strings.Count(line, "-") > 3 {
			continue
		}
		// 去掉行内 markdown 符号
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
	if cfg.AppID == "" || cfg.AccessToken == "" {
		log.Fatal("缺少 TTS 凭证: 请设置 IAGENT_TTS_APP_ID 和 IAGENT_TTS_ACCESS_TOKEN 环境变量")
	}

	socketPath := "/tmp/ispeak.sock"
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("监听 socket 失败: %v", err)
	}
	defer os.Remove(socketPath)

	// 优雅退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		listener.Close()
		os.Remove(socketPath)
		os.Exit(0)
	}()

	log.Printf("iSpeak 已启动，监听 %s", socketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return
			}
			continue
		}
		go handleConnection(conn, cfg)
	}
}

func handleConnection(conn net.Conn, cfg Config) {
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
	vadMute()
	log.Printf("TTS: %s", cleaned)
	audio, err := synthesize(cfg, cleaned)
	if err != nil {
		log.Printf("TTS 失败: %v", err)
	} else if err := play(audio); err != nil {
		log.Printf("播放失败: %v", err)
	}
	vadUnmute()
}
