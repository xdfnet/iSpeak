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

# ========== 1. 安装服务 ==========
if [[ -S "$SOCK" ]] && pgrep ispeakd > /dev/null 2>&1; then
  log "服务已在运行"
else
  if [[ -d "$SCRIPT_DIR/.git" ]]; then
    # 源码目录：make install
    log "编译并安装..."
    cd "$SCRIPT_DIR" && make install >/dev/null 2>&1 || { err "make install 失败"; exit 1; }
  else
    # 下载 Release
    log "下载 Release..."
    mkdir -p "$BIN_DIR"
    curl -fsSL "$GITHUB/v$VERSION/ispeakd" -o "$BIN_DIR/ispeakd" || {
      err "下载失败，请到 https://github.com/$REPO/releases 下载"
      exit 1
    }
    chmod +x "$BIN_DIR/ispeakd"

    # 下载 hook
    mkdir -p "$CONFIG_DIR"
    curl -fsSL "https://raw.githubusercontent.com/$REPO/master/configs/hook-speak.sh" -o "$CONFIG_DIR/hook-speak.sh"
    chmod +x "$CONFIG_DIR/hook-speak.sh"

    # 写入 plist 并启动
    curl -fsSL "https://raw.githubusercontent.com/$REPO/master/configs/com.iSpeak.plist" | \
      sed "s|BINARY_PATH_PLACEHOLDER|$BIN_DIR/ispeakd|" > ~/Library/LaunchAgents/com.iSpeak.plist
    launchctl load ~/Library/LaunchAgents/com.iSpeak.plist
    sleep 0.5
  fi
fi

# ========== 2. 配置 API Key ==========
CONFIG_FILE="$CONFIG_DIR/config.json"
mkdir -p "$CONFIG_DIR"

if [[ -f "$CONFIG_FILE" ]]; then
  KEY_IN_CONFIG=$(sed -n 's/.*"apiKey"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG_FILE" 2>/dev/null | head -1)
  [[ -n "$KEY_IN_CONFIG" && "$KEY_IN_CONFIG" != "your-api-key" ]] && { log "配置已就绪"; API_KEY="$KEY_IN_CONFIG"; }
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

# ========== 3. 配置 Hook ==========
install_hook() {
  local file="$1" src="$2" cmd="bash $HOME/.config/iSpeak/hook-speak.sh $src"
  [[ ! -f "$file" ]] && { warn "未找到 $src 配置，跳过"; return; }
  grep -q "hook-speak.sh" "$file" 2>/dev/null && { log "$src Hook 已配置 ✓"; return; }
  if command -v jq &>/dev/null; then
    local tmp=$(mktemp)
    jq --arg c "$cmd" '.hooks.Stop[0].hooks += [{"type": "command", "command": $c, "timeout": 30}]' "$file" > "$tmp" && mv "$tmp" "$file"
    log "$src Hook 配置完成 ✓"
  else
    warn "$src: jq 未安装，无法自动配置 Hook"
  fi
}

echo ""
log "配置 Hook..."
install_hook "$HOME/.claude/settings.json" "Claude Code"
install_hook "$HOME/.codex/hooks.json" "Codex"

# ========== 4. 自检 ==========
echo ""
echo "============================================"
echo "  自检"
echo "============================================"
echo ""

if [[ -S "$SOCK" ]]; then
  log "服务运行中 ✓"
else
  err "服务未运行，请检查日志: $LOG"
  exit 1
fi

"$BIN_DIR/ispeak" "安装完成" 2>/dev/null && log "语音测试: 成功 ✓" || warn "语音测试: 失败"

echo ""
log "安装完成！"
echo ""
