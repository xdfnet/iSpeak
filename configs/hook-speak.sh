#!/bin/bash
# Claude Code / Codex 共用播报 Hook：
# 只取最后一条 assistant 回复，加 `{source:<name>}` 前缀后发给 ispeakd。
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

SOURCE="${1:-claude}"
SOCK="$HOME/.config/iSpeak/ispeak.sock"
LOG="$HOME/.config/iSpeak/hook.log"
STATE_FILE="$HOME/.config/iSpeak/hook.last"

# Codex `notify` 会把 JSON 作为最后一个参数传入；
# Claude/Claude 风格 Stop Hook 会把 JSON 写到 stdin。
input="${2:-}"
if [[ -z "$input" ]]; then
  input=$(cat)
fi
input_file=$(mktemp)
trap 'rm -f "$input_file"' EXIT
printf "%s" "$input" > "$input_file"

result=$(SOURCE="$SOURCE" HOOK_INPUT_FILE="$input_file" HOOK_STATE_FILE="$STATE_FILE" node <<'NODE' 2>/dev/null
const fs = require("fs");
const crypto = require("crypto");

(() => {
  const input = readFile(process.env.HOOK_INPUT_FILE || "");
  const payload = parseJSON(input) || {};
  const source = process.env.SOURCE || "";
  const stateFile = process.env.HOOK_STATE_FILE || "";
  const result = source.startsWith("codex")
    ? lastCodexAssistant(payload)
    : lastClaudeAssistant(payload);

  if (!result.text) {
    return;
  }

  if (stateFile && result.turnId) {
    if (isDuplicateTurn(stateFile, source, result.turnId)) {
      return;
    }
    saveTurn(stateFile, source, result.turnId, result.text);
  } else if (stateFile) {
    saveTurn(stateFile, source, "", result.text);
  }

  process.stdout.write(result.text);
})();

function lastClaudeAssistant(payload) {
  const direct = firstString(payload.last_assistant_message, payload.message);
  if (direct) return { text: direct, turnId: extractTurnId(payload) };

  const transcript = firstString(payload.transcript_path, payload.transcriptPath);
  return transcript ? lastClaudeTranscript(transcript, payload) : { text: "", turnId: extractTurnId(payload) };
}

function lastCodexAssistant(payload) {
  const direct = firstString(
    payload["last-assistant-message"],
    payload.last_assistant_message,
    payload.lastAssistantMessage,
    payload.message,
    payload.lastMessage
  );
  if (direct) return { text: direct, turnId: extractTurnId(payload) };

  const transcript = firstString(
    payload.transcript_path,
    payload.transcriptPath,
    payload["transcript-path"]
  );
  return transcript ? lastAssistantFromTranscript(transcript, "codex") : { text: "", turnId: extractTurnId(payload) };
}

function readFile(file) {
  try {
    return fs.readFileSync(file, "utf8");
  } catch {
    return "";
  }
}

function parseJSON(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function firstString(...values) {
  for (const value of values) {
    if (typeof value === "string" && value !== "") return value;
  }
  return "";
}

function collectText(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map(item => collectText(item))
      .filter(Boolean)
      .join(" ");
  }
  if (!content || typeof content !== "object") return "";
  if (typeof content.text === "string") return content.text;
  if (content.content) return collectText(content.content);
  return "";
}

function lastClaudeTranscript(file, payload) {
  const deadline = Date.now() + 5000;
  let result = { text: "", turnId: extractTurnId(payload) };

  while (Date.now() <= deadline) {
    result = lastAssistantFromTranscript(file, "claude");
    if (result.text) return result;
    sleepMs(120);
  }

  return result;
}

function lastAssistantFromTranscript(file, source) {
  let data = "";
  try {
    data = fs.readFileSync(file, "utf8");
  } catch {
    return "";
  }

  let last = "";
  let turnId = "";
  for (const line of data.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const event = parseJSON(line);
    if (!event) continue;

    if (source === "claude") {
      if (event.role === "assistant") {
        last = collectText(event.content) || last;
      }
      if (event.message && event.message.role === "assistant") {
        last = collectText(event.message.content) || last;
      }
    }

    if (source === "codex" &&
      event.type === "response_item" &&
      event.payload &&
      event.payload.type === "message" &&
      event.payload.role === "assistant"
    ) {
      last = collectText(event.payload.content) || last;
      turnId = turnId || extractTurnId(event) || extractTurnId(event.payload);
    }
  }
  return { text: last, turnId };
}

function extractTurnId(payload) {
  return firstString(
    payload.turn_id,
    payload.turnId,
    payload["turn-id"]
  );
}

function isDuplicateTurn(stateFile, source, turnId) {
  const current = `${source}:${turnId}`;
  try {
    return fs.readFileSync(stateFile, "utf8").trim() === current;
  } catch {
    return false;
  }
}

function saveTurn(stateFile, source, turnId, text) {
  const current = `${source}:${turnId || textHash(text)}`;
  try {
    fs.mkdirSync(require("path").dirname(stateFile), { recursive: true });
    fs.writeFileSync(stateFile, current, "utf8");
  } catch {
    // 去重失败不影响播报。
  }
}

function textHash(text) {
  return crypto.createHash("sha1").update(text, "utf8").digest("hex");
}

function sleepMs(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

NODE
)

if [[ "$ISPEAK_HOOK_PRINT_TEXT" == "1" ]]; then
  printf "%s" "$result"
  exit 0
fi

echo "=== $(date) ===" >> "$LOG"
echo "SOURCE: $SOURCE" >> "$LOG"
echo "TEXT_LEN: ${#result}" >> "$LOG"
echo "PREVIEW: ${result:0:150}" >> "$LOG"

# Claude Code Stop Hook 调试
if [[ "$SOURCE" == "claude" && -n "$input" ]]; then
  # 用 grep 提取 transcript_path
  tp=$(echo "$input" | grep -o '"transcript_path":"[^"]*"' | head -1 | sed 's/"transcript_path":"//;s/"$//')
  if [[ -n "$tp" ]]; then
    echo "CLAUDE_TRANSCRIPT_PATH: $tp" >> "$LOG"
    if [[ -f "$tp" ]]; then
      echo "CLAUDE_TRANSCRIPT_EXISTS: yes" >> "$LOG"
    else
      echo "CLAUDE_TRANSCRIPT_EXISTS: no" >> "$LOG"
    fi
  else
    echo "CLAUDE_TRANSCRIPT_PATH: none" >> "$LOG"
    echo "CLAUDE_RAW: ${input:0:300}" >> "$LOG"
  fi
fi

if [[ -n "$result" && -S "$SOCK" ]]; then
  printf "{source:%s}%s" "$SOURCE" "$result" | nc -U -w5 "$SOCK" 2>> "$LOG"
  echo "SPOKE: OK" >> "$LOG"
else
  echo "SPOKE: SKIP" >> "$LOG"
fi
