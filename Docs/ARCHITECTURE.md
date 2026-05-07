# iSpeak 架构文档

## 概述

iSpeak 是一个运行在 macOS 上的本地 TTS 播报守护进程，通过 Unix Socket 接收文本，调用火山引擎 TTS 流式 API，边合成边播放。

当前版本采用“任务仓库 + 单 speak worker”流式链路：
- speak worker：领取待合成任务，SSE 每到一段音频就写入播放器 stdin
- 播放器优先使用 `ffplay -i pipe:0`，没有 `ffplay` 时回退到完整音频 `afplay`

## 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│                         客户端                              │
│  ispeak (bash CLI)  ──nc -U──>  ~/.config/iSpeak/ispeak.sock │
└─────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────┐
│                     ispeakd (Go Daemon)                    │
│                                                             │
│  Socket Acceptor                                            │
│    - net.Listener.Accept()                                  │
│    - 每个连接读取文本并提交任务                               │
│                                                             │
│  Task Engine                                                │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  Task Repository (in-memory)                         │  │
│  │  - tasks: map[uint64]*Task                           │  │
│  │  - pendingSynth: []uint64 (FIFO)                     │  │
│  └───────────────────────────────────────────────────────┘  │
│             │                                               │
│             ▼                                               │
│  Speak Worker (single)                                      │
│  - pending_synth -> speaking                                │
│  - 调用 TTS 流式接口（失败直接删除，不重试）                  │
│  - SSE audio chunk -> StreamPlayer.Write                    │
│  - 播放完成后删除任务；连续失败删除任务                       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## 核心数据结构

### Task

```go
type Task struct {
    ID     uint64     // 任务 ID（递增）
    Text   string     // 过滤后的待合成文本
    Status TaskStatus // 当前状态
    Voice  VoiceInfo  // 任务音色快照
    Cfg    Config     // 任务配置快照（提交时）
}
```

### TaskStatus

```go
const (
    TaskStatusPendingSynth TaskStatus = iota // 待合成
    TaskStatusSpeaking                       // 流式合成播放中
)
```

说明：
- 终态不持久化。任务成功/失败后都会从仓库删除。
- 不保留 `failed/canceled/completed` 常驻状态，历史通过日志追踪。

### TaskEngine

```go
type TaskEngine struct {
    mu sync.Mutex

    nextID       uint64
    tasks        map[uint64]*Task
    pendingSynth []uint64

    synthWake chan struct{}

    synthesizeStreamFn func(ctx context.Context, cfg Config, text string, voice *VoiceInfo, onAudio func([]byte) error) error
    newStreamPlayerFn  func() (StreamPlayer, error)
}
```

### 播放器接口

```go
type StreamPlayer interface {
    Write(audio []byte) error
    CloseAndWait() error
    Abort() error
}
```

## 状态机与逻辑

### 状态流转

```
pending_synth -> speaking -> delete
```

### 任务提交（核心规则）

`Submit(cleanedText, voice, cfg)` 原子执行：
1. 删除所有 `pending_synth` 任务
2. 打断当前 `speaking` 任务（取消合成/停止播放）
3. 创建新任务（`pending_synth`）
4. 唤醒 speak worker

策略说明：
- 未开始合成的旧任务直接删除
- 已领取但过期的旧任务在合成前跳过
- 正在合成/播放的旧任务会被新消息取消

### Speak worker 规则

1. FIFO 领取 `pending_synth` 任务并置 `speaking`
2. 启动 `StreamPlayer`
3. 调用 TTS 流式接口，SSE 每解析出一个音频 chunk 就写入播放器
4. TTS 结束后关闭播放器 stdin 并等待播放结束
5. 成功：删除任务
6. 连续失败：删除任务

## 消息流程

### 1. 接收并清洗消息

`handleConnection()`：
- 读取 socket 文本
- 解析 `{source:xxx}` 音色前缀
- `cleanText()` 过滤 Markdown/表格符号
- 将“过滤后文本”提交给 `TaskEngine.Submit`

### 2. 流式合成播放阶段

- speak worker 领取任务
- HTTP POST 火山引擎 TTS 接口
- 解析 SSE 流并 base64 解码音频 chunk
- 优先将 chunk 写入 `ffplay` stdin 实时播放
- 没有 `ffplay` 时缓存完整音频，结束后写临时 MP3 并用 `afplay` 播放
- 删除任务与临时文件

## 并发与一致性

- 单引擎锁 `mu` 保护任务仓库与 FIFO 队列
- 单 speak worker，保证播报顺序稳定
- `synthWake` 为缓冲 1 的唤醒信号，防止重复唤醒堆积
- FIFO 保证未开始任务公平顺序

## 失败与成本策略

- 新任务到达时清理 `pending_synth` 并打断当前任务，避免无效合成/播放
- 流式合成/播放失败：直接删除任务，不重试，避免重复播报
- 只保留最新消息优先播报，降低 TTS 成本

## 文件布局

```
~/.config/iSpeak/
├── config.json      # API Key、音色配置
├── ispeak.sock      # Unix Socket
├── ispeak.log       # 日志（lumberjack 轮转）
└── hook-speak.sh    # Claude/Codex Stop Hook

~/Library/LaunchAgents/
└── com.iSpeak.plist # launchd 服务配置
```

## 稳定性设计

- 关键 worker 使用 `panic recover`
- 配置热更新（每次连接重新加载配置）
- 播放器子进程命令协议，保证“播完再删任务”
- 日志轮转（10MB/份，保留 3 份）
- 进程级 temp 目录，退出时自动清理
