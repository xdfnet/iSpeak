# 安装与集成

本文说明如何安装 iSpeak，并接入 Claude Code、Codex、Copilot CLI 和 Pi。

## 前置条件

- macOS
- Go：npm 安装和源码安装都会在本机编译 `ispeakd`
- 火山引擎 TTS API Key
- `~/.local/bin` 在 `PATH` 中，方便直接运行 `ispeak`

## npm 安装

```bash
npm i -g @xdfnet/ispeak
```

安装脚本会：

- 编译 `ispeakd`
- 安装 `~/.local/bin/ispeakd`
- 安装 `ispeak`、`ispeak-claude`、`ispeak-codex`、`ispeak-copilot`、`ispeak-pi`
- 首次创建 `~/.config/iSpeak/config.json`
- 安装 `~/.config/iSpeak/hook-speak.sh`
- 安装 `~/.config/iSpeak/ispeak.ts`
- 写入并加载 `~/Library/LaunchAgents/com.ispeak.plist`

## 源码安装

```bash
git clone https://github.com/xdfnet/iSpeak.git
cd iSpeak
make install
```

`make install` 与 npm postinstall 的部署结果一致。

## 初始化配置

安装后编辑：

```bash
open ~/.config/iSpeak/config.json
```

至少需要填入 `apiKey`。默认 endpoint 是：

```text
https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse
```

配置改完无需重启，daemon 每次收到 socket 连接时会按 mtime 热加载配置。

## 验证服务

```bash
ispeak status
ispeak "iSpeak 准备好了"
ispeak-claude "Claude 音色测试"
ispeak-codex "Codex 音色测试"
ispeak-copilot "Copilot 音色测试"
ispeak-pi "Pi 音色测试"
```

## Claude Code

`~/.claude/settings.json`：

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "bash ~/.config/iSpeak/hook-speak.sh claude"
          }
        ]
      }
    ]
  }
}
```

Claude Code 官方 hook 是三层结构：事件 → matcher group → handlers。`Stop` hook 直接提供 `last_assistant_message`，脚本会读取该字段并发送 `{source:claude}`。

## Codex

`~/.codex/hooks.json`：

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "bash /Users/admin/.config/iSpeak/hook-speak.sh codex",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

Codex 官方 hook 是三层结构：事件 → matcher group → handlers。`Stop` 的 `matcher` 当前会被忽略，可省略。首次加载后，Codex 可能要求信任新的 Stop hook。进入 Codex 后执行 `/hooks`，找到该条目并信任。

## Copilot CLI

`~/.copilot/hooks/ispeak-hook.json`：

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

Copilot CLI 的 `agentStop` 只提供 `transcriptPath`。`hook-speak.sh` 会读取 JSONL transcript，只取最新 `user.message` 之后的 assistant，并用 `~/.config/iSpeak/hook.last` 记录已播 assistant id，等待最新消息落盘，避免播到上一条回复。

改动 hook 配置后，重启 Copilot CLI。

## Pi

`make install` 会部署：

```text
~/.config/iSpeak/ispeak.ts
```

Pi 全局配置示例：

```json
{
  "extensions": [
    "/Users/你的用户名/.config/iSpeak/ispeak.ts"
  ]
}
```

Pi Extension 会发送 `{source:pi}`，由 `sourceVoices.pi` 选择音色。

## 卸载

```bash
make uninstall
```

卸载会停止 launchd 服务并删除二进制/CLI/plist，但保留：

```text
~/.config/iSpeak/
```

这样 API Key、音色配置和 hook 状态不会被误删。
