#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOOK="$ROOT/configs/hook-speak.sh"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

assert_stdin_extracts() {
  local name="$1"
  local source="$2"
  local input="$3"
  local want="$4"
  local got

  got=$(printf "%s" "$input" | ISPEAK_HOOK_STATE_FILE="$TMPDIR/hook-test.last" ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" "$source")
  if [[ "$got" != "$want" ]]; then
    echo "hook fixture failed: $name" >&2
    echo "want: $want" >&2
    echo "got:  $got" >&2
    exit 1
  fi
}

assert_arg_extracts() {
  local name="$1"
  local source="$2"
  local input="$3"
  local want="$4"
  local got

  got=$(ISPEAK_HOOK_STATE_FILE="$TMPDIR/hook-test.last" ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" "$source" "$input")
  if [[ "$got" != "$want" ]]; then
    echo "hook fixture failed: $name" >&2
    echo "want: $want" >&2
    echo "got:  $got" >&2
    exit 1
  fi
}

assert_stdin_extracts "claude direct field" claude \
  '{"last_assistant_message":"Claude 直接字段"}' \
  "Claude 直接字段"

assert_arg_extracts "codex notify argv field" codex \
  '{"turn-id":"turn-123","last-assistant-message":"Codex 横杠字段"}' \
  "Codex 横杠字段"

assert_stdin_extracts "codex stop stdin field" codex \
  '{"turn_id":"turn-456","last_assistant_message":"Codex Stop 字段"}' \
  "Codex Stop 字段"

# Copilot CLI agentStop: 通过 transcriptPath 读取 JSONL
cat > "$TMPDIR/events.jsonl" <<'JSONL'
{"type":"session.start","data":{},"id":"1","timestamp":"2026-05-14T00:00:00Z"}
{"type":"user.message","data":{"content":"你好"},"id":"2","timestamp":"2026-05-14T00:00:01Z","parentId":"1"}
{"type":"assistant.message","data":{"content":"Copilot 的回复","toolRequests":[]},"id":"3","timestamp":"2026-05-14T00:00:02Z","parentId":"2"}
{"type":"assistant.turn_end","data":{},"id":"4","timestamp":"2026-05-14T00:00:02Z","parentId":"3"}
JSONL

assert_stdin_extracts "copilot agentStop transcript" copilot \
  "{\"sessionId\":\"s1\",\"transcriptPath\":\"$TMPDIR/events.jsonl\",\"stopReason\":\"end_turn\"}" \
  "Copilot 的回复"

old_hash=$(printf "%s" "上一条 Copilot 回复" | shasum | awk '{print $1}')
printf "%s" "$old_hash" > "$TMPDIR/hook.last"
cat > "$TMPDIR/delayed-events.jsonl" <<'JSONL'
{"type":"assistant.message","data":{"content":"上一条 Copilot 回复"},"id":"old","timestamp":"2026-05-14T00:00:01Z"}
JSONL
(
  sleep 0.3
  printf '%s\n' '{"type":"assistant.message","data":{"content":"当前 Copilot 回复"},"id":"new","timestamp":"2026-05-14T00:00:02Z"}' >> "$TMPDIR/delayed-events.jsonl"
) &
delayed_pid=$!
got=$(printf '{"sessionId":"s2","transcriptPath":"%s/delayed-events.jsonl","stopReason":"end_turn"}' "$TMPDIR" |
  ISPEAK_HOOK_STATE_FILE="$TMPDIR/hook.last" ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" copilot)
wait "$delayed_pid"
if [[ "$got" != "当前 Copilot 回复" ]]; then
  echo "hook fixture failed: copilot waits for fresh transcript append" >&2
  echo "want: 当前 Copilot 回复" >&2
  echo "got:  $got" >&2
  exit 1
fi

assert_cli_payload() {
  local name="$1"
  local command="$2"
  local input="$3"
  local want="$4"
  local got

  got=$(ISPEAK_CLI_PRINT_PAYLOAD=1 "$ROOT/scripts/$command" "$input")
  if [[ "$got" != "$want" ]]; then
    echo "cli source fixture failed: $name" >&2
    echo "want: $want" >&2
    echo "got:  $got" >&2
    exit 1
  fi
}

assert_cli_payload "plain ispeak keeps default source" ispeak \
  "默认入口" \
  "默认入口"

assert_cli_payload "claude source command" ispeak-claude \
  "Claude 入口" \
  "{source:claude}Claude 入口"

assert_cli_payload "codex source command" ispeak-codex \
  "Codex 入口" \
  "{source:codex}Codex 入口"

assert_cli_payload "copilot source command" ispeak-copilot \
  "Copilot 入口" \
  "{source:copilot}Copilot 入口"

assert_cli_payload "pi source command" ispeak-pi \
  "Pi 入口" \
  "{source:pi}Pi 入口"

echo "hook fixtures passed"
