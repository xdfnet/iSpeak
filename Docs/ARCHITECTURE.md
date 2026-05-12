# iSpeak 架构文档

## 概述

iSpeak 是一个运行在 macOS 上的本地 TTS 播报守护进程，通过 Unix Socket 接收文本，调用火山引擎 TTS 流式 API，边合成边通过原生 AVAudioEngine 播放 PCM 音频。

核心链路：**Socket → Player (channel) → TTS SSE → AVAudioEngine**

## 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│                         客户端                              │
│         ispeak "文本" / Pi Extension / Claude Hook            │
│         nc -U  ─────────>  ~/.config/iSpeak/ispeak.sock      │
│         (Unix Socket)                                        │
└─────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────┐
│                     ispeakd (Go Daemon)                      │
│                                                             │
│  Socket Acceptor (handleConnection)                         │
│    - 读文本 → 解析 {source:xxx} → 选音色 → cleanText → 提交 │
│                                                             │
│  Player (channel 驱动)                                      │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  chan job (buffer=1)                                  │  │
│  │  Submit: drain 旧消息 → 入队最新                      │  │
│  │  loop:   for j := range ch → play(j, player)          │  │
│  └───────────────────────────────────────────────────────┘  │
│             │                                               │
│             ▼                                               │
│  AVAudioEngine (cgo, 单实例复用)                            │
│    - PCM 48kHz 单声道 int16 → float32                       │
│    - 流式 scheduleBuffer + pending 计数 + cond 同步         │
│    - 关闭时补齐残留字节                                     │
└─────────────────────────────────────────────────────────────┘
```

## 核心数据结构

### job

```go
type job struct {
    text   string    // cleanText 清洗后的文本
    voice  VoiceInfo // 音色快照
    source string    // 来源: "claude" / "codex" / "pi" / "default"
    cfg    Config    // 配置快照
}
```

### Player

```go
type Player struct {
    ch chan job   // buffer=1，串行播报
}
```

单 goroutine 消费 channel，一个 AVAudioEngine 实例复用。新消息到达时 drain 旧消息，不打断正在播放的。

### StreamPlayer

```go
type StreamPlayer interface {
    Write(audio []byte) error
    CloseAndWait() error
}
```

## 消息流程

### 1. Socket 接收

`handleConnection()`:
1. `bufio.Scanner` 读取完整文本（最大 1MB）
2. `extractVoicePrefix` 解析 `{source:claude}` 前缀，匹配 SourceVoices
3. 未匹配到 → fallback 到 DefaultVoice
4. `cleanText()` 过滤文本噪音（markdown/code/URL/path/UUID 等）
5. `player.Submit(文本, 音色, 来源, 配置)`

### 2. 调度与去重

`Submit()`:
- 非阻塞 drain channel 中旧消息：`select { case <-ch: default: }`
- 新消息入队

策略：**新消息丢弃旧排队消息，不打断正在播放的**

### 3. 流式合成与播放

`play()`:
1. HTTP POST 火山引擎 `/api/v3/tts/unidirectional/sse`
2. SSE 流式解析 → base64 解码 → PCM int16 数据
3. 每块 PCM 立即写入 AVAudioEngine 播放
4. **合成失败**：只记日志，播放器正常继续
5. **播放器写入失败**：返回 error，loop 层重建 AVAudioEngine

## SSE 解析

`parseSSEStream()`:
- 逐行读取，累积 `data:` 行
- 空行触发 flush → `processEvent()` 解析 JSON
- 兼容非标准直出（无 `data:` 前缀的裸 JSON）
- `extractAudioBase64` 递归提取：顶层 `data/audio/audio_data` → 嵌套 `data/result/payload`
- 错误码检查：`code` 不为 0 且不为 20000000 时返回 error
- 整条流无音频块 → 返回 `"no audio data"`

## 配置热加载

`loadConfig()`:
- mtime 缓存：路径相同 + 修改时间未变 → 直接返回缓存
- 校验失败 → 用上一次有效配置兜底
- 文件不存在 → fallback 环境变量 `IAGENT_TTS_API_KEY` / `IAGENT_TTS_ENDPOINT`

## 稳定性设计

- **panic recover**: loop goroutine 崩溃后 `go p.loop()` 自动重启
- **播放器重建**: 写入失败时关闭旧实例并创建新的 AVAudioEngine
- **新消息优先**: channel buffer=1 + drain，旧排队消息自动丢弃
- **配置热加载**: 每次连接重新读取，mtime 缓存避免频繁 I/O
- **HTTP 复用**: 全局 `ttsHTTPClient`，30s 超时，连接池复用
- **日志轮转**: lumberjack，10MB/份，保留 3 份，压缩归档
- **优雅退出**: SIGINT/SIGTERM 触发 listener.Close()

## 清洗规则

`cleanText()` 过滤顺序（先跨行块再行内符号）：

1. 跳过代码块 (` ```...``` `)
2. 跳过 artifact (`<artifact>...</artifact>`)
3. 跳过 Markdown 表格（分隔线 + 表头 + 内容行）
4. 跳过 HTML 源码行、进度噪声行
5. 行内清洗：ANSI 转义 → 链接 URL → 绝对路径 → UUID → commit hash → markdown 符号 → HTML 标签

保留适合听的内容：结论、状态、下一步动作、关键错误原因。

## 文件布局

```
~/.config/iSpeak/
├── config.json      # API Key、音色映射
├── ispeak.sock      # Unix Socket
├── ispeak.log       # 日志（lumberjack 轮转）
├── hook-speak.sh    # Claude/Codex Hook
└── ispeak.ts        # Pi Extension

~/Library/LaunchAgents/
└── com.ispeak.plist # launchd 服务配置
```

## 来源 & 音色映射

Hook 传入 `{source:claude}` 前缀，ispeakd 解析后匹配 `config.json` 中的 `sourceVoices`：

```json
{
  "defaultVoice": { "voice_type": "zh_female_mizai_uranus_bigtts", "resourceId": "seed-tts-2.0" },
  "sourceVoices": {
    "claude": { "voice_type": "zh_female_tianmeitaozi_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "codex": { "voice_type": "zh_female_shuangkuaisisi_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "pi": { "voice_type": "zh_female_mizai_uranus_bigtts", "resourceId": "seed-tts-2.0" }
  }
}
```

日志区分来源：`TTS [claude]: 文本` / `TTS [codex]: 文本` / `TTS [pi]: 文本` / `TTS [default]: 文本`
