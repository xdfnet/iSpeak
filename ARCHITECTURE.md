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
Claude Code / Codex 终端
│
├─ 回复文字
│   └─ Stop Hook → ~/.config/iSpeak/hook-speak.sh
│       │
│       ├─ 读 transcript_path JSONL
│       ├─ 提取 30s 内所有 assistant text
│       └─ nc -U /tmp/ispeak.sock
│
└─ 手动触发
    └─ ispeak "文本" → nc -U /tmp/ispeak.sock

        Unix Socket (/tmp/ispeak.sock)
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
    │  ├─ cleanText() 过滤格式          │
    │  └─ enqueue()     入播放队列       │
    │                                   │
    │  playbackWorker()                 │
    │  ├─ synthesize()  调用 TTS         │
    │  └─ play()        afplay          │
    └───────────────────────────────────┘
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

### 1. 全文一次播放，不拆句

全文一次 TTS → 一次 afplay。`cleanText()` 过滤掉 markdown 表格、分隔线、粗体/代码标记等格式符，保留自然朗读文本。

**原因**: 实测发现拆句反而破坏语义连贯性，整段播放效果更好。

### 2. cleanText 过滤规则

- 跳过 markdown 表格行 (`| ... |`)
- 跳过表格分隔行 (`|---|---|`)
- 移除 `**` `*` `` ` `` `#` `>` 等行内标记
- 换行替换为逗号

不做自然语言理解，仅做字符级过滤。

### 3. afplay 而非 AVAudioPlayer

Go 没有 Foundation 绑定，用 `exec.Command("afplay", tmpFile)`。

**代价**: 无法精确控制音量（当前用系统音量）。  
**收益**: 零依赖，标准库搞定。

### 4. TTS 并发 + 播放串行

当前采用两阶段：

1. 连接协程先做 TTS（并发，信号量限流）
2. 合成好的音频再入播放队列（单 worker 串行播放）

这样既保留了“只播一条”的稳定体验，也降低了高并发下的总等待时间。

默认参数：
- 播放队列长度：`128`
- TTS 并发上限：`4`
- TTS 重试：失败后最多再试 `1` 次（总尝试 2 次）

### 5. 不做媒体控制

当前版本不做 Music/Spotify/汽水音乐 的暂停与恢复控制，职责保持为纯播报。

### 6. 配置三层优先级

```
~/.config/iSpeak/config.json  >  环境变量  >  代码默认值
```

- config.json: 推荐方式，和 dotfiles 一起管理
- 环境变量: 开发调试用
- 默认值: endpoint/resourceId/voiceType 有合理 fallback

### 7. launchd 守护

```xml
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
```

退出自动重启，开机自动加载。

### 8. 进程防护

- `handleConnection` 和 `playbackWorker` 都有 `panic recover`，避免单条异常导致进程退出。
- `ispeak status` 提供最小健康检查（launchd / socket / 二进制路径）。

## 文件清单

| 文件 | 用途 |
|------|------|
| `main.go` | 全部逻辑 (~280行) |
| `scripts/ispeak` | 运行命令（status/test/restart/...） |
| `configs/` | 部署模板 (config/hook/plist) |
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
| 并发 | TTS 并发（默认 4），播放串行 |
