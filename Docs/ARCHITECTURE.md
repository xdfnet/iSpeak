# iSpeak 架构文档

## 概述

iSpeak 是一个运行在 macOS 上的本地 TTS 播报守护进程，通过 Unix Socket 接收文本，调用火山引擎 TTS API 生成音频，按序号顺序播放。

## 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│                         客户端                              │
│                                                              │
│   ispeak (bash CLI)  ──nc -U──>  ~/.config/iSpeak/ispeak.sock │
│                                                              │
└─────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────┐
│                     ispeakd (Go Daemon)                    │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                   Socket Acceptor                    │   │
│  │              net.Listener.Accept()                   │   │
│  └──────────────────────────────────────────────────────┘   │
│                            │                               │
│                            ▼                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                    Task Manager                        │   │
│  │                                                       │   │
│  │   tasks: map[uint64]*Task                            │   │
│  │   activeTaskID: uint64                               │   │
│  │                                                       │   │
│  │   状态流转:                                           │   │
│  │   pending → synthesizing → completed/failed          │   │
│  │   pending → canceled                                 │   │
│  └──────────────────────────────────────────────────────┘   │
│                            │                               │
│                            ▼                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              TTS Context Manager                      │   │
│  │                                                       │   │
│  │   ttsCtx: context.Context                            │   │
│  │   ttsCancel: context.CancelFunc                      │   │
│  │                                                       │   │
│  │   取消策略:                                           │   │
│  │   - pending 任务 → 取消（标记 canceled）             │   │
│  │   - synthesizing 任务 → 不取消，等它完成             │   │
│  │   - completed 任务 → 已入队，正常播放                │   │
│  └──────────────────────────────────────────────────────┘   │
│                            │                               │
│                            ▼                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              HTTP Client → 火山引擎 TTS                │   │
│  │                                                       │   │
│  │   POST /api/v3/tts/unidirectional                    │   │
│  │   SSE 流式响应                                        │   │
│  │   base64 解码 → MP3 数据                              │   │
│  └──────────────────────────────────────────────────────┘   │
│                            │                               │
│                            ▼                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                playbackWorker                         │   │
│  │                                                       │   │
│  │   playQueue: chan playJob (128)                      │   │
│  │   buffer: map[uint64]playJob (64)                    │   │
│  │   nextExpected: uint64                                │   │
│  │                                                       │   │
│  │   逻辑:                                               │   │
│  │   - 严格按 seq 顺序播放                               │   │
│  │   - 乱序音频缓存，超时 60s 跳过                       │   │
│  │   - interruptCh 收到信号 → kill afplay               │   │
│  └──────────────────────────────────────────────────────┘   │
│                            │                               │
│                            ▼                               │
│                      afplay (系统命令)                     │
│                            │                               │
│                            ▼                               │
│                        扬声器/耳机                         │
└─────────────────────────────────────────────────────────────┘
```

## 核心数据结构

### Task

```go
type Task struct {
    ID       uint64      // 任务唯一 ID
    Text     string      // 待合成文本
    Status   TaskStatus  // 任务状态
    Seq      uint64      // 播放序号（完成后填充）
    Audio    []byte      // 音频数据（完成后填充）
    Canceled bool        // 是否已被取消
}
```

### TaskStatus

```go
const (
    TaskStatusPending      TaskStatus = iota // 等待合成
    TaskStatusSynthesizing                  // 合成中
    TaskStatusCompleted                     // 合成完成，已入队
    TaskStatusFailed                        // 合成失败
    TaskStatusCanceled                      // 被新请求取消
)
```

### playJob

```go
type playJob struct {
    taskID    uint64      // 关联的任务 ID
    seq       uint64      // 播放序号
    enqueuedAt time.Time  // 入队时间
    audio     []byte      // MP3 音频数据
    voiceType string      // 音色类型
}
```

## 消息流程

### 1. 接收消息

```
nc -U ~/.config/iSpeak/ispeak.sock
        │
        ▼
handleConnection()
  - 解析音色前缀 {source:claude} 或 {source:codex}
  - cleanText() 过滤格式符号
  - 创建 Task，状态 = pending
```

### 2. 打断旧任务

```
oldActiveTask.cancel()
  - 如果状态 == pending → 标记 canceled
  - 如果状态 == synthesizing → 不管，等它完
```

### 3. 发起新合成

```
ttsCtx, ttsCancel = context.WithCancel()
task.Status = synthesizing
synthesize(ttsCtx, ...)
  - HTTP POST 火山 TTS API
  - SSE 流式解析 base64 音频
  - 返回 MP3 数据
```

### 4. 入队播放

```
task.Status = completed
seq = nextSequence()
task.Seq = seq
queue <- playJob{taskID, seq, audio}
  │
  ▼
playbackWorker:
  - 等待 queue
  - 严格按 seq 顺序播放
  - 乱序缓存，超时跳过
  - afplay 播放 MP3
```

## 打断策略

### 节省费用逻辑

| 旧任务状态 | 新消息到达时 | 费用 |
|-----------|-------------|------|
| pending | 取消（标记 canceled） | 不计费 |
| synthesizing | 不取消 | 已产生，等它完 |
| completed | 不取消 | 已入队播放 |

### 为什么正在合成的不取消？

火山引擎 TTS API 特性：
- 服务端收到完整文本后开始合成
- 取消请求只能停止**接收**数据，不能停止**合成**
- 服务端该算的已经算了

所以取消正在合成的任务没有意义，不如让它跑完。

## 并发模型

```
taskMu: sync.Mutex   // 保护 tasks map 和 task 状态
taskIDMu: sync.Mutex // 保护 nextTaskID 自增
ttsCtxMu: sync.Mutex // 保护 ttsCtx/ttsCancel
seqMu: sync.Mutex    // 保护 nextSeq 自增
cmdMu: sync.Mutex    // 保护 currentCmd (afplay)
```

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

- **panic recover**: 关键 goroutine 有 recover，防止崩溃
- **序号连续**: 严格按 seq 顺序播放，不乱序
- **缓冲队列**: 上限 64 条，超时 60s 跳过
- **播放重试**: 失败自动重试 1 次
- **日志轮转**: 10MB/份，保留 3 份
- **Temp 清理**: 进程级 tempDir，退出时删除
- **配置热更新**: 无需重启服务
