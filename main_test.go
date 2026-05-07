package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubmitClearsPendingSynthOnly(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		return onAudio([]byte("ok"))
	}
	e.newStreamPlayerFn = newFakeStreamPlayerFactory()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}

	e.Submit("a", voice, cfg)
	e.Submit("b", voice, cfg)

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pendingSynth) != 1 {
		t.Fatalf("expected 1 pending synth, got %d", len(e.pendingSynth))
	}
	id := e.pendingSynth[0]
	task, ok := e.tasks[id]
	if !ok {
		t.Fatalf("expected pending task exists")
	}
	if task.Text != "b" {
		t.Fatalf("expected latest task text b, got %s", task.Text)
	}
}

func TestSpeakDeletesOnSynthesisFailureWithoutRetry(t *testing.T) {
	e := NewTaskEngine()
	var mu sync.Mutex
	calls := 0
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return errors.New("fail")
	}
	e.newStreamPlayerFn = newFakeStreamPlayerFactory()
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	id := e.Submit("x", voice, cfg)

	waitFor(t, 2*time.Second, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		_, ok := e.tasks[id]
		return !ok
	})

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected synth attempts=1, got %d", calls)
	}
}

func TestSpeakDeletesOnPlayerWriteFailureWithoutRetry(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		return onAudio([]byte("audio"))
	}
	var mu sync.Mutex
	calls := 0
	e.newStreamPlayerFn = func() (StreamPlayer, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return &fakeStreamPlayer{writeErr: calls == 1}, nil
	}
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	id := e.Submit("x", voice, cfg)

	waitFor(t, 2*time.Second, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		_, ok := e.tasks[id]
		return !ok
	})

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected player attempts=1, got %d", calls)
	}
}

func TestSubmitInterruptsSpeakingTask(t *testing.T) {
	e := NewTaskEngine()
	start := make(chan struct{}, 1)
	var mu sync.Mutex
	processed := make([]string, 0, 2)

	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		if text == "a" {
			start <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		mu.Lock()
		processed = append(processed, text)
		mu.Unlock()
		return onAudio([]byte(text))
	}
	e.newStreamPlayerFn = newFakeStreamPlayerFactory()
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	e.Submit("a", voice, cfg)
	<-start // a 已进入 speaking
	e.Submit("b", voice, cfg)

	waitFor(t, 3*time.Second, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.tasks) == 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(processed) != 1 || processed[0] != "b" {
		t.Fatalf("expected processed [b], got %#v", processed)
	}
}

func TestClaimedStaleTaskIsSkippedBeforeSynthesis(t *testing.T) {
	e := NewTaskEngine()
	calls := 0
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		calls++
		return onAudio([]byte(text))
	}
	e.newStreamPlayerFn = newFakeStreamPlayerFactory()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	firstID := e.Submit("a", voice, cfg)
	claimedID := e.claimPendingSynth()
	if claimedID != firstID {
		t.Fatalf("expected claimed first task %d, got %d", firstID, claimedID)
	}
	e.Submit("b", voice, cfg)

	e.processSpeakTask(firstID)

	if calls != 0 {
		t.Fatalf("expected stale task skipped before synthesis, got calls=%d", calls)
	}
	e.mu.Lock()
	_, firstExists := e.tasks[firstID]
	e.mu.Unlock()
	if firstExists {
		t.Fatalf("expected stale task deleted")
	}
}

func TestSubmitInterruptsPlaybackTask(t *testing.T) {
	e := NewTaskEngine()
	playbackStarted := make(chan struct{}, 1)
	firstPlayer := &fakeStreamPlayer{closeBlock: make(chan struct{}), closeStarted: playbackStarted}
	var mu sync.Mutex
	playerCalls := 0
	e.newStreamPlayerFn = func() (StreamPlayer, error) {
		mu.Lock()
		defer mu.Unlock()
		playerCalls++
		if playerCalls == 1 {
			return firstPlayer, nil
		}
		return &fakeStreamPlayer{}, nil
	}
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		if err := onAudio([]byte(text)); err != nil {
			return err
		}
		return ctx.Err()
	}
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	firstID := e.Submit("a", voice, cfg)
	<-playbackStarted
	secondID := e.Submit("b", voice, cfg)

	waitForTaskDeleted(t, e, firstID)
	waitForTaskDeleted(t, e, secondID)

	if !firstPlayer.aborted {
		t.Fatalf("expected first player aborted")
	}
}

func TestSynthesisPanicDeletesTaskAndWorkerContinues(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		if text == "panic" {
			panic("boom")
		}
		return onAudio([]byte(text))
	}
	e.newStreamPlayerFn = newFakeStreamPlayerFactory()
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	panicID := e.Submit("panic", voice, cfg)
	waitForTaskDeleted(t, e, panicID)

	okID := e.Submit("ok", voice, cfg)
	waitForTaskDeleted(t, e, okID)
}

