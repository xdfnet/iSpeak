// ttsd — 独立 TTS 播报守护进程
// 监听 Unix Socket，收到文本 → 字节跳动 TTS → afplay 播放
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
	ttsMaxAttempts   = 2
	ttsRetryBackoff  = 400 * time.Millisecond
	playMaxAttempts  = 2
	playRetryBackoff = 200 * time.Millisecond
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

// 进程级 temp 目录（进程退出时清理）
var tempDir string

type AudioPlayer interface {
	Play(audio []byte) error
	Close() error
}

type playerCommand struct {
	Path string `json:"path"`
}

type playerResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type commandPlayer struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	input  io.WriteCloser
	stdout io.ReadCloser
	dec    *json.Decoder
}

func newCommandPlayer() (*commandPlayer, error) {
	p := &commandPlayer{}
	if err := p.startLocked(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *commandPlayer) startLocked() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--player-worker")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}
	p.cmd = cmd
	p.input = stdin
	p.stdin = bufio.NewWriter(stdin)
	p.stdout = stdout
	p.dec = json.NewDecoder(stdout)
	go func(c *exec.Cmd) {
		_ = c.Wait()
	}(cmd)
	return nil
}

func (p *commandPlayer) restartLocked() error {
	if p.input != nil {
		_ = p.input.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	p.cmd = nil
	p.input = nil
	p.stdin = nil
	p.stdout = nil
	p.dec = nil
	return p.startLocked()
}

func (p *commandPlayer) Play(audio []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stdin == nil || p.dec == nil {
		if err := p.startLocked(); err != nil {
			return err
		}
	}

	tmpFile := filepath.Join(tempDir, fmt.Sprintf("ttsd-%d.mp3", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, audio, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	defer os.Remove(tmpFile)

	playOnce := func() error {
		cmdMsg := playerCommand{Path: tmpFile}
		if err := p.decodedWrite(cmdMsg); err != nil {
			return err
		}
		resp, err := p.decodedRead()
		if err != nil {
			return err
		}
		if !resp.OK {
			if resp.Error == "" {
				return errors.New("player worker failed")
			}
			return errors.New(resp.Error)
		}
		return nil
	}

	if err := playOnce(); err == nil {
		return nil
	}
	if err := p.restartLocked(); err != nil {
		return err
	}
	return playOnce()
}

func (p *commandPlayer) decodedWrite(cmdMsg playerCommand) error {
	b, err := json.Marshal(cmdMsg)
	if err != nil {
		return err
	}
	if _, err := p.stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return p.stdin.Flush()
}

func (p *commandPlayer) decodedRead() (playerResponse, error) {
	var resp playerResponse
	if err := p.dec.Decode(&resp); err != nil {
		return playerResponse{}, err
	}
	return resp, nil
}

func (p *commandPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.input != nil {
		_ = p.input.Close()
		p.input = nil
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
		p.stdout = nil
	}
	if p.stdin != nil {
		p.stdin = nil
	}
	p.dec = nil
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		p.cmd = nil
	}
	return nil
}

// 任务状态
// 生命周期：pending_synth -> synthesizing -> pending_play -> playing -> delete
type TaskStatus int

const (
	TaskStatusPendingSynth TaskStatus = iota
	TaskStatusSynthesizing
	TaskStatusPendingPlay
	TaskStatusPlaying
)

// 单个 TTS 任务
type Task struct {
	ID     uint64
	Text   string
	Status TaskStatus
	Voice  VoiceInfo
	Cfg    Config
	Audio  []byte
}

// 任务引擎：任务仓库 + 单合成 worker + 单播放 worker
type TaskEngine struct {
	mu sync.Mutex

	nextID       uint64
	tasks        map[uint64]*Task
	pendingSynth []uint64
	pendingPlay  []uint64

	synthWake chan struct{}
	playWake  chan struct{}

	synthesizeFn func(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error)
	playFn       func(audio []byte) error
}

func NewTaskEngine() *TaskEngine {
	return &TaskEngine{
		tasks:        make(map[uint64]*Task),
		synthWake:    make(chan struct{}, 1),
		playWake:     make(chan struct{}, 1),
		synthesizeFn: synthesize,
		playFn:       playAudio,
	}
}

func (e *TaskEngine) Start() {
	go e.synthWorker()
	go e.playWorker()
}

func (e *TaskEngine) Submit(text string, voice VoiceInfo, cfg Config) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 新任务进来先删所有未开始合成任务
	for _, id := range e.pendingSynth {
		delete(e.tasks, id)
		log.Printf("删除待合成任务: id=%d", id)
	}
	e.pendingSynth = e.pendingSynth[:0]

	e.nextID++
	task := &Task{
		ID:     e.nextID,
		Text:   text,
		Status: TaskStatusPendingSynth,
		Voice:  voice,
		Cfg:    cfg,
	}
	e.tasks[task.ID] = task
	e.pendingSynth = append(e.pendingSynth, task.ID)
	log.Printf("任务创建: id=%d text=%s", task.ID, text)

	notify(e.synthWake)
	return task.ID
}

func (e *TaskEngine) synthWorker() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("合成 worker 崩溃: %v", r)
		}
	}()

	for {
		id := e.claimPendingSynth()
		if id == 0 {
			<-e.synthWake
			continue
		}

		task, ok := e.getTask(id)
		if !ok {
			continue
		}

		var (
			audio   []byte
			lastErr error
		)
		for i := 1; i <= ttsMaxAttempts; i++ {
			audio, lastErr = e.synthesizeFn(context.Background(), task.Cfg, task.Text, &task.Voice)
			if lastErr == nil {
				break
			}
			if i < ttsMaxAttempts {
				time.Sleep(ttsRetryBackoff)
			}
		}

		if lastErr != nil {
			log.Printf("TTS 失败并删除任务: id=%d err=%v", id, lastErr)
			e.deleteTask(id)
			continue
		}

		e.markPendingPlay(id, audio)
		notify(e.playWake)
	}
}

