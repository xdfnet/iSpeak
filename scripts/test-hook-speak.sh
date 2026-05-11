#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOOK="$ROOT/configs/hook-speak.sh"

assert_stdin_extracts() {
  local name="$1"
  local source="$2"
  local input="$3"
  local want="$4"
  local got

  got=$(printf "%s" "$input" | ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" "$source")
  if [[ "$got" != "$want" ]]; then
    echo "hook fixture failed: $name" >&2
    echo "want: $want" >&2
    echo "got:  $got" >&2
    exit 1
  fi
}

assert_stdin_dedupes() {
  local name="$1"
  local source="$2"
  local input="$3"
  local want_first="$4"
  local want_second="$5"
  local state_file
  local got_first
  local got_second

  state_file="$(mktemp)"

  got_first=$(printf "%s" "$input" | HOOK_STATE_FILE="$state_file" ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" "$source")
  got_second=$(printf "%s" "$input" | HOOK_STATE_FILE="$state_file" ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" "$source")

  if [[ "$got_first" != "$want_first" || "$got_second" != "$want_second" ]]; then
    echo "hook fixture failed: $name" >&2
    echo "want first:  $want_first" >&2
    echo "got first:   $got_first" >&2
    echo "want second: $want_second" >&2
    echo "got second:  $got_second" >&2
    exit 1
  fi

  rm -f "$state_file"
}

assert_arg_extracts() {
  local name="$1"
  local source="$2"
  local input="$3"
  local want="$4"
  local got

  got=$(ISPEAK_HOOK_PRINT_TEXT=1 bash "$HOOK" "$source" "$input")
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

transcript="$(mktemp)"
trap 'rm -f "$transcript"' EXIT
printf '%s\n' \
  '{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Codex transcript 字段"}]},"turn_id":"turn-789"}' \
  > "$transcript"

assert_stdin_extracts "codex transcript fallback" codex \
  "{\"transcript_path\":\"$transcript\"}" \
  "Codex transcript 字段"

assert_stdin_dedupes "codex duplicate turn" codex \
  '{"turn-id":"turn-999","last-assistant-message":"Codex 重复 turn"}' \
  "Codex 重复 turn" \
  ""

echo "hook fixtures passed"
