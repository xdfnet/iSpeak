# Hook 文本提取链路

`configs/hook-speak.sh` 统一服务 Claude Code、Codex、Copilot CLI。它从 Hook JSON 中提取最后一条 assistant 回复，加上 `{source:<name>}` 前缀后发往 `~/.config/iSpeak/ispeak.sock`。Pi 不走这个脚本，使用 `configs/ispeak.ts` Extension 发送 `{source:pi}`。

## 输入来源

| 来源 | 事件 | 字段 | 处理 |
|------|------|------|------|
| Claude Code | `Stop` | `last_assistant_message` | 直接提取 |
| Codex | `Stop` | `last_assistant_message` | 直接提取 |
| Codex legacy notify | `agent-turn-complete` | `last-assistant-message` | 跳过，避免重复播报 |
| Copilot CLI | `agentStop` | `transcriptPath` / `transcript_path` | 读 JSONL，等待最新 `assistant.message` 落盘 |

脚本统一处理 stdin 和 argv：

```bash
input="${2:-}"          # 兼容 argv 传入 JSON 的旧模式
if [[ -z "$input" ]]; then
  input=$(cat)          # Stop / agentStop 通常走 stdin
fi
```

## 提取逻辑

```js
if (payload.type === "agent-turn-complete") return;

const text = payload.last_assistant_message
  || payload["last-assistant-message"]
  || "";

if (text) return text;

const transcriptPath = payload.transcriptPath || payload.transcript_path;
if (transcriptPath) {
  return source === "copilot"
    ? waitForFreshTranscriptText(transcriptPath)
    : extractFromTranscript(transcriptPath);
}
```

## Copilot 延迟落盘

Copilot CLI 的 `agentStop` 不直接提供 `last_assistant_message`，只提供 `transcriptPath`。实测中，`agentStop` 可能早于最新 `assistant.message` 写入 JSONL；如果 hook 立即读取，会读到上一条回复，表现为“只能听见倒数第二条”。

当前策略：

- 从 transcript 中读取最后一条 `assistant.message.data.content`
- 把已播文本的 SHA-1 hash 写入 `~/.config/iSpeak/hook.last`
- 下次 Copilot hook 触发时，如果最后一条文本 hash 仍等于 `hook.last`，说明 transcript 还没更新
- 每 120ms 轮询一次，最多等待 4 秒
- 读到新的 assistant 文本后更新 `hook.last` 并播报
- 超时仍没有新文本则跳过，避免重复播上一条

相关环境变量：

```bash
ISPEAK_HOOK_STATE_FILE=/tmp/hook.last          # 测试时覆盖状态文件
ISPEAK_COPILOT_TRANSCRIPT_WAIT_MS=4000        # 覆盖 Copilot 等待时长
ISPEAK_HOOK_PRINT_TEXT=1                      # 只打印提取文本，不发 socket
ISPEAK_SKIP=1                                 # 跳过 hook
```

## Hook 配置

### Claude Code

`~/.claude/settings.json`：

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

`~/.codex/hooks.json`：

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

Codex 首次加载新 Stop hook 时需要在 `/hooks` 中信任。

### Copilot CLI

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

改动 hook 配置后重启 Copilot CLI，让它重新加载配置。

## 来源与音色

Hook 调用时传入来源名称（`$1`），脚本发送：

```text
{source:claude}文本
{source:codex}文本
{source:copilot}文本
```

`ispeakd` 解析前缀后匹配 `config.json` 中的 `sourceVoices`。未配置对应来源时 fallback 到 `defaultVoice`。

配置示例：

```json
{
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

日志示例：

```text
TTS [claude]: 飞哥好。
TTS [codex]: 测试通过。
TTS [copilot]: 任务完成。
TTS [pi]: 已处理。
TTS [default]: 直接文本。
```

## 手动验证

只测试文本提取：

```bash
printf '{"last_assistant_message":"你好"}' \
  | ISPEAK_HOOK_PRINT_TEXT=1 bash ~/.config/iSpeak/hook-speak.sh claude
```

测试 Copilot transcript：

```bash
tmpdir=$(mktemp -d)
cat > "$tmpdir/events.jsonl" <<'JSONL'
{"type":"assistant.message","data":{"content":"Copilot 回复"}}
JSONL

printf '{"transcriptPath":"%s/events.jsonl"}' "$tmpdir" \
  | ISPEAK_HOOK_PRINT_TEXT=1 bash ~/.config/iSpeak/hook-speak.sh copilot
```

完整 fixture：

```bash
bash scripts/test-hook-speak.sh
```

## 历史演进

- v1：复杂 transcript 轮询 + turn/session 去重，曾因去重 key 过粗导致同一 session 只播第一条。
- v2：简化为 direct 字段提取，去掉宽泛 `payload.message` 回退，避免误播。
- v3：新增 Copilot `agentStop`，从 `transcriptPath` 读取 JSONL。
- v4：为 Copilot 增加 `hook.last` 文本 hash 和短轮询，避免只播倒数第二条回复。
