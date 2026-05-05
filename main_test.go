package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSubmitClearsPendingSynthOnly(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
		return []byte("ok"), nil
	}
	e.playFn = func(audio []byte) error { return nil }

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

func TestSynthesizeRetryThenDeleteOnFailure(t *testing.T) {
	e := NewTaskEngine()
	var mu sync.Mutex
	calls := 0
	e.synthesizeFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil, errors.New("fail")
	}
	e.playFn = func(audio []byte) error { return nil }
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
	if calls != 2 {
		t.Fatalf("expected synth attempts=2, got %d", calls)
	}
}

func TestPlaybackRetryThenDelete(t *testing.T) {
	e := NewTaskEngine()
	e.synthesizeFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
		return []byte("audio"), nil
	}
	var mu sync.Mutex
	calls := 0
	e.playFn = func(audio []byte) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return errors.New("transient")
		}
		return nil
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
	if calls != 2 {
		t.Fatalf("expected play attempts=2, got %d", calls)
	}
}

func TestSubmitDoesNotInterruptSynthesizingTask(t *testing.T) {
	e := NewTaskEngine()
	start := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	processed := make([]string, 0, 2)

	e.synthesizeFn = func(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error) {
		if text == "a" {
			start <- struct{}{}
			<-release
		}
		mu.Lock()
		processed = append(processed, text)
		mu.Unlock()
		return []byte(text), nil
	}
	e.playFn = func(audio []byte) error { return nil }
	e.Start()

	cfg := Config{}
	voice := VoiceInfo{VoiceType: "v", ResourceID: "r"}
	e.Submit("a", voice, cfg)
	<-start // a 已进入 synthesizing
	e.Submit("b", voice, cfg)
	close(release)

	waitFor(t, 3*time.Second, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.tasks) == 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(processed) != 2 || processed[0] != "a" || processed[1] != "b" {
		t.Fatalf("expected processed [a b], got %#v", processed)
	}
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
