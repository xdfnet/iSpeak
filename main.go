// ttsd — 独立 TTS 播报守护进程
// 监听 Unix Socket，收到文本 → 字节跳动 TTS SSE → 流式播放
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

var errAlreadyRunning = errors.New("iSpeak already running")

type StreamPlayer interface {
	Write(audio []byte) error
	CloseAndWait() error
	Abort() error
}

type ffplayStreamPlayer struct {
	path string
	cmd  *exec.Cmd

	mu       sync.Mutex
	stdin    io.WriteCloser
	waitOnce sync.Once
	waitErr  error
}

func newDefaultStreamPlayer() (StreamPlayer, error) {
	if path, ok := findExecutable("ffplay", "/opt/homebrew/bin/ffplay", "/usr/local/bin/ffplay"); ok {
		log.Printf("播放器模式: ffplay 流式 stdin (%s)", path)
		return newFFplayStreamPlayer(path)
	}

	log.Printf("播放器模式: afplay 完整音频 fallback")
	return &bufferedStreamPlayer{}, nil
}

func findExecutable(name string, candidates ...string) (string, bool) {
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return path, true
		}
	}
	return "", false
}

func newFFplayStreamPlayer(path string) (*ffplayStreamPlayer, error) {
	cmd := exec.Command(path, "-nodisp", "-autoexit", "-loglevel", "error", "-i", "pipe:0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	return &ffplayStreamPlayer{path: path, cmd: cmd, stdin: stdin}, nil
}

func (p *ffplayStreamPlayer) Write(audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("播放器输入已关闭")
	}
	if _, err := stdin.Write(audio); err != nil {
		return fmt.Errorf("写入播放器失败: %w", err)
	}
	return nil
}

func (p *ffplayStreamPlayer) CloseAndWait() error {
	p.mu.Lock()
	stdin := p.stdin
	p.stdin = nil
	p.mu.Unlock()
	if stdin != nil {
		if err := stdin.Close(); err != nil {
			return fmt.Errorf("关闭播放器输入失败: %w", err)
		}
	}
	if err := p.wait(); err != nil {
		return fmt.Errorf("ffplay failed: %w", err)
	}
	return nil
}

func (p *ffplayStreamPlayer) Abort() error {
	p.mu.Lock()
	stdin := p.stdin
	p.stdin = nil
	p.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.wait()
}

func (p *ffplayStreamPlayer) wait() error {
	p.waitOnce.Do(func() {
		if p.cmd != nil {
			p.waitErr = p.cmd.Wait()
		}
	})
	return p.waitErr
}

type bufferedStreamPlayer struct {
	chunks [][]byte
}

func (p *bufferedStreamPlayer) Write(audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	chunk := append([]byte(nil), audio...)
	p.chunks = append(p.chunks, chunk)
	return nil
}

func (p *bufferedStreamPlayer) CloseAndWait() error {
	total := 0
	for _, chunk := range p.chunks {
		total += len(chunk)
	}
	audio := make([]byte, 0, total)
	for _, chunk := range p.chunks {
		audio = append(audio, chunk...)
	}
	return playAudio(audio)
}

func (p *bufferedStreamPlayer) Abort() error {
	p.chunks = nil
	return nil
}

// 任务状态
// 生命周期：pending_synth -> speaking -> delete
type TaskStatus int

const (
	TaskStatusPendingSynth TaskStatus = iota
	TaskStatusSpeaking
)

// 单个 TTS 任务
type Task struct {
	ID     uint64
	Text   string
	Status TaskStatus
	Voice  VoiceInfo
	Cfg    Config
}

// 任务引擎：任务仓库 + 单流式合成播放 worker
type TaskEngine struct {
	mu sync.Mutex

	nextID       uint64
	latestID     uint64
	tasks        map[uint64]*Task
	pendingSynth []uint64
	activeID     uint64
	activeCancel context.CancelFunc

	synthWake chan struct{}

	synthesizeStreamFn func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error
	newStreamPlayerFn  func() (StreamPlayer, error)
}

func NewTaskEngine() *TaskEngine {
	return &TaskEngine{
		tasks:              make(map[uint64]*Task),
		synthWake:          make(chan struct{}, 1),
		synthesizeStreamFn: synthesizeStream,
		newStreamPlayerFn:  newDefaultStreamPlayer,
	}
}

func (e *TaskEngine) Start() {
	go e.speakWorker()
}

