# iSpeak

![Version](https://img.shields.io/badge/version-1.6.0-blue)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://golang.org/dl/)
![Platform](https://img.shields.io/badge/platform-macOS-green)

iSpeak 让 AI 编程助手开口说话。你写代码，它播结果——眼睛休息，耳朵来听。

适合 Claude Code 或 Codex 常驻后台的开发者。AI 完成任务后自动播报；你发新消息时，旧播报立即中断，不花冤枉钱。

## 效果示例

```
# 默认音色：温柔女声
ispeak "Pull request 已合并，3 个测试通过"

# Claude 模式：专属音色
ispeak-claude "Code review 完成，发现 2 处可优化"

# Codex 模式：另一种音色
ispeak-codex "构建完成，耗时 12 秒"
```

## 为什么选 iSpeak

| 问题 | 方案 |
|------|------|
| AI 生成多条回复，TTS 账单飞涨 | 新消息只保留最新待合成任务，避免无效合成 |
| 回复快慢不一，音频播报乱序 | 单 speak worker，FIFO 顺序稳定 |
| 修改配置要重启服务 | 热更新：编辑 `config.json` 立即生效 |
| 默认音色太无聊 | 来源专属音色，Claude 和 Codex 声音不同 |

## 快速上手

**快速安装：**

```bash
git clone https://github.com/xdfnet/iSpeak.git && cd iSpeak && make install
```

安装时手动输入 API Key，然后验证：

```bash
ispeak status
ispeak test "iSpeak 准备好了"
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
│   任务引擎                                           │
│   （pending_synth → speaking → delete）              │
│         │                                            │
│         ▼                                           │
│   单 Worker 流式链路                                 │
│   （SSE audio chunk → 播放器 stdin）                  │
│         │                                            │
│         ▼                                           │
│   流式播放器                                         │
│   （优先 ffplay stdin，无 ffplay 回退 afplay）         │
└─────────────────────────────────────────────────────┘
```

**任务状态流转：**
```
pending_synth → speaking → delete
```

## 全部命令

```bash
ispeak "消息"    # 播报
ispeak status    # 服务状态
ispeak restart   # 重启服务
ispeak version   # 版本
```

语音专属快捷命令（指向 ispeak 的软链接）：
```bash
ispeak-claude "消息"   # Claude 专属音色
ispeak-codex "消息"    # Codex 专属音色
```

## 配置说明

`~/.config/iSpeak/config.json`：

```json
{
  "apiKey": "你的火山引擎 API Key",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
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
      "voice_type": "zh_male_shaonianzixin_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    }
  }
}
```

音色库参考 [火山引擎 TTS 控制台](https://console.volcengine.com/tts)，填写对应的 `voice_type` 和 `resourceId` 即可。

## 集成说明

### Claude Code

在 `~/.claude/settings.json` 中添加 Stop Hook：

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash $HOME/.config/iSpeak/hook-speak.sh claude",
        "timeout": 30
      }]
    }]
  }
}
```

### Codex

在 `~/.codex/hooks.json` 中添加 Stop Hook：

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash $HOME/.config/iSpeak/hook-speak.sh codex",
        "timeout": 30
      }]
    }]
  }
}
```

## 开发命令

```bash
make build      # 编译 ispeakd
make install    # 安装并启动服务（自动自检）
make deploy     # 安装 + 部署配置文件（不覆盖已有配置）
make uninstall  # 卸载（停止服务 + 删除文件）
make clean      # 清理编译产物
make help       # 显示帮助
```

## 文件路径

| 文件 | 用途 |
|------|------|
| `~/Library/LaunchAgents/com.iSpeak.plist` | macOS 自动启动服务 |
| `~/.config/iSpeak/ispeak.sock` | Unix Socket |
| `~/.config/iSpeak/ispeak.log` | 日志（轮转） |
| `~/.config/iSpeak/config.json` | 你的 API Key 和音色配置 |
| `~/.config/iSpeak/hook-speak.sh` | Claude/Codex Hook 脚本 |

## License

MIT — 随便用，随便改。
