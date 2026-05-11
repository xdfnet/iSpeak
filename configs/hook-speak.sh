#!/bin/bash
# Claude Code / Codex 共用播报 Hook：
# 取 last_assistant_message，加 {source:<name>} 前缀后发给 ispeakd。
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

result=$(SOURCE="$SOURCE" HOOK_INPUT_FILE="$input_file" node <<'NODE' 2>/dev/null
const fs = require("fs");

(() => {
  const input = readFile(process.env.HOOK_INPUT_FILE || "");
  const payload = parseJSON(input) || {};
  const source = process.env.SOURCE || "";

  const text = source.startsWith("codex")
    ? (payload["last-assistant-message"] || payload.last_assistant_message || payload.lastAssistantMessage || payload.message || payload.lastMessage || "")
    : (payload.last_assistant_message || payload.message || "");

  if (text) process.stdout.write(text);
})();

function readFile(file) {
  try { return fs.readFileSync(file, "utf8"); } catch { return ""; }
}
function parseJSON(text) {
  try { return JSON.parse(text); } catch { return null; }
}
NODE
)

if [[ "$ISPEAK_HOOK_PRINT_TEXT" == "1" ]]; then
  printf "%s" "$result"
  exit 0
fi

if [[ -n "$result" && -S "$SOCK" ]]; then
  printf "{source:%s}%s" "$SOURCE" "$result" | nc -U -w5 "$SOCK" 2>> "$LOG"
else
  echo "$(date): SKIP source=$SOURCE text_len=${#result}" >> "$LOG"
fi
