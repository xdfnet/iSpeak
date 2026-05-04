# iSpeak

小而稳的本地 TTS 播报服务：接收文本，转换语音并顺序播放。

## 组成

- `~/.local/bin/ispeakd`：守护进程（监听 `/tmp/ispeak.sock`）
- `~/.local/bin/ispeak`：命令入口
- `~/.local/bin/ispeak-claude`：Claude Code 用（湾湾音色）
- `~/.local/bin/ispeak-codex`：Codex 用（桃子音色）

## 快速安装

```bash
cd /path/to/iSpeak
make install
ispeak status
ispeak test "飞哥你好"
```

## 常用命令

```bash
ispeak "任务完成"          # 日常播报（默认音色）
ispeak test               # 自检播报（默认测试文案）
ispeak test "飞哥你好"     # 自检播报（自定义文案）
ispeak status              # 查看服务/socket/音色配置
ispeak restart             # 重启服务
ispeak recover             # 重启 + 状态检查 + 测试播报
ispeak logs 80             # 查看最近 80 行日志
ispeak tail                # 实时日志
```

## 音色指定

```bash
ispeak "文本"           # 默认音色（小何）
ispeak-claude "文本"    # 湾湾音色（Claude Code）
ispeak-codex "文本"     # 桃子音色（Codex）
```

## 配置

配置文件路径：`~/.config/iSpeak/config.json`

```json
{
  "apiKey": "bfa4b2a7-7465-44d2-9626-d26abfc24baa",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "defaultVoice": {
    "voice_type": "zh_female_xiaohe_uranus_bigtts",
    "resourceId": "seed-tts-2.0"
  },
  "sourceVoices": {
    "claude": {
      "voice_type": "zh_female_wanwanxiaohe_moon_bigtts",
      "resourceId": "seed-tts-1.0"
    },
    "codex": {
      "voice_type": "zh_female_tianmeitaozi_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    }
  }
}
```

## Hook 配置

Claude Code (`~/.claude/settings.json`)：
```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash /Users/admin/.config/iSpeak/hook-speak.sh claude",
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
        "command": "bash /Users/admin/.config/iSpeak/hook-speak.sh codex",
        "timeout": 30
      }]
    }]
  }
}
```

## 音色用途

| 命令 | 音色 | 来源 |
|------|------|------|
| `ispeak` | 小何 | seed-tts-2.0 |
| `ispeak-claude` | 湾湾 | Claude Code |
| `ispeak-codex` | 桃子 | Codex |

## CLI 对话测试

```bash
# Claude Code
claude -p "说一句话"

# Codex
codex exec "说一句话"
```

## 稳定性策略

- TTS 并发、播放串行（避免音频重叠）
- TTS 并发上限：`4`
- 失败自动重试：`1` 次
- 关键 worker 带 `panic recover`

## 路径速查

- `~/Library/LaunchAgents/com.iSpeak.plist`
- `/tmp/ispeak.sock`
- `/tmp/iSpeak.log`
- `~/.config/iSpeak/config.json`
- `~/.config/iSpeak/hook-speak.sh`
- `~/.local/bin/ispeakd`
- `~/.local/bin/ispeak`, `ispeak-claude`, `ispeak-codex`

## License

MIT
