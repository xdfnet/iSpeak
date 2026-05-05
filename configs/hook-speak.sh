#!/bin/bash
# Stop Hook: 从 transcript 文件中提取本次会话所有 Claude 回复文本
# iAgent 调用 Claude 时设 ISPEAK_SKIP=1，此时跳过（iAgent 自己播）
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

# 来源参数: claude 或 codex
SOURCE="${1:-claude}"

SOCK=/tmp/ispeak.sock
LOG="$HOME/.config/iSpeak/hook.log"

input=$(cat)

# 从 stdin JSON 提取 transcript 路径和最后一条消息
# 简单 JSON 解析（不依赖 python3）
transcript=$(echo "$input" | sed -n 's/.*"transcript_path"\s*:\s*"\([^"]*\)".*/\1/p')
last_msg=$(echo "$input" | sed -n 's/.*"last_assistant_message"\s*:\s*"\([^"]*\)".*/\1/p')

all_text="$last_msg"

# 如果有 transcript 文件，提取最近 30 秒内的所有 assistant 消息
if [[ -n "$transcript" && -f "$transcript" ]]; then
  # 计算 30 秒前的时间戳（毫秒）
  cutoff=$(($(date +%s) * 1000 - 30000))

  # 用 awk 解析 JSON lines，提取 role=assistant 且 timestamp > cutoff 的 text
  extra=$(awk -v cutoff="$cutoff" '
    {
      # 提取 timestamp
      if (match($0, /"timestamp"\s*:\s*[0-9]+/)) {
        ts = substr($0, RSTART, RLENGTH)
        gsub(/[^0-9]/, "", ts)
        ts = int(ts)
        if (ts < cutoff) next
      }

      # 提取 role
      if (match($0, /"role"\s*:\s*"assistant"/)) {
        # 提取 content（可能是字符串或数组）
        if (match($0, /"content"\s*:\s*\[/)) {
          # 数组形式，提取所有 text 字段
          gsub(/[^{]*\[/, "", $0)
          gsub(/\].*/, "", $0)
          while (match($0, /"text"\s*:\s*"[^"]*"/)) {
            t = substr($0, RSTART, RLENGTH)
            gsub(/"text"\s*:\s*"/, "", t)
            gsub(/"$/, "", t)
            if (t != "") print t
            $0 = substr($0, RSTART + RLENGTH)
          }
        } else if (match($0, /"content"\s*:\s*"\([^"]*\)"/)) {
          t = substr($0, RSTART, RLENGTH)
          gsub(/"content"\s*:\s*"/, "", t)
          gsub(/"$/, "", t)
          if (t != "") print t
        }
      }
    }
  ' "$transcript" 2>/dev/null | sort -u | tr '\n' ' ')

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
