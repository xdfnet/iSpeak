#!/bin/bash
# iSpeak 一键安装脚本
set -euo pipefail

VERSION="1.1.0"
CONFIG_DIR="$HOME/.config/iSpeak"
BIN_DIR="$HOME/.local/bin"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()   { echo -e "${GREEN}[iSpeak]${NC} $1"; }
warn()  { echo -e "${YELLOW}[iSpeak]${NC} $1"; }
err()   { echo -e "${RED}[iSpeak]${NC} $1" >&2; }

echo ""
echo "  iSpeak $VERSION 一键安装"
echo "============================================"
echo ""

# ========== 1. 安装服务 ==========
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [[ -S /tmp/ispeak.sock ]] && pgrep -x ispeakd > /dev/null 2>&1; then
  log "服务已在运行，跳过安装"
else
  log "安装服务..."
  mkdir -p "$BIN_DIR"

  # 已有本地构建则用本地的，否则下载 Release
  if [[ -f "$SCRIPT_DIR/build/ispeakd" ]]; then
    install -m 0755 "$SCRIPT_DIR/build/ispeakd" "$BIN_DIR/ispeakd"
  elif [[ -f "$BIN_DIR/ispeakd" ]]; then
    log "二进制已存在"
  else
    err "未找到 ispeakd，请先 clone 源码或从 Release 下载"
    exit 1
  fi

  install -m 0755 "$SCRIPT_DIR/scripts/ispeak" "$BIN_DIR/ispeak"
  ln -sf "$BIN_DIR/ispeak" "$BIN_DIR/ispeak-claude"
  ln -sf "$BIN_DIR/ispeak" "$BIN_DIR/ispeak-codex"

  # 部署 plist
  sed "s|BINARY_PATH_PLACEHOLDER|$BIN_DIR/ispeakd|" \
    "$SCRIPT_DIR/configs/com.iSpeak.plist" > ~/Library/LaunchAgents/com.iSpeak.plist"

  # 启动
  launchctl unload ~/Library/LaunchAgents/com.iSpeak.plist 2>/dev/null || true
  launchctl load ~/Library/LaunchAgents/com.iSpeak.plist
  sleep 0.5
fi

# ========== 2. 配置 API Key ==========
CONFIG_FILE="$CONFIG_DIR/config.json"
mkdir -p "$CONFIG_DIR"

# 已有有效配置则跳过
if [[ -f "$CONFIG_FILE" ]]; then
  KEY_IN_CONFIG=$(sed -n 's/.*"apiKey"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG_FILE" 2>/dev/null | head -1)
  if [[ -n "$KEY_IN_CONFIG" && "$KEY_IN_CONFIG" != "your-api-key" ]]; then
    log "配置文件已就绪，跳过"
    API_KEY="$KEY_IN_CONFIG"
  fi
fi

if [[ -z "${API_KEY:-}" ]]; then
  echo ""
  echo "请输入火山引擎 API Key："
  echo "获取地址: https://console.volcengine.com/tts"
  echo -n "→ "
  read -r API_KEY
  [[ -z "$API_KEY" ]] && { err "API Key 不能为空"; exit 1; }
fi

log "写入配置..."
cat > "$CONFIG_FILE" <<EOF
{
  "apiKey": "$API_KEY",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "defaultVoice": {
    "voice_type": "zh_female_mizai_uranus_bigtts",
    "resourceId": "seed-tts-2.0"
  },
  "sourceVoices": {
    "claude": {
      "voice_type": "zh_female_tianmeitaozi_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    },
    "codex": {
      "voice_type": "zh_male_shaonianzixin_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    }
  }
}
EOF

# ========== 3. 安装 Hook 脚本 ==========
HOOK_FILE="$CONFIG_DIR/hook-speak.sh"
if [[ ! -f "$HOOK_FILE" ]]; then
  cp "$SCRIPT_DIR/configs/hook-speak.sh" "$HOOK_FILE"
  chmod +x "$HOOK_FILE"
fi

# ========== 4. 配置 AI 助手 Hook ==========
install_hook() {
  local settings_file="$1"
  local source="$2"
  local hook_cmd="bash $HOME/.config/iSpeak/hook-speak.sh $source"

  [[ ! -f "$settings_file" ]] && { warn "未找到 $source 配置，跳过"; return; }
  grep -q "hook-speak.sh" "$settings_file" 2>/dev/null && { log "$source Hook 已配置 ✓"; return; }

  if command -v jq &>/dev/null; then
    local tmp=$(mktemp)
    jq --arg cmd "$hook_cmd" \
      '.hooks.Stop[0].hooks += [{"type": "command", "command": $cmd, "timeout": 30}]' \
      "$settings_file" > "$tmp" && mv "$tmp" "$settings_file"
    log "$source Hook 配置完成 ✓"
  else
    warn "$source: 安装 jq 可自动配置 Hook: brew install jq"
    warn "$source: 或手动在 $settings_file 添加 Stop Hook"
    echo "  { \"type\": \"command\", \"command\": \"$hook_cmd\", \"timeout\": 30 }"
  fi
}

echo ""
log "配置 AI 助手 Hook..."
install_hook "$HOME/.claude/settings.json" "Claude Code"
install_hook "$HOME/.codex/hooks.json" "Codex"

# ========== 5. 自检 ==========
echo ""
echo "============================================"
echo "  自检"
echo "============================================"
echo ""

# 状态检查
IS_RUNNING=false
[[ -S /tmp/ispeak.sock ]] && IS_RUNNING=true

if $IS_RUNNING; then
  log "服务状态: 运行中 ✓"
else
  err "服务未运行，请检查日志: /tmp/iSpeak.log"
  exit 1
fi

# 测试播报（静默）
echo "测试语音..."
ispeak "iSpeak 安装完成" 2>/dev/null && log "语音测试: 成功 ✓" || warn "语音测试: 失败"

echo ""
log "安装完成！"
echo ""

if ! command -v jq &>/dev/null; then
  echo "提示: 安装 jq 可以自动配置 Hook"
  echo "  brew install jq"
  echo ""
fi
echo "请重启 Claude Code / Codex 让 Hook 生效"
echo ""