func (e *TaskEngine) playWorker() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("播放 worker 崩溃: %v", r)
		}
	}()

	for {
		id := e.claimPendingPlay()
		if id == 0 {
			<-e.playWake
			continue
		}

		task, ok := e.getTask(id)
		if !ok {
			continue
		}

		var lastErr error
		for i := 1; i <= playMaxAttempts; i++ {
			lastErr = e.playFn(task.Audio)
			if lastErr == nil {
				break
			}
			if i < playMaxAttempts {
				time.Sleep(playRetryBackoff)
			}
		}

		if lastErr != nil {
			log.Printf("播放失败并删除任务: id=%d err=%v", id, lastErr)
		} else {
			log.Printf("播放完成并删除任务: id=%d", id)
		}
		e.deleteTask(id)
	}
}

func (e *TaskEngine) claimPendingSynth() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()

	for len(e.pendingSynth) > 0 {
		id := e.pendingSynth[0]
		e.pendingSynth = e.pendingSynth[1:]
		task, ok := e.tasks[id]
		if !ok {
			continue
		}
		if task.Status != TaskStatusPendingSynth {
			continue
		}
		task.Status = TaskStatusSynthesizing
		return id
	}
	return 0
}

func (e *TaskEngine) claimPendingPlay() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()

	for len(e.pendingPlay) > 0 {
		id := e.pendingPlay[0]
		e.pendingPlay = e.pendingPlay[1:]
		task, ok := e.tasks[id]
		if !ok {
			continue
		}
		if task.Status != TaskStatusPendingPlay {
			continue
		}
		task.Status = TaskStatusPlaying
		return id
	}
	return 0
}

func (e *TaskEngine) markPendingPlay(id uint64, audio []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, ok := e.tasks[id]
	if !ok {
		return
	}
	task.Audio = audio
	task.Status = TaskStatusPendingPlay
	e.pendingPlay = append(e.pendingPlay, id)
	log.Printf("合成完成转待播放: id=%d", id)
}

func (e *TaskEngine) getTask(id uint64) (*Task, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	task, ok := e.tasks[id]
	if !ok {
		return nil, false
	}
	clone := *task
	return &clone, true
}

func (e *TaskEngine) deleteTask(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.tasks, id)
}

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
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
	Text        string         `json:"text"`
	Speaker     string         `json:"speaker"`
	AudioParams ttsAudioParams `json:"audio_params"`
}

type ttsAudioParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
}

// 调用字节跳动 TTS API，返回 MP3 音频数据
func synthesize(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
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

	resp, err := ttsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body) // 消费 body 以释放连接
		resp.Body.Close()
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	defer resp.Body.Close()

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
		log.Printf("SSE 数据解析失败: %v", err)
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
		// 过滤纯表格分隔行（|---|---|、:---|:---| 等）
		if strings.Trim(line, "|-: ") == "" {
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
	if len(os.Args) > 1 && os.Args[1] == "--player-worker" {
		runPlayerWorker()
		return
	}

	log.SetFlags(log.Ltime | log.Lshortfile)

	// 日志轮转：最大 10MB，保留 3 份
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
	}()

	engine := NewTaskEngine()
	player, err := newCommandPlayer()
	if err != nil {
		log.Fatalf("播放器子进程启动失败: %v", err)
	} else {
		log.Printf("播放器模式: 常驻子进程")
		engine.playFn = player.Play
		defer player.Close()
	}
	engine.Start()

	log.Printf("iSpeak 已启动，监听 %s", socketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return
			}
			continue
		}
		go handleConnection(conn, engine)
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

func runPlayerWorker() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var cmdMsg playerCommand
		if err := json.Unmarshal([]byte(line), &cmdMsg); err != nil {
			_ = enc.Encode(playerResponse{OK: false, Error: err.Error()})
			continue
		}
		if cmdMsg.Path == "" {
			_ = enc.Encode(playerResponse{OK: false, Error: "empty path"})
			continue
		}

		if err := playFileWithBestBackend(cmdMsg.Path); err != nil {
			_ = enc.Encode(playerResponse{OK: false, Error: err.Error()})
			continue
		}
		_ = enc.Encode(playerResponse{OK: true})
	}
}

func playFileWithBestBackend(path string) error {
	if _, err := exec.LookPath("ffplay"); err == nil {
		cmd := exec.Command("ffplay", "-nodisp", "-autoexit", "-loglevel", "error", path)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ffplay failed: %w", err)
		}
		return nil
	}

	cmd := exec.Command("/usr/bin/afplay", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("afplay failed: %w", err)
	}
	return nil
}

func playAudio(data []byte) error {
	tmpFile := filepath.Join(tempDir, fmt.Sprintf("ttsd-%d.mp3", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	defer os.Remove(tmpFile)

	cmd := exec.Command("/usr/bin/afplay", tmpFile)
	log.Printf("播放开始: %s", filepath.Base(tmpFile))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("播放失败: %w", err)
	}
	return nil
}

func handleConnection(conn net.Conn, engine *TaskEngine) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("连接处理崩溃: %v", r)
		}
	}()
	defer conn.Close()

	cfg := loadConfig()

	var sb strings.Builder
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1*1024*1024), 1*1024*1024)
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
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

	log.Printf("TTS: %s", cleaned)
	engine.Submit(cleaned, *voice, cfg)
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
