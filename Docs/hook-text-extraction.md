# Hook 文本提取链路

`hook-speak.sh` 只做一件事：从 Hook JSON 里取 assistant 回复文本，发给 iSpeak socket。当前 51 行。

## 提取逻辑

```js
// Codex 遗留 notify（agent-turn-complete）与现代 Stop Hook 重复触发，跳过
if (payload.type === "agent-turn-complete") return;

const text = payload.last_assistant_message     // Claude Stop / Codex Stop (snake_case)
  || payload["last-assistant-message"]            // Codex notify (kebab-case)
  || "";
```

不再需要 transcript 轮询、去重、状态文件、`payload.message` fallback。

## 输入来源

| 来源 | 传参方式 | 字段名 | 处理 |
|------|---------|--------|------|
| Claude Code Stop Hook | stdin | `last_assistant_message` | 提取并播报 |
| Codex Stop Hook | stdin | `last_assistant_message` | 提取并播报 |
| Codex 遗留 notify | `$2` (argv) | `last-assistant-message` | 跳过（`agent-turn-complete`） |

脚本统一处理 stdin 和 argv：

```bash
input="${2:-}"          # 遗留 notify 走 $2
if [[ -z "$input" ]]; then
  input=$(cat)          # Stop Hook 走 stdin
fi
```

## Codex Stop Hook（现代）

stdin JSON：

```json
{
  "turn_id": "...",
  "transcript_path": "...",
  "last_assistant_message": "最后一条 assistant 回复"
}
```

源码：`codex-rs/hooks/src/events/stop.rs` — `StopCommandInput` struct 包含 `last_assistant_message`。

## Codex 遗留 notify（跳过）

Codex 有两套通知机制同时触发：

| 机制 | 事件 | 触发时机 |
|------|------|---------|
| 现代 Stop Hook | `stop` | agent 回合结束 |
| 遗留 notify | `agent-turn-complete` | agent 回合结束 |

两套系统都包含 `last_assistant_message`，导致重复播报。现代 Stop Hook 已覆盖需求，遗留 notify 通过 `payload.type === "agent-turn-complete"` 跳过。

源码：`codex-rs/hooks/src/legacy_notify.rs` — 向后兼容，JSON 通过 `command.arg()` 传入，字段序列化为 kebab-case。

## 触发时间点

Hook 在 AI **回复完成**时触发，每个回合一次。Claude Code 和 Codex 均使用 `Stop` 事件：

```
用户发送消息 → AI 生成回复 → 回复结束 → Hook 触发 → 提取文本 → 发送 socket → TTS 播报
```

从 Hook 触发到 TTS 首字延迟通常 < 500ms（取决于文本长度和网络）。

## 来源 & 音色

Hook 调用时传入来源名称（`$1`），对应 `config.json` 中的音色映射：

```bash
# ~/.claude/settings.json — Claude Code
"command": "bash ~/.config/iSpeak/hook-speak.sh claude"

# ~/.codex/hooks.json — Codex
"command": "bash /Users/admin/.config/iSpeak/hook-speak.sh codex"
```

文本加上 `{source:claude}` 或 `{source:codex}` 前缀发往 socket，`ispeakd` 解析后选择对应音色。无前缀则用 `defaultVoice`。

音色映射示例（`~/.config/iSpeak/config.json`）：

```json
{
  "defaultVoice": { "voice_type": "zh_female_mizai_uranus_bigtts" },
  "sourceVoices": {
    "claude": { "voice_type": "zh_female_tianmeitaozi_uranus_bigtts" },
    "codex": { "voice_type": "zh_female_shuangkuaisisi_uranus_bigtts" }
  }
}
```

日志中也会区分来源：

```
TTS [claude]: 飞哥好。           → tianmeitaozi 音色
TTS [codex]: 飞哥，你好。        → shuangkuaisisi 音色
TTS [default]: 直接文本          → mizai 音色
```

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

Claude Code 官方文档只列出 `transcript_path`，但实际 payload **包含 `last_assistant_message`**（实测确认）。直接用 direct 字段，无需读 transcript。

## 历史演进

- v1（250 行）：transcript 轮询 + turn_id 去重 + state file + text hash。复杂度高，`session_id` 做去重 key 导致同一 session 只播第一条。
- v2（53 行）：省略去重和 transcript 轮询，但 `payload.message` 回退太宽泛，且 Codex 重复触发未处理。
- v3（51 行）：统一提取，移除 `payload.message`，过滤 `agent-turn-complete` 解决 Codex 双重通知导致的重复播报。
