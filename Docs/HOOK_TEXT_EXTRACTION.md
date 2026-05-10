# Hook 文本提取链路

本文记录 Claude Code / Codex CLI 在 Hook 中拿到“最后一条 assistant 回复”的实际方式。`hook-speak.sh` 的目标只做两件事：取最后一条 assistant 回复，发给 iSpeak socket。

## 结论

推荐优先级：

1. **Codex `notify`**：从脚本第二个参数 `$2` 读取 JSON，取 `last-assistant-message`。
2. **Claude / Codex Stop Hook**：从 stdin 读取 JSON，优先取 `last_assistant_message`。
3. **明确 transcript**：如果没有直接字段，只读取 payload 里明确传入的 `transcript_path`。

不扫描 `~/.codex/sessions`。没有 direct 字段也没有 `transcript_path` 时，本次不播报。

## Codex CLI：notify

当前本机版本：

```text
codex-cli 0.130.0
```

Codex CLI 的 `notify = [...]` 是 legacy notify 机制。官方源码里会把通知 JSON 追加成命令的最后一个 argv 参数，不写 stdin。

配置示例：

```toml
notify = ["/Users/你的用户名/.config/iSpeak/hook-speak.sh", "codex"]
```

脚本实际收到：

```bash
$1 = "codex"
$2 = '{"type":"agent-turn-complete",...,"last-assistant-message":"..."}'
stdin = empty
```

核心字段：

```json
{
  "type": "agent-turn-complete",
  "thread-id": "...",
  "turn-id": "...",
  "cwd": "...",
  "input-messages": ["..."],
  "last-assistant-message": "最后一条 assistant 回复"
}
```

所以 Codex `notify` 的正确读取方式是：

```bash
input="${2:-}"
```

然后解析：

```js
payload["last-assistant-message"]
```

源码依据：`codex-rs/hooks/src/legacy_notify.rs`。该文件把 `last_assistant_message` 序列化为 kebab-case 的 `last-assistant-message`，并在执行命令前 `command.arg(notify_payload)`。

## Codex CLI：Stop Hook

Codex 也支持 Claude 风格 Hook。Stop Hook 的输入 JSON 写入 stdin。

配置示例：

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "bash $HOME/.config/iSpeak/hook-speak.sh codex",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

脚本实际收到：

```bash
$1 = "codex"
$2 = empty
stdin = '{"hook_event_name":"Stop",...,"last_assistant_message":"..."}'
```

核心字段：

```json
{
  "session_id": "...",
  "turn_id": "...",
  "transcript_path": "...",
  "cwd": "...",
  "hook_event_name": "Stop",
  "model": "...",
  "permission_mode": "bypassPermissions",
  "stop_hook_active": false,
  "last_assistant_message": "最后一条 assistant 回复"
}
```

源码依据：

- `codex-rs/hooks/src/events/stop.rs`：构造 `StopCommandInput`，包含 `last_assistant_message` 和 `transcript_path`。
- `codex-rs/hooks/schema/generated/stop.command.input.schema.json`：Stop stdin schema。
- `codex-rs/hooks/src/engine/command_runner.rs`：Hook 命令通过 stdin 接收 `input_json`。

## Codex Transcript

Codex 的 transcript/session 文件是 JSONL。实际 assistant 回复形态：

```json
{
  "type": "response_item",
  "payload": {
    "type": "message",
    "role": "assistant",
    "content": [
      {
        "type": "output_text",
        "text": "最后一条 assistant 回复"
      }
    ]
  }
}
```

提取规则：

```js
event.type === "response_item" &&
event.payload?.type === "message" &&
event.payload?.role === "assistant"
```

然后拼接：

```js
event.payload.content[].text
```

## Claude Code：Stop Hook

Claude Code 官方 Stop Hook 通过 stdin 传 JSON，核心字段是：

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "hook_event_name": "Stop",
  "stop_hook_active": false
}
```

有些版本或场景可能直接提供：

```json
{
  "last_assistant_message": "最后一条 assistant 回复"
}
```

所以 Claude 的读取顺序是：

1. `last_assistant_message`
2. `message`
3. `transcript_path`

Claude transcript 常见 assistant 形态：

```json
{"role":"assistant","content":[{"type":"text","text":"..."}]}
```

或：

```json
{"message":{"role":"assistant","content":[{"type":"text","text":"..."}]}}
```

## 当前脚本策略

`configs/hook-speak.sh` 当前入口：

```bash
input="${2:-}"
if [[ -z "$input" ]]; then
  input=$(cat)
fi
```

含义：

- Codex `notify`：读 `$2`
- Claude / Codex Stop Hook：读 stdin

Codex 文本字段优先级：

```js
payload["last-assistant-message"]
payload.last_assistant_message
payload.lastAssistantMessage
payload.message
payload.lastMessage
payload.transcript_path
payload.transcriptPath
payload["transcript-path"]
```

Claude 文本字段优先级：

```js
payload.last_assistant_message
payload.message
payload.transcript_path
payload.transcriptPath
```

## 为什么不能只读 stdin

因为 Codex `notify` 不走 stdin。只读 stdin 会导致：

```text
TEXT_LEN: 0
SPOKE: SKIP
```

正确做法是先读 `$2`，再读 stdin；不扫历史 session。
