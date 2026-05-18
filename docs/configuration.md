# 配置说明

iSpeak 的运行配置位于：

```text
~/.config/iSpeak/config.json
```

源码中的初始化模板位于：

```text
configs/config.example.json
```

首次安装时，如果用户配置不存在，安装脚本会复制初始化模板；如果用户配置已存在，不会覆盖。

## 完整示例

```json
{
  "apiKey": "your-api-key",
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
    "copilot": {
      "voice_type": "zh_male_dayi_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    },
    "pi": {
      "voice_type": "zh_male_taocheng_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    }
  }
}
```

## 字段

`apiKey`：火山引擎控制台获取的新版 API Key。运行时通过 `X-Api-Key` 请求头发送。

`endpoint`：TTS 流式接口地址。当前默认使用 SSE：

```text
https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse
```

`defaultVoice`：未带 `{source:...}` 前缀，或来源没有匹配音色时使用。

`sourceVoices`：来源到音色的映射。当前约定来源：

```text
claude
codex
copilot
pi
```

每个音色包含：

- `voice_type`：火山引擎音色 ID
- `resourceId`：资源 ID，豆包 TTS 2.0 通常为 `seed-tts-2.0`

## 来源选择

CLI wrapper 会自动注入来源：

```bash
ispeak-claude "文本"   # {source:claude}文本
ispeak-codex "文本"    # {source:codex}文本
ispeak-copilot "文本"  # {source:copilot}文本
ispeak-pi "文本"       # {source:pi}文本
```

Hook 也会注入来源：

```bash
bash ~/.config/iSpeak/hook-speak.sh claude
bash ~/.config/iSpeak/hook-speak.sh codex
bash ~/.config/iSpeak/hook-speak.sh copilot
```

`ispeakd` 收到文本后调用 `extractVoicePrefix` 解析 `{source:xxx}`。如果 `xxx` 在 `sourceVoices` 里存在，就使用对应音色；否则使用 `defaultVoice`。

## 热更新

配置每次连接都会尝试加载，并用 mtime 缓存减少 I/O。

这意味着修改 `config.json` 后无需重启服务，下一次 `ispeak "文本"` 或 hook 触发就会使用新配置。

如果配置格式错误或缺少必填字段：

- 已有有效缓存时，继续使用上一份有效配置
- 启动时无有效配置会直接报错退出
- 单次连接配置错误会跳过本次播报并写日志

## endpoint 迁移

安装脚本会把旧 endpoint：

```text
https://openspeech.bytedance.com/api/v3/tts/unidirectional
```

迁移为：

```text
https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse
```

迁移前会备份到：

```text
~/.config/iSpeak/config.json.bak
```

## 本机配置与模板

`configs/config.example.json` 是新用户初始化模板。

`~/.config/iSpeak/config.json` 是本机真实配置，包含个人 API Key，不能提交到仓库。

发布新版本时，如果默认音色策略变化，需要同步：

- `configs/config.example.json`
- `README.md`
- `AGENTS.md`
- `docs/architecture.md`
- `docs/hook-text-extraction.md`
