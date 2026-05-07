#!/bin/bash
# Stop Hook: 从 transcript 文件中提取本次会话所有 Claude 回复文本
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

extract_recent_assistant_text() {
  local transcript="$1"
  local cutoff="$2"

  if command -v node >/dev/null 2>&1; then
    node -e '
      const fs = require("fs");
      const file = process.argv[1];
      const cutoff = Number(process.argv[2]);
      const out = [];

      function collectText(content) {
        if (typeof content === "string") {
          out.push(content);
          return;
        }
        if (!Array.isArray(content)) return;
        for (const item of content) {
          if (item && typeof item.text === "string") out.push(item.text);
        }
      }

      for (const line of fs.readFileSync(file, "utf8").split(/\r?\n/)) {
        if (!line.trim()) continue;
        try {
          const event = JSON.parse(line);
          if (typeof event.timestamp === "number" && event.timestamp < cutoff) continue;
          if (event.role === "assistant") collectText(event.content);
          if (event.message && event.message.role === "assistant") collectText(event.message.content);
        } catch (_) {}
      }
      process.stdout.write([...new Set(out.filter(Boolean))].join(" "));
    ' "$transcript" "$cutoff" 2>/dev/null
    return
  fi

  awk -v cutoff="$cutoff" '
    {
      if (match($0, /"timestamp"[[:space:]]*:[[:space:]]*[0-9]+/)) {
        ts = substr($0, RSTART, RLENGTH)
        gsub(/[^0-9]/, "", ts)
        ts = int(ts)
        if (ts < cutoff) next
      }

      if (match($0, /"role"[[:space:]]*:[[:space:]]*"assistant"/)) {
        if (match($0, /"content"[[:space:]]*:[[:space:]]*\[/)) {
          gsub(/[^{]*\[/, "", $0)
          gsub(/\].*/, "", $0)
          while (match($0, /"text"[[:space:]]*:[[:space:]]*"[^"]*"/)) {
            t = substr($0, RSTART, RLENGTH)
            gsub(/"text"[[:space:]]*:[[:space:]]*"/, "", t)
            gsub(/"$/, "", t)
            if (t != "") print t
            $0 = substr($0, RSTART + RLENGTH)
          }
        } else if (match($0, /"content"[[:space:]]*:[[:space:]]*"[^"]*"/)) {
          t = substr($0, RSTART, RLENGTH)
          gsub(/"content"[[:space:]]*:[[:space:]]*"/, "", t)
          gsub(/"$/, "", t)
          if (t != "") print t
        }
      }
    }
  ' "$transcript" 2>/dev/null | sort -u | tr '\n' ' '
}

# 从 stdin JSON 提取 transcript 路径和最后一条消息
transcript=$(json_value "transcript_path")
last_msg=$(json_value "last_assistant_message")

all_text="$last_msg"

# 如果有 transcript 文件，提取最近 30 秒内的所有 assistant 消息
if [[ -n "$transcript" && -f "$transcript" ]]; then
  # 计算 30 秒前的时间戳（毫秒）
  cutoff=$(($(date +%s) * 1000 - 30000))

  # 优先用 JSON parser，Node 不存在时回退到简易 awk。
  extra=$(extract_recent_assistant_text "$transcript" "$cutoff")

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
