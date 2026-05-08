#!/bin/bash
# Stop Hook: 只播报本次停止时的最后一条 assistant 回复
# iAgent 调用 Claude 时设 ISPEAK_SKIP=1，此时跳过（iAgent 自己播）
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

# 来源参数: claude 或 codex
SOURCE="${1:-claude}"

SOCK="$HOME/.config/iSpeak/ispeak.sock"
LOG="$HOME/.config/iSpeak/hook.log"

input=$(cat)

json_value() {
  local key="$1"
  if command -v node >/dev/null 2>&1; then
    printf "%s" "$input" | node -e '
      const key = process.argv[1];
      let input = "";
      process.stdin.setEncoding("utf8");
      process.stdin.on("data", chunk => input += chunk);
      process.stdin.on("end", () => {
        try {
          const value = JSON.parse(input)[key];
          if (typeof value === "string") process.stdout.write(value);
        } catch (_) {}
      });
    ' "$key"
    return
  fi

  printf "%s" "$input" | sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

extract_last_assistant_text() {
  local transcript="$1"

  if command -v node >/dev/null 2>&1; then
    node -e '
      const fs = require("fs");
      const file = process.argv[1];
      let last = "";

      function collectText(content) {
        const out = [];
        if (typeof content === "string") {
          return content;
        }
        if (!Array.isArray(content)) return "";
        for (const item of content) {
          if (item && typeof item.text === "string") out.push(item.text);
        }
        return out.join(" ");
      }

      for (const line of fs.readFileSync(file, "utf8").split(/\r?\n/)) {
        if (!line.trim()) continue;
        try {
          const event = JSON.parse(line);
          if (event.role === "assistant") last = collectText(event.content) || last;
          if (event.message && event.message.role === "assistant") last = collectText(event.message.content) || last;
        } catch (_) {}
      }
      process.stdout.write(last);
    ' "$transcript" 2>/dev/null
    return
  fi

  awk '
    {
      if (match($0, /"role"[[:space:]]*:[[:space:]]*"assistant"/)) {
        if (match($0, /"content"[[:space:]]*:[[:space:]]*\[/)) {
          gsub(/[^{]*\[/, "", $0)
          gsub(/\].*/, "", $0)
          msg = ""
          while (match($0, /"text"[[:space:]]*:[[:space:]]*"[^"]*"/)) {
            t = substr($0, RSTART, RLENGTH)
            gsub(/"text"[[:space:]]*:[[:space:]]*"/, "", t)
            gsub(/"$/, "", t)
            if (t != "") msg = msg " " t
            $0 = substr($0, RSTART + RLENGTH)
          }
          if (msg != "") last = msg
        } else if (match($0, /"content"[[:space:]]*:[[:space:]]*"[^"]*"/)) {
          t = substr($0, RSTART, RLENGTH)
          gsub(/"content"[[:space:]]*:[[:space:]]*"/, "", t)
          gsub(/"$/, "", t)
          if (t != "") last = t
        }
      }
    }
    END {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", last)
      if (last != "") print last
    }
  ' "$transcript" 2>/dev/null
}

# 从 stdin JSON 提取最后一条消息；没有时再从 transcript 取最后一条 assistant
transcript=$(json_value "transcript_path")
last_msg=$(json_value "last_assistant_message")

all_text="$last_msg"

# Claude Code 部分版本不提供 last_assistant_message，此时只取 transcript 最后一条 assistant。
if [[ -z "$all_text" && -n "$transcript" && -f "$transcript" ]]; then
  extra=$(extract_last_assistant_text "$transcript")
  if [[ -n "$extra" ]]; then
    all_text="$extra"
  fi
fi

echo "=== $(date) ===" >> "$LOG"
echo "SOURCE: $SOURCE" >> "$LOG"
echo "TEXT_LEN: ${#all_text}" >> "$LOG"
echo "PREVIEW: ${all_text:0:150}" >> "$LOG"

if [[ -n "$all_text" && -S "$SOCK" ]]; then
  printf "{source:%s}%s" "$SOURCE" "$all_text" | nc -U -w5 "$SOCK" 2>> "$LOG"
  echo "SPOKE: OK" >> "$LOG"
else
  echo "SPOKE: SKIP" >> "$LOG"
fi
