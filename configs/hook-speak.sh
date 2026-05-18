#!/bin/bash
# Claude Code / Codex / Copilot CLI 共用播报 Hook（Pi 走 ispeak.ts Extension）
# 取最后一条 assistant 消息，加 {source:<name>} 前缀后发给 ispeakd。
# Claude:  payload.last_assistant_message (snake_case)
# Codex:   payload.last_assistant_message (Stop)
# Copilot: payload.transcriptPath → 读取最新 user.message 之后的 assistant.message.content
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

SOURCE="${1:-claude}"
SOCK="$HOME/.config/iSpeak/ispeak.sock"
LOG="$HOME/.config/iSpeak/hook.log"
STATE_FILE="${ISPEAK_HOOK_STATE_FILE:-$HOME/.config/iSpeak/hook.last}"

input="${2:-}"
if [[ -z "$input" ]]; then
  input=$(cat)
fi
input_file=$(mktemp)
trap 'rm -f "$input_file"' EXIT
printf "%s" "$input" > "$input_file"

result=$(SOURCE="$SOURCE" HOOK_INPUT_FILE="$input_file" HOOK_STATE_FILE="$STATE_FILE" node <<'NODE' 2>>"$LOG"
const fs = require("fs");
const crypto = require("crypto");

(() => {
  const input = readFile(process.env.HOOK_INPUT_FILE || "");
  const payload = parseJSON(input) || {};
  const source = process.env.SOURCE || "";

  // Claude / Codex: 直接从官方 Stop payload 取 last_assistant_message
  const text = payload.last_assistant_message || "";

  if (text) {
    rememberSpoken(source, payload, text);
    process.stdout.write(text);
    return;
  }

  // Copilot CLI agentStop: 从 transcriptPath 读取最新 user.message 后的 assistant.message
  if (source === "copilot") {
    const transcriptPath = payload.transcriptPath || payload.transcript_path;
    const lastText = transcriptPath ? waitForFreshCopilotTranscriptText(transcriptPath) : "";
    if (lastText) process.stdout.write(lastText);
  }
})();

function readFile(file) {
  try { return fs.readFileSync(file, "utf8"); } catch { return ""; }
}
function parseJSON(text) {
  try { return JSON.parse(text); } catch { return null; }
}
function contentText(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content.map(contentText).filter(Boolean).join(" ");
  }
  if (!content || typeof content !== "object") return "";
  if (typeof content.text === "string") return content.text;
  if (content.content) return contentText(content.content);
  return "";
}
function extractLatestCopilotAssistant(path) {
  try {
    const lines = fs.readFileSync(path, "utf8").split("\n");
    const events = [];
    for (const line of lines) {
      if (!line.trim()) continue;
      const event = parseJSON(line);
      if (event) events.push(event);
    }

    let lastUserIndex = -1;
    for (let i = 0; i < events.length; i++) {
      if (events[i].type === "user.message") lastUserIndex = i;
    }

    let lastText = "";
    let lastId = "";
    for (let i = lastUserIndex + 1; i < events.length; i++) {
      const event = events[i];
      if (event.type !== "assistant.message") continue;
      const data = event.data || {};
      const content = contentText(data.content || "");
      if (content) {
        lastText = content;
        lastId = event.id || "";
      }
    }

    return { text: lastText, id: lastId };
  } catch { return { text: "", id: "" }; }
}
function waitForFreshCopilotTranscriptText(path) {
  const stateFile = process.env.HOOK_STATE_FILE || "";
  const previous = readFile(stateFile).trim();
  const deadline = Date.now() + Number(process.env.ISPEAK_COPILOT_TRANSCRIPT_WAIT_MS || 4000);

  while (Date.now() <= deadline) {
    const latest = extractLatestCopilotAssistant(path);
    const current = latest.id || textHash(latest.text);
    if (latest.text && current !== previous) {
      saveState(stateFile, current);
      return latest.text;
    }
    sleepMs(120);
  }

  return "";
}
function rememberSpoken(source, payload, text) {
  saveState(process.env.HOOK_STATE_FILE || "", stateKey(source, payload, text, ""));
}
function stateKey(source, payload, text, eventId) {
  const turnId = payload["turn-id"] || payload.turn_id || payload.turnId || "";
  return [source || "unknown", turnId || eventId || textHash(text)].join(":");
}
function saveState(file, value) {
  if (!file) return;
  try {
    fs.mkdirSync(require("path").dirname(file), { recursive: true });
    fs.writeFileSync(file, value, "utf8");
  } catch {}
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

if [[ -n "$result" && -S "$SOCK" ]]; then
  printf "{source:%s}%s" "$SOURCE" "$result" | nc -U -w5 "$SOCK" 2>> "$LOG"
fi
