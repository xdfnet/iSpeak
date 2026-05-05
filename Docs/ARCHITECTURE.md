# iSpeak 架构文档

## 概述

iSpeak 是一个运行在 macOS 上的本地 TTS 播报守护进程，通过 Unix Socket 接收文本，调用火山引擎 TTS API 生成音频并播放。

当前版本采用“任务仓库 + 双 worker”两段流水线：
- 合成 worker：领取待合成任务，完成后转待播放
- 播放 worker：领取待播放任务，播放后删除任务

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
│  │  - pendingPlay:  []uint64 (FIFO)                     │  │
│  └───────────────────────────────────────────────────────┘  │
│             │                                 │              │
│             ▼                                 ▼              │
│  Synth Worker (single)               Play Worker (single)   │
│  - pending_synth -> synthesizing     - pending_play -> playing
│  - 调用 TTS（失败重试1次）            - 调用常驻播放器（失败重试1次）
│  - 成功后转 pending_play             - 完成后删除任务         │
│  - 连续失败删除任务                   - 连续失败删除任务       │
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
    Audio  []byte     // 合成音频（待播放阶段使用）
}
```

### TaskStatus

```go
const (
    TaskStatusPendingSynth TaskStatus = iota // 待合成
    TaskStatusSynthesizing                   // 合成中
    TaskStatusPendingPlay                    // 待播放
    TaskStatusPlaying                        // 播放中
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
    pendingPlay  []uint64

    synthWake chan struct{}
    playWake  chan struct{}

    synthesizeFn func(ctx context.Context, cfg Config, text string, voice *VoiceInfo) ([]byte, error)
    playFn       func(audio []byte) error
}
```

### 播放器协议（主进程 <-> 子进程）

```go
type playerCommand struct {
    Path string `json:"path"` // 待播放 MP3 文件路径
}

type playerResponse struct {
    OK    bool   `json:"ok"`            // 是否播放成功
    Error string `json:"error,omitempty"` // 失败原因
}
```

## 状态机与逻辑

### 状态流转

```
pending_synth -> synthesizing -> pending_play -> playing -> delete
```

### 任务提交（核心规则）

`Submit(cleanedText, voice, cfg)` 原子执行：
1. 删除所有 `pending_synth` 任务
2. 创建新任务（`pending_synth`）
3. 唤醒合成 worker

策略说明：
- 只清理“未开始合成”的任务
- 不打断 `synthesizing`
- 不打断 `playing`

### 合成 worker 规则

1. FIFO 领取 `pending_synth` 任务并置 `synthesizing`
2. 调用 TTS 合成（最多 2 次，即失败重试 1 次）
3. 成功：写入 `Audio`，转 `pending_play`，唤醒播放 worker
4. 连续失败：删除任务

### 播放 worker 规则

1. FIFO 领取 `pending_play` 任务并置 `playing`
2. 调用常驻播放器子进程播放（最多 2 次，即失败重试 1 次）
3. 成功：删除任务
4. 连续失败：删除任务

## 消息流程

### 1. 接收并清洗消息

`handleConnection()`：
- 读取 socket 文本
- 解析 `{source:xxx}` 音色前缀
- `cleanText()` 过滤 Markdown/表格符号
- 将“过滤后文本”提交给 `TaskEngine.Submit`

### 2. 合成阶段

- 合成 worker 领取任务
- HTTP POST 火山引擎 TTS 接口
- 解析 SSE 流并 base64 解码音频
- 成功后进入待播放

### 3. 播放阶段

- 播放 worker 领取任务
- 写临时 MP3 到进程 temp 目录
- 向常驻播放器子进程发送 `{"path":"..."}` 命令
- 子进程播放完成后回执 `{"ok":true}`
- 删除任务与临时文件

## 并发与一致性

- 单引擎锁 `mu` 保护任务仓库与两个 FIFO 队列
- 单合成 worker + 单播放 worker，降低并发复杂度
- `synthWake/playWake` 为缓冲 1 的唤醒信号，防止重复唤醒堆积
- FIFO 保证“同阶段”公平顺序；跨阶段由流水线自然衔接

## 失败与成本策略

- 新任务到达时仅清理 `pending_synth`，避免无效合成
- 合成失败：重试 1 次后删除
- 播放失败：重试 1 次后删除
- 执行中任务不打断，行为稳定、可预期

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