func (e *TaskEngine) Submit(text string, voice VoiceInfo, cfg Config) uint64 {
	e.mu.Lock()

	// 新任务进来先删所有未开始合成任务
	for _, id := range e.pendingSynth {
		delete(e.tasks, id)
		log.Printf("删除待合成任务: id=%d", id)
	}
	e.pendingSynth = e.pendingSynth[:0]

	cancelActive := e.activeCancel
	activeID := e.activeID
	if activeID != 0 {
		log.Printf("打断当前播报任务: id=%d", activeID)
	}

	e.nextID++
	task := &Task{
		ID:     e.nextID,
		Text:   text,
		Status: TaskStatusPendingSynth,
		Voice:  voice,
		Cfg:    cfg,
	}
	e.tasks[task.ID] = task
	e.latestID = task.ID
	e.pendingSynth = append(e.pendingSynth, task.ID)
	log.Printf("任务创建: id=%d text=%s", task.ID, text)

	notify(e.synthWake)
	e.mu.Unlock()

	if cancelActive != nil {
		cancelActive()
	}
	return task.ID
}

func (e *TaskEngine) speakWorker() {
	for {
		id := e.claimPendingSynth()
		if id == 0 {
			<-e.synthWake
			continue
		}

		e.processSpeakTask(id)
	}
}

func (e *TaskEngine) processSpeakTask(id uint64) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("播报任务崩溃并删除: id=%d err=%v", id, r)
			e.deleteTask(id)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	e.setActiveTask(id, cancel)
	defer e.clearActiveTask(id)

	task, ok := e.getTask(id)
	if !ok {
		return
	}
	if !e.isLatestTask(id) {
		cancel()
		log.Printf("跳过过期播报任务: id=%d", id)
		e.deleteTask(id)
		return
	}

	if err := e.speakOnce(ctx, task); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("播报已打断并删除任务: id=%d", id)
		} else {
			log.Printf("播报失败并删除任务: id=%d err=%v", id, err)
		}
		e.deleteTask(id)
		return
	}

	log.Printf("播报完成并删除任务: id=%d", id)
	e.deleteTask(id)
}

func (e *TaskEngine) speakOnce(ctx context.Context, task *Task) error {
	startedAt := time.Now()
	player, err := e.newStreamPlayerFn()
	if err != nil {
		return fmt.Errorf("启动播放器失败: %w", err)
	}

	firstChunkLogged := false
	onAudio := func(audio []byte) error {
		if len(audio) > 0 && !firstChunkLogged {
			firstChunkLogged = true
			log.Printf("首个音频 chunk: id=%d elapsed=%s bytes=%d", task.ID, time.Since(startedAt).Round(time.Millisecond), len(audio))
		}
		return player.Write(audio)
	}

	if err := e.synthesizeStreamFn(ctx, task.Cfg, task.Text, &task.Voice, onAudio); err != nil {
		_ = player.Abort()
		return err
	}
	log.Printf("TTS 流结束: id=%d elapsed=%s", task.ID, time.Since(startedAt).Round(time.Millisecond))

	done := make(chan error, 1)
	go func() {
		done <- player.CloseAndWait()
	}()
	select {
	case err := <-done:
		if err != nil {
			_ = player.Abort()
			return err
		}
	case <-ctx.Done():
		_ = player.Abort()
		<-done
		return ctx.Err()
	}
	return nil
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
		task.Status = TaskStatusSpeaking
		return id
	}
	return 0
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

func (e *TaskEngine) setActiveTask(id uint64, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeID = id
	e.activeCancel = cancel
}

func (e *TaskEngine) clearActiveTask(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activeID == id {
		e.activeID = 0
		e.activeCancel = nil
	}
}

func (e *TaskEngine) isLatestTask(id uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.latestID == id
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

// 调用字节跳动 TTS API，返回完整 MP3 音频数据。保留给测试和 fallback 使用。
func synthesize(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
	var chunks [][]byte
	if err := synthesizeStream(ctx, cfg, text, voice, func(audio []byte) error {
		chunk := append([]byte(nil), audio...)
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		return nil, err
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
				Format:     "mp3",
				SampleRate: 24000,
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
		io.Copy(io.Discard, resp.Body) // 消费 body 以释放连接
		resp.Body.Close()
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	return parseSSEStream(resp.Body, onAudio)
}

// 解析 SSE 流，提取 base64 音频数据
func parseSSE(r io.Reader) ([]byte, error) {
	var chunks [][]byte
	if err := parseSSEStream(r, func(audio []byte) error {
		chunk := append([]byte(nil), audio...)
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		return nil, err
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

func parseSSEStream(r io.Reader, onAudio func([]byte) error) error {
	audioChunks := 0
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

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

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if err := flush(); err != nil {
				return err
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
	if err := flush(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
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

	engine := NewTaskEngine()
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
	if err := validateConfig(cfg); err != nil {
		log.Printf("配置错误，跳过本次播报: %v", err)
		return
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1*1024*1024), 1*1024*1024)
	for scanner.Scan() {
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
