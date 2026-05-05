#!/bin/bash
# iSpeak 一键安装脚本
set -euo pipefail

VERSION="1.3.0"
REPO="xdfnet/iSpeak"
CONFIG_DIR="$HOME/.config/iSpeak"
BIN_DIR="$HOME/.local/bin"
GITHUB="https://github.com/$REPO/releases/download"
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

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SOCK="$CONFIG_DIR/ispeak.sock"
LOG="$CONFIG_DIR/ispeak.log"

# ========== 1. 安装二进制 ==========
mkdir -p "$BIN_DIR"
mkdir -p "$CONFIG_DIR"

if [[ -f "$SCRIPT_DIR/build/ispeakd" ]]; then
  # 源码目录：本地构建
  log "安装本地构建..."
  install -m 0755 "$SCRIPT_DIR/build/ispeakd" "$BIN_DIR/ispeakd"
  install -m 0755 "$SCRIPT_DIR/scripts/ispeak" "$BIN_DIR/ispeak"
elif [[ -f "$BIN_DIR/ispeakd" ]]; then
  log "二进制已存在"
else
  # 下载 Release
  log "下载 Release..."
  curl -fsSL "$GITHUB/v$VERSION/ispeakd" -o "$BIN_DIR/ispeakd" || {
    err "下载失败，请到 https://github.com/$REPO/releases 下载"
    exit 1
  }
  chmod +x "$BIN_DIR/ispeakd"
  log "下载完成"
fi

ln -sf "$BIN_DIR/ispeakd" "$BIN_DIR/ispeakd" 2>/dev/null || true
ln -sf "$BIN_DIR/ispeak" "$BIN_DIR/ispeak-claude" 2>/dev/null || true
ln -sf "$BIN_DIR/ispeak" "$BIN_DIR/ispeak-codex" 2>/dev/null || true

# ========== 2. 安装 Hook 脚本 ==========
HOOK_FILE="$CONFIG_DIR/hook-speak.sh"
if [[ ! -f "$HOOK_FILE" ]]; then
  if [[ -f "$SCRIPT_DIR/configs/hook-speak.sh" ]]; then
    cp "$SCRIPT_DIR/configs/hook-speak.sh" "$HOOK_FILE"
  else
    curl -fsSL "https://raw.githubusercontent.com/$REPO/master/configs/hook-speak.sh" -o "$HOOK_FILE"
  fi
  chmod +x "$HOOK_FILE"
fi

# ========== 3. 配置 API Key ==========
CONFIG_FILE="$CONFIG_DIR/config.json"

if [[ -f "$CONFIG_FILE" ]]; then
  KEY_IN_CONFIG=$(sed -n 's/.*"apiKey"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG_FILE" 2>/dev/null | head -1)
  if [[ -n "$KEY_IN_CONFIG" && "$KEY_IN_CONFIG" != "your-api-key" ]]; then
    log "配置已就绪"
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

# ========== 4. 启动/重启服务 ==========
if [[ -S "$SOCK" ]] && pgrep ispeakd > /dev/null 2>&1; then
  log "服务已在运行"
else
  log "启动服务..."
  # 写入 plist
  sed "s|BINARY_PATH_PLACEHOLDER|$BIN_DIR/ispeakd|" \
    "$SCRIPT_DIR/configs/com.iSpeak.plist" 2>/dev/null || \
    curl -fsSL "https://raw.githubusercontent.com/$REPO/master/configs/com.iSpeak.plist" | \
    sed "s|BINARY_PATH_PLACEHOLDER|$BIN_DIR/ispeakd|" > ~/Library/LaunchAgents/com.iSpeak.plist

  launchctl unload ~/Library/LaunchAgents/com.iSpeak.plist 2>/dev/null || true
  launchctl load ~/Library/LaunchAgents/com.iSpeak.plist
  sleep 0.5
fi

# ========== 5. 配置 AI 助手 Hook ==========
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

# ========== 6. 自检 ==========
echo ""
echo "============================================"
echo "  自检"
echo "============================================"
echo ""

IS_RUNNING=false
[[ -S "$SOCK" ]] && IS_RUNNING=true

if $IS_RUNNING; then
  log "服务状态: 运行中 ✓"
else
  err "服务未运行，请检查日志: $LOG"
  exit 1
fi

echo "测试语音..."
"$BIN_DIR/ispeak" "iSpeak 安装完成" 2>/dev/null && log "语音测试: 成功 ✓" || warn "语音测试: 失败"

echo ""
log "安装完成！"
echo ""

if ! command -v jq &>/dev/null; then
  echo "提示: 安装 jq 可以自动配置 Claude Code Hook"
  echo "  brew install jq"
  echo ""
fi
