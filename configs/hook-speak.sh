#!/bin/bash
# Claude Code / Codex 共用播报 Hook：
# 取 last_assistant_message，加 {source:<name>} 前缀后发给 ispeakd。
# Claude: payload.last_assistant_message (snake_case)
# Codex:  payload["last-assistant-message"] (kebab-case)
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

SOURCE="${1:-claude}"
SOCK="$HOME/.config/iSpeak/ispeak.sock"
LOG="$HOME/.config/iSpeak/hook.log"

input="${2:-}"
if [[ -z "$input" ]]; then
  input=$(cat)
fi
input_file=$(mktemp)
trap 'rm -f "$input_file"' EXIT
printf "%s" "$input" > "$input_file"

result=$(HOOK_INPUT_FILE="$input_file" node <<'NODE' 2>>"$LOG"
const fs = require("fs");

(() => {
  const input = readFile(process.env.HOOK_INPUT_FILE || "");
  const payload = parseJSON(input) || {};

  const text = payload.last_assistant_message
    || payload["last-assistant-message"]
    || "";

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
