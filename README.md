# iSpeak

![Version](https://img.shields.io/badge/version-1.8.1-blue)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://golang.org/dl/)
![Platform](https://img.shields.io/badge/platform-macOS-green)

iSpeak 让 AI 编程助手开口说话。你写代码，它播结果——眼睛休息，耳朵来听。

适合 Claude Code、Codex、Copilot CLI 或 Pi 常驻后台的开发者。AI 完成任务后自动播报；你发新消息时，未开始的旧播报会被丢弃，不花冤枉钱。

## 效果示例

```
# 默认音色：温柔女声
ispeak "Pull request 已合并，3 个测试通过"
```

## 为什么选 iSpeak

| 问题 | 方案 |
|------|------|
| AI 生成多条回复，TTS 账单飞涨 | 新消息丢弃旧排队消息，避免无效合成 |
| 回复快慢不一，音频播报乱序 | 单 channel goroutine，串行顺序稳定 |
| 修改配置要重启服务 | 热更新：编辑 `config.json` 立即生效 |
| 默认音色太无聊 | hook 按来源前缀选择音色 |
| Copilot 播到上一条回复 | 记录已播文本 hash，等待最新 transcript 落盘 |

## 快速上手

**npm 安装：**

```bash
npm i -g @xdfnet/ispeak
```

当前 npm 安装会在本机编译 `ispeakd`，需要已安装 Go。主播放链路使用 macOS 原生 `AVAudioEngine`，不依赖 `ffmpeg`。合成失败记录日志，播放器异常自动重建。

**源码安装：**

```bash
git clone https://github.com/xdfnet/iSpeak.git && cd iSpeak && make install
```

安装后编辑 API Key，然后验证：

```bash
open ~/.config/iSpeak/config.json
ispeak status
ispeak "iSpeak 准备好了"
ispeak-copilot "Copilot 音色测试"
```

## 工作原理

```
你："重构 auth 模块"
        │
        ▼
┌─────────────────────────────────────────────────────┐
│  ispeakd — Mac 上常驻的守护进程                      │
│                                                       │
│   通过 Unix Socket 接收文本                          │
│         │                                            │
│         ▼                                           │
│   Player (channel)                                  │
│   buffer=1 + drain（新消息丢弃旧排队消息）             │
│         │                                            │
│         ▼                                           │
│   TTS SSE → AVAudioEngine（单实例复用）              │
│         │                                            │
│         ▼                                           │
│   失败记录日志，播放器异常自动重建                    │
└─────────────────────────────────────────────────────┘
```

## 语音清洗规则

清洗只影响 TTS 播报内容，不改变 AI 助手屏幕显示内容。

播报前会过滤或简化这些内容：

- Markdown 格式符号：标题 `#`、加粗 `**`、反引号、引用 `>`
- Markdown 表格整块：表头、分隔线、表格内容都不播
- 代码块：``` 包裹的内容不播
- artifact / HTML 内容：不播生成的页面源码
- Markdown 链接：只保留链接标题，不播 URL
- 绝对路径：简化为“路径”
- 长 commit hash、UUID、长 ID：不播
- 下载进度噪声：速度、ETA、预计剩余时间、ANSI 控制符

保留优先级：结论、成功/失败状态、需要用户操作的下一步、关键错误原因。

## 全部命令

```bash
ispeak "消息"          # 默认音色播报
ispeak-claude "消息"   # Claude 音色播报
ispeak-codex "消息"    # Codex 音色播报
ispeak-copilot "消息"  # Copilot 音色播报
ispeak-pi "消息"       # Pi 音色播报
ispeak status          # 服务状态
ispeak restart         # 重启服务
ispeak version         # 版本
```

## 配置说明

`~/.config/iSpeak/config.json`：

```json
{
  "apiKey": "你的火山引擎 API Key",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse",
  "defaultVoice": {
    "voice_type": "zh_female_mizai_uranus_bigtts",
    "resourceId": "seed-tts-2.0"
  },
  "sourceVoices": {
    "claude": {
      "voice_type": "zh_female_tianmeitaozi_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    },
    "codex": {
      "voice_type": "zh_female_xiaohe_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    },
    "pi": {
      "voice_type": "zh_male_taocheng_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    },
    "copilot": {
      "voice_type": "zh_male_dayi_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    }
  }
}
```

音色库参考 [火山引擎 TTS 控制台](https://console.volcengine.com/tts)，填写对应的 `voice_type` 和 `resourceId` 即可。

## 集成说明

`hook-speak.sh` 统一服务 Claude Code、Codex、Copilot CLI；Pi 使用独立的 Extension 脚本。详细提取逻辑见 [docs/hook-text-extraction.md](/Users/admin/iCode/iSpeak/docs/hook-text-extraction.md)。

### Claude Code

Hook 配置在 `~/.claude/settings.json`：

```json
{
  "hooks": {
    "Stop": [
      {
        "type": "command",
        "command": "bash ~/.config/iSpeak/hook-speak.sh claude"
      }
    ]
  }
}
```

### Codex

Hook 配置在 `~/.codex/hooks.json`：

```json
{
  "hooks": {
    "Stop": [
      {
        "type": "command",
        "command": "bash /Users/admin/.config/iSpeak/hook-speak.sh codex"
      }
    ]
  }
}
```

首次加载后 Codex 会要求信任新的 Stop hook。进入 Codex 后执行 `/hooks`，找到对应条目并信任即可。

### Copilot CLI

Hook 配置在 `~/.copilot/hooks/ispeak-hook.json`：

```json
{
  "version": 1,
  "hooks": {
    "agentStop": [
      {
        "type": "command",
        "bash": "bash $HOME/.config/iSpeak/hook-speak.sh copilot",
        "timeoutSec": 10
      }
    ]
  }
}
```

Copilot CLI 的 `agentStop` 不直接提供 `last_assistant_message`，而是通过 `transcriptPath` 指向 JSONL 日志文件。`hook-speak.sh` 会自动识别并读取 transcript 提取最后一条 assistant 消息。重启 Copilot CLI 后生效。

Copilot 的 transcript 有时会晚于 `agentStop` 事件落盘。脚本会把上次已播文本的 hash 写入 `~/.config/iSpeak/hook.last`；如果当前读到的仍是上一条回复，会最多等待 4 秒，直到新的 `assistant.message` 出现再播。

### Pi

Pi 使用 Extension 机制接入，`make install` 会自动部署。

Pi 的全局设置（`~/.pi/agent/settings.json`）中指定扩展路径：

```json
{
  "extensions": [
    "/Users/你的用户名/.config/iSpeak/ispeak.ts"
  ]
}
```

无需额外配置，每次 Pi 回复完成后自动播报。多 Agent 共用同一个 `ispeakd` 服务，`config.json` 中通过 `sourceVoices.pi` 选择专属音色。

## 开发命令

```bash
make build      # 编译 ispeakd
make test       # 运行 Go/race/hook/npm 打包预检
make install    # 安装并启动服务（自动自检）
make deploy     # 安装 + 部署配置文件（不覆盖已有配置）
make uninstall  # 卸载（停止服务 + 删除文件）
make clean      # 清理编译产物
make help       # 显示帮助
```

## 文件路径

| 文件 | 用途 |
|------|------|
| `~/Library/LaunchAgents/com.ispeak.plist` | macOS 自动启动服务 |
| `~/.config/iSpeak/ispeak.sock` | Unix Socket |
| `~/.config/iSpeak/ispeak.log` | 日志（轮转） |
| `~/.config/iSpeak/config.json` | 你的 API Key 和音色配置 |
| `~/.config/iSpeak/hook.last` | Copilot hook 去除上一条回复的状态文件 |
| `~/.config/iSpeak/hook-speak.sh` | Claude / Codex / Copilot CLI Hook 脚本 |
| `~/.config/iSpeak/ispeak.ts` | Pi Extension 脚本 |
| `~/.local/bin/ispeak-*` | 默认和各来源 CLI 入口 |
| `~/.copilot/hooks/ispeak-hook.json` | Copilot CLI Hook 配置 |

## License

MIT — 随便用，随便改。
