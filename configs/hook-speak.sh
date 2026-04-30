#!/bin/bash
# Stop Hook: 从 transcript 文件中提取本次会话所有 Claude 回复文本
# iAgent 调用 Claude 时设 ISPEAK_SKIP=1，此时跳过（iAgent 自己播）
[[ "$ISPEAK_SKIP" == "1" ]] && exit 0

SOCK=/tmp/iagent.tts.sock
LOG="$HOME/.config/iSpeak/hook.log"

input=$(cat)
echo "$input" > "$HOME/.config/iSpeak/hook-input.json"

echo "=== $(date) ===" >> "$LOG"

# 从 stdin JSON 提取 transcript 路径和最后一条消息
transcript=$(echo "$input" | python3 -c "import json,sys; print(json.loads(sys.stdin.read()).get('transcript_path',''))" 2>/dev/null)
last_msg=$(echo "$input" | python3 -c "import json,sys; print(json.loads(sys.stdin.read()).get('last_assistant_message',''))" 2>/dev/null)

all_text="$last_msg"

# 如果有 transcript 文件，提取当前 session 中未播过的 assistant 消息
if [[ -n "$transcript" && -f "$transcript" ]]; then
  # 提取最近 30 秒内的所有 assistant 消息（避免播历史消息）
  cutoff=$(python3 -c "import time; print(int((time.time() - 30) * 1000))" 2>/dev/null)

  extra=$(python3 -c "
import json, sys, time

cutoff = int((time.time() - 30) * 1000)
texts = []

with open('$transcript') as f:
    for line in f:
        try:
            msg = json.loads(line.strip())
        except:
            continue
        # 只取 assistant 的 text 消息
        if msg.get('role') == 'assistant':
            ts = msg.get('timestamp', 0)
            if ts < cutoff:
                continue  # 跳过 30 秒前的旧消息
            content = msg.get('content', '')
            if isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get('type') == 'text':
                        t = block.get('text', '').strip()
                        if t:
                            texts.append(t)
                    elif isinstance(block, str) and block.strip():
                        texts.append(block.strip())
            elif isinstance(content, str) and content.strip():
                texts.append(content.strip())

# 去重 + 保持顺序
seen = set()
unique = []
for t in texts:
    if t not in seen:
        seen.add(t)
        unique.append(t)

print('\n'.join(unique))
" 2>/dev/null)

  if [[ -n "$extra" ]]; then
    all_text="$extra"
  fi
fi

echo "TEXT_LEN: ${#all_text}" >> "$LOG"
echo "PREVIEW: ${all_text:0:150}" >> "$LOG"

if [[ -n "$all_text" && -S "$SOCK" ]]; then
  echo "$all_text" | nc -U -w5 "$SOCK" 2>> "$LOG"
  echo "SPOKE: OK" >> "$LOG"
else
  echo "SPOKE: SKIP" >> "$LOG"
fi
