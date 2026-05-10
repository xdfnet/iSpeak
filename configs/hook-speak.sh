#!/bin/bash
# Claude Code / Codex 共用播报 Hook：
# 只取最后一条 assistant 回复，加 `{source:<name>}` 前缀后发给 ispeakd。
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

SOURCE="${1:-claude}"
SOCK="$HOME/.config/iSpeak/ispeak.sock"
LOG="$HOME/.config/iSpeak/hook.log"

# Codex `notify` 会把 JSON 作为最后一个参数传入；
# Claude/Claude 风格 Stop Hook 会把 JSON 写到 stdin。
input="${2:-}"
if [[ -z "$input" ]]; then
  input=$(cat)
fi
input_file=$(mktemp)
trap 'rm -f "$input_file"' EXIT
printf "%s" "$input" > "$input_file"

text=$(SOURCE="$SOURCE" HOOK_INPUT_FILE="$input_file" node <<'NODE' 2>/dev/null
const fs = require("fs");

{
  const input = readFile(process.env.HOOK_INPUT_FILE || "");
  const payload = parseJSON(input) || {};
  const source = process.env.SOURCE || "";
  const text = source.startsWith("codex")
    ? lastCodexAssistant(payload)
    : lastClaudeAssistant(payload);

  if (text) process.stdout.write(text);
}

function lastClaudeAssistant(payload) {
  const direct = firstString(payload.last_assistant_message, payload.message);
  if (direct) return direct;

  const transcript = firstString(payload.transcript_path, payload.transcriptPath);
  return transcript ? lastAssistantFromTranscript(transcript, "claude") : "";
}

function lastCodexAssistant(payload) {
  const direct = firstString(
    payload["last-assistant-message"],
    payload.last_assistant_message,
    payload.lastAssistantMessage,
    payload.message,
    payload.lastMessage
  );
  if (direct) return direct;

  const transcript = firstString(
    payload.transcript_path,
    payload.transcriptPath,
    payload["transcript-path"]
  );
  return transcript ? lastAssistantFromTranscript(transcript, "codex") : "";
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
  if (!Array.isArray(content)) return "";
  return content
    .map(item => item && typeof item.text === "string" ? item.text : "")
    .filter(Boolean)
    .join(" ");
}

function lastAssistantFromTranscript(file, source) {
  let data = "";
  try {
    data = fs.readFileSync(file, "utf8");
  } catch {
    return "";
  }

  let last = "";
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
    }
  }
  return last;
}

NODE
)

if [[ "$ISPEAK_HOOK_PRINT_TEXT" == "1" ]]; then
  printf "%s" "$text"
  exit 0
fi

echo "=== $(date) ===" >> "$LOG"
echo "SOURCE: $SOURCE" >> "$LOG"
echo "TEXT_LEN: ${#text}" >> "$LOG"
echo "PREVIEW: ${text:0:150}" >> "$LOG"

if [[ -n "$text" && -S "$SOCK" ]]; then
  printf "{source:%s}%s" "$SOURCE" "$text" | nc -U -w5 "$SOCK" 2>> "$LOG"
  echo "SPOKE: OK" >> "$LOG"
else
  echo "SPOKE: SKIP" >> "$LOG"
fi
