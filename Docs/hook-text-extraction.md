# Hook 文本提取链路

本文记录 Claude Code / Codex CLI 在 Hook 中拿到“最后一条 assistant 回复”的实际方式。`hook-speak.sh` 的目标只做两件事：取最后一条 assistant 回复，发给 iSpeak socket。

## 结论

推荐优先级：

1. **Codex `notify`**：从脚本第二个参数 `$2` 读取 JSON，取 `last-assistant-message`（kebab-case）。
2. **Codex Stop Hook**：从 stdin 读取 JSON，取 `last_assistant_message`（snake_case）。
3. **Claude Code Stop Hook**：从 stdin 读取 JSON，只读 `transcript_path`（官方无 direct 字段）。

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

源码依据：`codex-rs/hooks/src/legacy_notify.rs`（https://github.com/openai/codex，2026-05-11）。该文件把 `last_assistant_message` 序列化为 kebab-case 的 `last-assistant-message`，并在执行命令前 `command.arg(notify_payload)`。

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

核心字段（源码 `StopCommandInput` struct）：

```rust
struct StopCommandInput {
    session_id: String,
    turn_id: String,
    transcript_path: NullableString,
    cwd: String,
    hook_event_name: String,
    model: String,
    permission_mode: String,
    stop_hook_active: bool,
    last_assistant_message: NullableString,  // ← Codex 有此字段
}
```

对应 JSON：

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

- `codex-rs/hooks/src/events/stop.rs`（https://github.com/openai/codex，2026-05-11）：构造 `StopCommandInput`，包含 `last_assistant_message` 和 `transcript_path`。
- `codex-rs/hooks/src/engine/command_runner.rs`（同上）：Hook 命令通过 stdin 接收 `input_json`。

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

> **来源**：[Claude Code Hooks Reference](https://code.claude.com/docs/en/hooks.md)，更新时间：2026-05-11

Claude Code 官方 Stop Hook **没有 `last_assistant_message` 字段**。

根据官方文档，Stop Hook 的 Common Input Fields 为：

```json
{
  "session_id": "abc123",
  "transcript_path": "/home/user/.claude/projects/.../transcript.jsonl",
  "cwd": "/home/user/my-project",
  "permission_mode": "default",
  "hook_event_name": "Stop",
  "effort": {
    "level": "medium"
  }
}
```

子 agent 上下文中额外字段：

```json
{
  "agent_id": "subagent_xyz",
  "agent_type": "Explore"
}
```

**结论**：Claude Code Stop Hook 官方设计只提供 `transcript_path`，没有直接内嵌 `last_assistant_message`。旧版本脚本的 `last_assistant_message` / `message` fallback 实际上**从未被官方文档支持**。

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
- 如果 Codex 的 `notify` 和 `Stop` 同时启用，脚本会按 `turn_id` 去重，避免同一回合播两次

Codex 文本字段优先级（源码确认）：

```js
payload["last-assistant-message"]  // notify: kebab-case
payload.last_assistant_message      // Stop Hook: snake_case
payload.lastAssistantMessage
payload.message
payload.lastMessage
payload.transcript_path
payload.transcriptPath
payload["transcript-path"]
```

Claude Code 文本字段优先级（官方文档）：

```js
payload.transcript_path  // 官方支持的唯一方式
```

> **注**：Claude Code Stop Hook 官方 payload 中**没有 `last_assistant_message` 字段**，这是与 Codex 的本质区别。

## 为什么不能只读 stdin

因为 Codex `notify` 不走 stdin。只读 stdin 会导致：

```text
TEXT_LEN: 0
SPOKE: SKIP
```

正确做法是先读 `$2`，再读 stdin；不扫历史 session。

## Claude Code TEXT_LEN: 0 的根因

当 Claude Code Stop Hook 触发但 `TEXT_LEN: 0` 时：

1. **官方字段不存在**：Claude Code Stop Hook 官方 payload 中**没有 `last_assistant_message` 字段**，只有 `transcript_path`
2. **transcript 文件可能晚一点才写完**：Hook 触发时文件虽已存在，但最后一条 assistant 文本还没落盘
3. **结果**：如果只读一次，`hook-speak.sh` 可能拿到空串，本次不播报

当前脚本对 Claude transcript 做了很短的轮询，等最后一条 assistant 文本真正出现再播，避免这个时序窗。

这是 **Claude Code 与 Codex 的设计差异**，非 bug。Codex CLI（无论 notify 还是 Stop Hook）都提供 `last_assistant_message`，而 Claude Code 官方只提供 `transcript_path`。

解决方案：从 `transcript_path` 读取并解析为最终一条 assistant 回复，并在 Claude 路径上补一个短轮询。
