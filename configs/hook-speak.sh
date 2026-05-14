#!/bin/bash
# Claude Code / Codex / Copilot CLI 共用播报 Hook（Pi 走 ispeak.ts Extension）
# 取最后一条 assistant 消息，加 {source:<name>} 前缀后发给 ispeakd。
# Claude:  payload.last_assistant_message (snake_case)
# Codex:   payload["last-assistant-message"] (kebab-case)
# Copilot: payload.transcriptPath → 读取 JSONL，取最后 assistant.message.content
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

  // Codex Stop hook 会在 agent-turn-complete 事件中重复触发，跳过
  if (payload.type === "agent-turn-complete") return;

  // Claude / Codex: 直接从 payload 取 last_assistant_message
  const text = payload.last_assistant_message
    || payload["last-assistant-message"]
    || "";

  if (text) { process.stdout.write(text); return; }

  // Copilot CLI agentStop: 从 transcriptPath 读取最后 assistant.message
  const transcriptPath = payload.transcriptPath || payload.transcript_path;
  if (transcriptPath) {
    const lastText = source === "copilot"
      ? waitForFreshTranscriptText(transcriptPath)
      : extractFromTranscript(transcriptPath);
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
function extractFromTranscript(path) {
  try {
    const lines = fs.readFileSync(path, "utf8").split("\n");
    let lastText = "";
    for (const line of lines) {
      if (!line.trim()) continue;
      const event = parseJSON(line);
      if (!event || event.type !== "assistant.message") continue;
      const data = event.data || {};
      const content = contentText(data.content || "");
      if (content) lastText = content;
    }
    return lastText;
  } catch { return ""; }
}
function waitForFreshTranscriptText(path) {
  const stateFile = process.env.HOOK_STATE_FILE || "";
  const previous = readFile(stateFile).trim();
  const deadline = Date.now() + Number(process.env.ISPEAK_COPILOT_TRANSCRIPT_WAIT_MS || 4000);
  let lastText = "";

  while (Date.now() <= deadline) {
    lastText = extractFromTranscript(path);
    if (lastText && textHash(lastText) !== previous) {
      saveState(stateFile, lastText);
      return lastText;
    }
    sleepMs(120);
  }

  return "";
}
function saveState(file, text) {
  if (!file) return;
  try {
    fs.mkdirSync(require("path").dirname(file), { recursive: true });
    fs.writeFileSync(file, textHash(text), "utf8");
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
