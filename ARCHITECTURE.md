# iSpeak 架构文档

## 定位

iSpeak 是播报能力的独立实现。它不做语音采集、不做 AI 对话、不感知上下文——只做一件事：**收到文字，说出来**。

## 为什么独立

iAgent 最早把 TTS 播报作为 StreamingSpeaker 模块嵌在 AgentControlCenter 里。问题是：

- iAgent 启动依赖麦克风，没麦克风就报"启动失败"
- 菜单栏状态和播报耦合，难以区分"语音不可用"和"服务挂了"
- Hook 触发要等 AgentControlCenter 整条链路就绪
- 播报是高频操作，不应受语音采集状态影响

拆分后：iAgent 管语音交互，iSpeak 管播报，互不阻塞。

## 系统全貌

```
Claude Code 终端
│
├─ 回复文字
│   └─ Stop Hook → /tmp/hook_debug.sh
│       │
│       ├─ 读 transcript_path JSONL
│       ├─ 提取 30s 内所有 assistant text
│       └─ nc -U /tmp/iagent.tts.sock
│
└─ 手动触发
    └─ speak "文本" → nc -U /tmp/iagent.tts.sock

        Unix Socket (/tmp/iagent.tts.sock)
                    │
    ┌───────────────▼───────────────────┐
    │           iSpeak (Go)             │
    │                                   │
    │  main()                           │
    │  ├─ net.Listen("unix", socket)    │
    │  └─ for { accept → go handle }    │
    │                                   │
    │  handleConnection()               │
    │  ├─ read all text                 │
    │  ├─ vadMute() → iAgent VAD 暂停   │
    │  ├─ splitSentences()              │
    │  └─ for each sentence:            │
    │       ├─ synthesize()  调用 TTS    │
    │       └─ play()        afplay     │
    │  └─ vadUnmute() → iAgent VAD 恢复 │
    └───────────────────────────────────┘
           │ mute / unmute
           ▼
    /tmp/iagent.vad.sock → iAgent VoiceService
                    │
    ┌───────────────▼───────────────────┐
    │     字节跳动 TTS API              │
    │  openspeech.bytedance.com         │
    │  /api/v3/tts/unidirectional       │
    │                                   │
    │  请求: POST JSON                  │
    │  响应: SSE 流, base64 MP3 块      │
    └───────────────────────────────────┘
```

## 关键设计决策

### 1. 全文一次发送，服务端拆句

`speak` 脚本把整段文字一次性 `nc` 发送。拆句在 `handleConnection` 里做。

**原因**: 如果客户端拆句、逐句 `nc`，每次新连接会触发独立的 `handleConnection` → 进程级并发 → 后一句的 `play()` 打断前一句。

### 2. 串行播放

每句合成→播放→等播完，再处理下一句。

**不做**: 预合成全部句子再连续播放。原因：第一句就能让用户听到，延迟更低。

### 3. afplay 而非 AVAudioPlayer

Go 没有 Foundation 绑定，用 `exec.Command("afplay", tmpFile)`。

**代价**: 无法精确控制音量（当前用系统音量）。  
**收益**: 零依赖，标准库搞定。

### 4. 配置三层优先级

```
~/.config/iSpeak/config.json  >  环境变量  >  代码默认值
```

- config.json: 推荐方式，和 dotfiles 一起管理
- 环境变量: 开发调试用
- 默认值: endpoint/resourceId/voiceType 有合理 fallback

### 5. launchd 守护

```xml
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
```

退出自动重启，开机自动加载。

## 文件清单

| 文件 | 用途 |
|------|------|
| `main.go` | 全部逻辑 (260行) |
| `speak` | Shell 客户端 |
| `README.md` | 使用说明 |
| `ARCHITECTURE.md` | 本文档 |
| `go.mod` | Go module |

## 依赖

```
Go 标准库:
  net         Unix Socket
  net/http    TTS SSE 请求
  os/exec     afplay 播放
  encoding/json
  bufio
  log
```

零外部依赖。`go build` 秒级完成。

## 性能

| 指标 | 值 |
|------|------|
| 二进制 | 8.2MB |
| 内存 | < 10MB |
| 启动 | < 100ms |
| TTS 延迟 (首句) | ~800ms |
| 并发 | 串行播放，多连接排队 |
