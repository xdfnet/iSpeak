package main

import (
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestParseSSEStreamHandlesLargeAudioLine(t *testing.T) {
	payload := strings.Repeat("a", 300*1024)
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	stream := strings.NewReader("data: {\"audio\":\"" + encoded + "\"}\n\n")

	var got []byte
	err := parseSSEStream(stream, func(audio []byte) error {
		got = append(got, audio...)
		return nil
	})
	if err != nil {
		t.Fatalf("parse stream: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("expected large payload preserved, got len=%d", len(got))
	}
}

func TestParseSSEStreamReturnsTTSFailureMessage(t *testing.T) {
	stream := strings.NewReader("event: 153\ndata: {\"code\":55001307,\"message\":\"voice clone failed\",\"data\":null}\n\n")

	err := parseSSEStream(stream, func(audio []byte) error {
		t.Fatalf("unexpected audio callback")
		return nil
	})
	if err == nil {
		t.Fatalf("expected tts failure")
	}
	if !strings.Contains(err.Error(), "55001307") || !strings.Contains(err.Error(), "voice clone failed") {
		t.Fatalf("expected code and message in error, got %v", err)
	}
}

func TestCleanTextRemovesSpeechNoise(t *testing.T) {
	input := strings.Join([]string{
		"## 结果",
		"- **验证通过**：[main.go](/Users/admin/iCode/iSpeak/main.go:123)",
		"- commit: a97e57d Improve latest-only task handling",
		"- 路径：/Users/admin/iCode/iSpeak/main.go",
		"| 文件 | 状态 |",
		"|------|------|",
		"| model-00001.safetensors | ✅ 完整 |",
		"```go",
		"fmt.Println(\"不要播代码\")",
		"```",
		"https://example.com/path",
		"飞哥，需要你重启服务。",
	}, "\n")

	got := cleanText(input)
	for _, bad := range []string{
		"**",
		"`",
		"/Users/admin",
		"https://",
		"fmt.Println",
		"safetensors",
		"文件",
		"状态",
		"完整",
		"|------|",
		"a97e57d",
	} {
		if strings.Contains(got, bad) {
			t.Fatalf("expected cleaned text not to contain %q, got %q", bad, got)
		}
	}
	for _, want := range []string{
		"结果",
		"验证通过",
		"main.go",
		"路径",
		"飞哥，需要你重启服务。",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected cleaned text to contain %q, got %q", want, got)
		}
	}
}

func TestCleanTextOrderingPreservesLinkTitleBeforeRemovingURL(t *testing.T) {
	got := cleanText("参考：[架构文档](https://example.com/docs)。")
	if !strings.Contains(got, "架构文档") {
		t.Fatalf("expected link title preserved, got %q", got)
	}
	if strings.Contains(got, "https://") || strings.Contains(got, "example.com") {
		t.Fatalf("expected URL removed, got %q", got)
	}
}

func TestCleanTextOrderingSkipsCodeBeforePathAndTableRules(t *testing.T) {
	input := strings.Join([]string{
		"结论保留。",
		"```text",
		"| 不该 | 播 |",
		"/Users/admin/iCode/iSpeak/main.go",
		"```",
		"后续保留。",
	}, "\n")

	got := cleanText(input)
	for _, bad := range []string{"不该", "/Users/admin", "main.go"} {
		if strings.Contains(got, bad) {
			t.Fatalf("expected code block removed before inline cleaning, got %q", got)
		}
	}
	if !strings.Contains(got, "结论保留。") || !strings.Contains(got, "后续保留。") {
		t.Fatalf("expected surrounding text preserved, got %q", got)
	}
}

func TestCleanTextOrderingRemovesTableHeaderWhenSeparatorAppears(t *testing.T) {
	input := strings.Join([]string{
		"前言。",
		"| 文件 | 状态 |",
		"|---|---|",
		"| main.go | 通过 |",
		"结论。",
	}, "\n")

	got := cleanText(input)
	for _, bad := range []string{"文件", "状态", "main.go"} {
		if strings.Contains(got, bad) {
			t.Fatalf("expected table header/content removed, got %q", got)
		}
	}
	if !strings.Contains(got, "前言。") || !strings.Contains(got, "结论。") {
		t.Fatalf("expected surrounding text preserved, got %q", got)
	}
}

func TestCleanTextOrderingRemovesUUIDBeforeCommitHash(t *testing.T) {
	got := cleanText("请求 ID：123e4567-e89b-12d3-a456-426614174000，状态成功。")
	if strings.Contains(got, "123e4567") || strings.Contains(got, "426614174000") {
		t.Fatalf("expected UUID removed as a whole, got %q", got)
	}
	if !strings.Contains(got, "状态成功。") {
		t.Fatalf("expected conclusion preserved, got %q", got)
	}
}

func TestCleanTextSkipsWholeMarkdownTable(t *testing.T) {
	input := strings.Join([]string{
		"表格如下：",
		"| 文件 | 状态 |",
		"|------|------|",
		"| main.go | 通过 |",
		"| main_test.go | 通过 |",
		"结论：验证通过。",
	}, "\n")

	got := cleanText(input)
	for _, bad := range []string{"文件", "状态", "main.go", "main_test.go"} {
		if strings.Contains(got, bad) {
			t.Fatalf("expected table content removed, got %q", got)
		}
	}
	if !strings.Contains(got, "表格如下：") || !strings.Contains(got, "结论：验证通过。") {
		t.Fatalf("expected surrounding text preserved, got %q", got)
	}
}

func TestCleanTextSkipsArtifactAndHTML(t *testing.T) {
	input := strings.Join([]string{
		"这是前置结论。",
		`<artifact identifier="demo" type="text/html">`,
		"<!doctype html>",
		"<html><body>不要播 HTML</body></html>",
		"</artifact>",
		"这是后置结论。",
	}, "\n")

	got := cleanText(input)
	if strings.Contains(got, "HTML") || strings.Contains(got, "artifact") {
		t.Fatalf("expected artifact/html removed, got %q", got)
	}
	if !strings.Contains(got, "这是前置结论。") || !strings.Contains(got, "这是后置结论。") {
		t.Fatalf("expected surrounding conclusions preserved, got %q", got)
	}
}

func TestCleanTextKeepsChinesePercentConclusion(t *testing.T) {
	input := strings.Join([]string{
		"下载 42% 12MB/s eta 1m",
		"测试通过率 95%，可以发布。",
	}, "\n")

	got := cleanText(input)
	if strings.Contains(got, "12MB/s") || strings.Contains(got, "eta") {
		t.Fatalf("expected progress noise removed, got %q", got)
	}
	if !strings.Contains(got, "测试通过率 95%，可以发布。") {
		t.Fatalf("expected Chinese percent conclusion preserved, got %q", got)
	}
}

func TestCleanTextKeepsPlainPercentLine(t *testing.T) {
	got := cleanText("覆盖率 95%")
	if !strings.Contains(got, "覆盖率 95%") {
		t.Fatalf("expected plain percent line preserved, got %q", got)
	}
}

func TestCleanTextKeepsOrdinaryFileReferenceLine(t *testing.T) {
	got := cleanText("已更新 main.go 和 README.md。")
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "README.md") {
		t.Fatalf("expected ordinary file references preserved, got %q", got)
	}
}

func TestCleanTextSingleLineArtifactDoesNotSwallowFollowingText(t *testing.T) {
	input := strings.Join([]string{
		`<artifact identifier="demo">不要播</artifact>`,
		"后面的结论要保留。",
	}, "\n")

	got := cleanText(input)
	if strings.Contains(got, "不要播") || strings.Contains(got, "artifact") {
		t.Fatalf("expected single-line artifact removed, got %q", got)
	}
	if !strings.Contains(got, "后面的结论要保留。") {
		t.Fatalf("expected following text preserved, got %q", got)
	}
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
