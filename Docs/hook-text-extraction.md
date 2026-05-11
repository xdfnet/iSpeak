# Hook 文本提取链路

`hook-speak.sh` 只做一件事：从 Hook JSON 里取 assistant 回复文本，发给 iSpeak socket。

## 结论

一行提取，覆盖所有来源：

```js
const text = payload.last_assistant_message     // Claude Stop / Codex Stop (snake_case)
  || payload["last-assistant-message"]            // Codex notify (kebab-case)
  || payload.message                              // fallback
  || "";
```

不再需要 transcript 轮询、去重、状态文件。

## 输入来源

| 来源 | 传参方式 | 字段名 |
|------|---------|--------|
| Claude Code Stop Hook | stdin | `last_assistant_message` |
| Codex Stop Hook | stdin | `last_assistant_message` |
| Codex notify | `$2` (argv) | `last-assistant-message` |

脚本统一处理：

```bash
input="${2:-}"          # Codex notify 走 $2
if [[ -z "$input" ]]; then
  input=$(cat)          # Claude / Codex Stop Hook 走 stdin
fi
```

## Codex notify

Codex `notify = [...]` 把 JSON 追加为命令最后一个 argv 参数。

配置：

```toml
notify = ["/Users/xxx/.config/iSpeak/hook-speak.sh", "codex"]
```

脚本收到：

```bash
$1 = "codex"
$2 = '{"type":"agent-turn-complete",...,"last-assistant-message":"..."}'
```

源码：`codex-rs/hooks/src/legacy_notify.rs` — `last_assistant_message` 序列化为 kebab-case `last-assistant-message`，通过 `command.arg(notify_payload)` 传入。

## Codex Stop Hook

stdin JSON：

```json
{
  "turn_id": "...",
  "transcript_path": "...",
  "last_assistant_message": "最后一条 assistant 回复"
}
```

源码：`codex-rs/hooks/src/events/stop.rs` — `StopCommandInput` struct 包含 `last_assistant_message`。

## Claude Code Stop Hook

stdin JSON（实测，2026-05）：

```json
{
  "session_id": "...",
  "transcript_path": "/Users/admin/.claude/projects/.../xxx.jsonl",
  "cwd": "...",
  "hook_event_name": "Stop",
  "last_assistant_message": "最后一条 assistant 回复"
}
```

Claude Code 官方文档只列出 `transcript_path`，但实际 payload **包含 `last_assistant_message`**（实测确认）。优先用 direct 字段，无需读 transcript。

## 历史演进

- v1（250 行）：transcript 轮询 + turn_id 去重 + state file + text hash。复杂度高，`session_id` 做去重 key 导致同一 session 只播第一条。
- v2（53 行）：省略去重和 transcript 轮询，但 Claude/Codex 分开写提取逻辑。
- v3（当前，51 行）：统一提取，一行覆盖所有来源。
