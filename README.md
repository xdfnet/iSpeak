# iSpeak

TTS 播报守护进程。监听 Unix Socket，收到文本 → 字节跳动 TTS → 串行播放。

**Go 单文件核心逻辑 · 0 外部依赖 · 开机自启 · 桃子音色。**

## 架构

```
Claude Code / Codex              iSpeak
┌──────────┐   Stop Hook       ┌─────────────────────┐
│  回复    │ ───────────────→  │  Unix Socket 监听    │
└──────────┘                   │  ↓                   │
  speak "文本"  ─────────────→ │  cleanText → 入队 →    │
                               │  TTS → afplay 播放     │
                               └─────────────────────┘
```

说明：当前版本不做媒体暂停/恢复控制，只负责文本播报；多条消息按队列串行播放。

## 全新部署

```bash
cd /path/to/iSpeak
make deploy                                     # 一键：编译 + 安装 + 配置 + 自启
# 编辑 ~/.config/iSpeak/config.json 填入 TTS 凭证
```

分步操作：

```bash
make build      # 编译
make install    # 安装到 /usr/local/bin
make deploy     # 部署配置 + 自启动
make start      # 启动
make stop       # 停止
make restart    # 重启
make log        # 查看日志
make clean      # 清理二进制
make uninstall  # 完全卸载
```

## 配置

`~/.config/iSpeak/config.json`:

```json
{
  "appId": "3059945724",
  "accessToken": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "resourceId": "seed-tts-2.0",
  "voiceType": "zh_female_tianmeitaozi_uranus_bigtts"
}
```

也支持环境变量：`IAGENT_TTS_APP_ID`、`IAGENT_TTS_ACCESS_TOKEN` 等。

## 使用

```bash
speak "飞哥你好"
echo "任务完成" | speak
```

## 自启动

```bash
launchctl load ~/Library/LaunchAgents/com.iSpeak.plist   # 启用
launchctl unload ~/Library/LaunchAgents/com.iSpeak.plist # 停用
tail -f /tmp/iSpeak.log                                  # 日志
```

plist 内容：

```xml
<!-- configs/com.iSpeak.plist -->
<dict>
    <key>Label</key>            <string>com.iSpeak</string>
    <key>ProgramArguments</key> <array><string>/usr/local/bin/iSpeak</string></array>
    <key>RunAtLoad</key>        <true/>   <!-- 开机自启 -->
    <key>KeepAlive</key>        <true/>   <!-- 崩溃自动重启 -->
</dict>
```

部署路径：`~/Library/LaunchAgents/com.iSpeak.plist`。`make deploy` 自动复制 + 加载。

## Claude Code 集成

### 1. Stop Hook

在 `~/.claude/settings.json` 的 `hooks` 对象中**合并**以下内容（不要覆盖已有配置）：

```json
"Stop": [{
  "hooks": [{
    "type": "command",
    "command": "bash $HOME/.config/iSpeak/hook-speak.sh",
    "timeout": 30
  }]
}]
```

完整示例（如果你已有其他 hooks）：

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash $HOME/.config/iSpeak/hook-speak.sh",
        "timeout": 30
      }]
    }]
  }
}
```

### 2. Hook 脚本

`~/.config/iSpeak/hook-speak.sh` — Claude 每次回复完自动触发：

- 从 `transcript_path` JSONL 提取最近 30 秒内所有 assistant 文本
- 逐句送到 iSpeak Socket 播放
- iAgent 内部调 Claude 时设 `ISPEAK_SKIP=1`，Hook 检测到自动跳过（避免双重播报）

### 3. iAgent ISPEAK_SKIP

`AgentService.swift` 在调 Claude 时注入 `ISPEAK_SKIP=1` 环境变量，防止 iAgent 语音交互 + 终端回复各播一遍。

## Codex CLI 集成

### 1. 启用 Hooks

```bash
codex features enable codex_hooks
```

或 `~/.codex/config.toml`:

```toml
[features]
codex_hooks = true
```

### 2. Stop Hook

`~/.codex/hooks.json`:

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash $HOME/.config/iSpeak/hook-speak.sh",
        "timeout": 30
      }]
    }]
  }
}
```

Claude Code 和 Codex 共用同一个 Hook 脚本，无需额外配置。

## 路径速查

| 路径 | 说明 |
|------|------|
| `/usr/local/bin/iSpeak` | 守护进程 |
| `/usr/local/bin/speak` | CLI 客户端（由仓库内 `speak` 脚本安装） |
| `~/.config/iSpeak/config.json` | TTS 配置 |
| `~/.config/iSpeak/hook-speak.sh` | Claude Hook 脚本 |
| `~/.config/iSpeak/hook.log` | 播报日志 |
| `~/Library/LaunchAgents/com.iSpeak.plist` | 自启配置 |
| `/tmp/ispeak.sock` | 播报 Socket |
| `/tmp/iSpeak.log` | launchd 日志 |

## License

MIT

## 项目文件

```
iSpeak/
├── main.go              Go 源码
├── speak                CLI 客户端
├── go.mod
├── README.md
├── ARCHITECTURE.md
├── .gitignore
├── configs/
│   ├── config.example.json    TTS 配置模板
│   ├── hook-speak.sh          Claude Hook 脚本
│   └── com.iSpeak.plist       launchd 自启配置
│
部署目标:
  /usr/local/bin/iSpeak
  /usr/local/bin/speak
  ~/.config/iSpeak/{config.json, hook-speak.sh}
  ~/Library/LaunchAgents/com.iSpeak.plist
```
