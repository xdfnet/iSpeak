# iSpeak

小而美的本地 TTS 播报服务。接收文本，通过字节跳动火山引擎 TTS 生成音频，按序号顺序播放。

**核心特性**：新消息打断旧播放 + TTS 合成可取消，不为旧消息付冤枉钱。

## 组成

```
~/.local/bin/ispeakd      守护进程（监听 Unix Socket）
~/.local/bin/ispeak       命令行入口
~/.local/bin/ispeak-claude  Claude Code 专用音色
~/.local/bin/ispeak-codex   Codex 专用音色
```

## 快速开始

```bash
# 安装
git clone https://github.com/yourname/iSpeak.git
cd iSpeak
make install

# 自检
ispeak status
ispeak test "你好世界"
```

## 常用命令

```bash
ispeak "任务完成"          # 播报文本（默认音色）
ispeak test               # 自检播报
ispeak test "飞哥你好"     # 自检播报（自定义文案）
ispeak status             # 查看服务状态
ispeak restart            # 重启服务
ispeak recover            # 重启 + 状态检查 + 测试播报
ispeak logs 80            # 查看最近 80 行日志
ispeak tail               # 实时日志
```

## 音色指定

```bash
ispeak "文本"              # 默认音色
ispeak-claude "文本"      # Claude 专用音色
ispeak-codex "文本"       # Codex 专用音色
```

## 配置

编辑 `~/.config/iSpeak/config.json`：

```json
{
  "apiKey": "YOUR-API-KEY",
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

**如何获取 API Key？**
前往 [火山引擎 TTS 控制台](https://console.volcengine.com/tts) 申请。

## Claude Code / Codex Hook 集成

Claude Code (`~/.claude/settings.json`)：
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

Codex (`~/.codex/hooks.json`)：
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

## 工作原理

```
CLI (nc socket)
    ↓
Unix Socket /tmp/ispeak.sock
    ↓
ispeakd 守护进程
    ├─ TTS 合成（context cancel 支持，新请求取消旧请求）
    └─ playbackWorker（串行播放，按序号排序）
              ↓
        afplay 播放
```

**打断机制**：新消息到达时，正在播放的音频被 kill，正在合成的 TTS 请求被 context cancel。只为最终想听的那条消息付费。

## 稳定性设计

- 播放严格按序号顺序，不乱序
- 缓冲队列上限 64 条，超时 60s 跳过
- 播放失败自动重试 1 次
- 关键 goroutine 有 `panic recover`
- 配置热更新，无需重启服务

## 路径速查

| 路径 | 说明 |
|------|------|
| `~/Library/LaunchAgents/com.iSpeak.plist` | macOS launchd 服务 |
| `/tmp/ispeak.sock` | Unix Socket |
| `/tmp/iSpeak.log` | 日志文件 |
| `~/.config/iSpeak/config.json` | 配置文件 |
| `~/.config/iSpeak/hook-speak.sh` | Claude/Codex Hook 脚本 |
| `~/.local/bin/ispeakd` | 守护进程 |
| `~/.local/bin/ispeak*` | CLI 软链 |

## 开发

```bash
make build    # 编译
make install  # 安装到 ~/.local/bin 并启动服务
make deploy   # 完整部署（install + 配置文件）
```

## License

MIT