func TestPlaybackPanicDeletesTaskAndWorkerContinues(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		return onAudio([]byte(text))
	}
	var mu sync.Mutex
	calls := 0
	e.newStreamPlayerFn = func() (StreamPlayer, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return &fakeStreamPlayer{panicOnWrite: calls == 1}, nil
	}
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	panicID := e.Submit("panic", voice, cfg)
	waitForTaskDeleted(t, e, panicID)

	okID := e.Submit("ok", voice, cfg)
	waitForTaskDeleted(t, e, okID)
}

func TestParseSSEStreamWritesChunksInOrder(t *testing.T) {
	stream := strings.NewReader(
		"data: {\"audio\":\"YQ==\"}\n\n" +
			"data: {\"data\":{\"audio\":\"Yg==\"}}\n\n" +
			"data: [DONE]\n\n",
	)

	var got []string
	err := parseSSEStream(stream, func(audio []byte) error {
		got = append(got, string(audio))
		return nil
	})
	if err != nil {
		t.Fatalf("parse stream: %v", err)
	}
	if strings.Join(got, "") != "ab" {
		t.Fatalf("expected chunks ab, got %#v", got)
	}
}

func TestInvalidSSEAudioDeletesTaskAndWorkerContinues(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeStreamFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error {
		if text == "bad" {
			return parseSSEStream(strings.NewReader("data: {\"audio\":\"***\"}\n\n"), onAudio)
		}
		return onAudio([]byte(text))
	}
	e.newStreamPlayerFn = newFakeStreamPlayerFactory()
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	badID := e.Submit("bad", voice, cfg)
	waitForTaskDeleted(t, e, badID)

	okID := e.Submit("ok", voice, cfg)
	waitForTaskDeleted(t, e, okID)
}

func TestValidateConfigRequiresDefaultVoiceResourceID(t *testing.T) {
	cfg := Config{
		APIKey:   "key",
		Endpoint: "https://example.com/tts",
		DefaultVoice: &VoiceInfo{
			VoiceType: "voice",
		},
	}

	err := validateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "defaultVoice.resourceId") {
		t.Fatalf("expected defaultVoice.resourceId error, got %v", err)
	}
}

func TestValidateConfigRequiresSourceVoiceResourceID(t *testing.T) {
	cfg := Config{
		APIKey:   "key",
		Endpoint: "https://example.com/tts",
		DefaultVoice: &VoiceInfo{
			VoiceType:  "voice",
			ResourceID: "resource",
		},
		SourceVoices: map[string]*VoiceInfo{
			"codex": {
				VoiceType: "codex-voice",
			},
		},
	}

	err := validateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "sourceVoices.codex.resourceId") {
		t.Fatalf("expected sourceVoices.codex.resourceId error, got %v", err)
	}
}

type fakeStreamPlayer struct {
	writeErr     bool
	closeErr     bool
	panicOnWrite bool
	chunks       [][]byte
	aborted      bool
	closed       bool
	closeBlock   chan struct{}
	closeStarted chan struct{}
	closeOnce    sync.Once
}

func newFakeStreamPlayerFactory() func() (StreamPlayer, error) {
	return func() (StreamPlayer, error) {
		return &fakeStreamPlayer{}, nil
	}
}

func (p *fakeStreamPlayer) Write(audio []byte) error {
	if p.panicOnWrite {
		panic("boom")
	}
	if p.writeErr {
		return errors.New("write failed")
	}
	p.chunks = append(p.chunks, append([]byte(nil), audio...))
	return nil
}

func (p *fakeStreamPlayer) CloseAndWait() error {
	p.closed = true
	if p.closeStarted != nil {
		p.closeStarted <- struct{}{}
	}
	if p.closeBlock != nil {
		<-p.closeBlock
	}
	if p.closeErr {
		return errors.New("close failed")
	}
	return nil
}

func (p *fakeStreamPlayer) Abort() error {
	p.aborted = true
	if p.closeBlock != nil {
		p.closeOnce.Do(func() {
			close(p.closeBlock)
		})
	}
	return nil
}

func TestListenUnixSocketRemovesStalePath(t *testing.T) {
	socketPath := shortSocketPath(t)
	if err := os.WriteFile(socketPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("write stale socket path: %v", err)
	}

	listener, err := listenUnixSocket(socketPath)
	if err != nil {
		t.Fatalf("listen with stale socket path: %v", err)
	}
	defer listener.Close()
}

func TestListenUnixSocketDetectsRunningInstance(t *testing.T) {
	socketPath := shortSocketPath(t)
	listener, err := listenUnixSocket(socketPath)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		close(done)
	}()

	second, err := listenUnixSocket(socketPath)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("expected errAlreadyRunning, got %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("test listener did not accept probe connection")
	}
}

func TestListenUnixSocketRemovesClosedListenerSocket(t *testing.T) {
	socketPath := shortSocketPath(t)
	stale, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("create stale listener: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale listener: %v", err)
	}

	listener, err := listenUnixSocket(socketPath)
	if err != nil {
		t.Fatalf("listen after stale listener close: %v", err)
	}
	defer listener.Close()
}

func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "ispeak-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, "sock")
}

func waitForTaskDeleted(t *testing.T, e *TaskEngine, id uint64) {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		_, ok := e.tasks[id]
		return !ok
	})
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
