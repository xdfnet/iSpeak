#!/bin/bash
# iSpeak 一键安装脚本
# 运行方式: bash -c "$(curl -fsSL https://raw.githubusercontent.com/xdfnet/iSpeak/master/setup.sh)"
set -euo pipefail

VERSION="1.1.0"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[iSpeak]${NC} $1"; }
warn() { echo -e "${YELLOW}[iSpeak]${NC} $1"; }
err() { echo -e "${RED}[iSpeak]${NC} $1" >&2; }

echo ""
echo "============================================"
echo "  iSpeak $VERSION - 一键安装"
echo "============================================"
echo ""

# ========== 1. 检出 / 安装 ==========
log "检查安装状态..."

if command -v ispeak &>/dev/null && [[ -S /tmp/ispeak.sock ]]; then
  warn "iSpeak 已安装且正在运行，跳过二进制安装"
  INSTALLED=true
else
  INSTALLED=false
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

  if [[ -d "$SCRIPT_DIR/.git" ]]; then
    log "从源码目录安装..."
    cd "$SCRIPT_DIR"
    make install >/dev/null 2>&1 || { err "make install 失败"; exit 1; }
  else
    log "安装 ispeakd..."
    BIN_DIR="$HOME/.local/bin"
    mkdir -p "$BIN_DIR"
    # 下载最新二进制
    ASSETS_URL="https://github.com/xdfnet/iSpeak/releases/latest/download"
    if command -v curl &>/dev/null; then
      curl -fsSL "$ASSETS_URL/ispeakd" -o "$BIN_DIR/ispeakd" && chmod +x "$BIN_DIR/ispeakd"
    fi
    curl -fsSL "$ASSETS_URL/ispeak" -o "$BIN_DIR/ispeak" && chmod +x "$BIN_DIR/ispeak"
    ln -sf "$BIN_DIR/ispeak" "$BIN_DIR/ispeak-claude"
    ln -sf "$BIN_DIR/ispeak" "$BIN_DIR/ispeak-codex"
  fi
fi

# ========== 2. API Key ==========
CONFIG_DIR="$HOME/.config/iSpeak"
CONFIG_FILE="$CONFIG_DIR/config.json"
mkdir -p "$CONFIG_DIR"

if [[ -f "$CONFIG_FILE" ]]; then
  # 已有配置，尝试读取 apiKey
  EXISTING_KEY=$(cat "$CONFIG_FILE" 2>/dev/null | sed -n 's/.*"apiKey"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
  if [[ -n "$EXISTING_KEY" && "$EXISTING_KEY" != "your-api-key" ]]; then
    warn "已有 API Key，跳过输入"
    API_KEY="$EXISTING_KEY"
  fi
fi

if [[ -z "${API_KEY:-}" ]]; then
  echo ""
  echo "请输入火山引擎 API Key："
  echo "(获取地址: https://console.volcengine.com/tts)"
  echo -n "→ "
  read -r API_KEY
  if [[ -z "$API_KEY" ]]; then
    err "API Key 不能为空"
    exit 1
  fi
fi

# ========== 3. 写入配置 ==========
log "写入配置文件..."
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

# ========== 4. Hook 安装 ==========
HOOK_FILE="$CONFIG_DIR/hook-speak.sh"
if [[ ! -f "$HOOK_FILE" ]]; then
  if [[ -f "$(dirname "$0")/configs/hook-speak.sh" ]]; then
    cp "$(dirname "$0")/configs/hook-speak.sh" "$HOOK_FILE"
  else
    # 下载 hook 脚本
    HOOK_URL="https://raw.githubusercontent.com/xdfnet/iSpeak/master/configs/hook-speak.sh"
    curl -fsSL "$HOOK_URL" -o "$HOOK_FILE"
  fi
  chmod +x "$HOOK_FILE"
fi

echo ""
echo "============================================"
echo "  配置 AI 助手 Hook"
echo "============================================"
echo ""

configure_claude_code() {
  local settings_file="$HOME/.claude/settings.json"
  local hook_cmd="bash $HOME/.config/iSpeak/hook-speak.sh claude"
  local hook_json='"command": "bash $HOME/.config/iSpeak/hook-speak.sh claude"'

  if [[ ! -f "$settings_file" ]]; then
    warn "未找到 Claude Code 配置 ($settings_file)，跳过"
    return
  fi

  log "配置 Claude Code Hook..."

  # 检查是否已有 ispeak hook
  if grep -q "hook-speak.sh" "$settings_file" 2>/dev/null; then
    warn "Claude Code Hook 已配置，跳过"
    return
  fi

  # 使用 jq 合并（如果有）
  if command -v jq &>/dev/null; then
    local tmp=$(mktemp)
    jq --arg cmd "$hook_cmd" '.hooks.Stop = [.hooks.Stop[] | .hooks += [{"type": "command", "command": $cmd, "timeout": 30}]] | .hooks.Stop[0].hooks = [.hooks.Stop[0].hooks[] | select(.command | contains("hook-speak") | not)] + [{"type": "command", "command": $cmd, "timeout": 30}]' "$settings_file" > "$tmp" 2>/dev/null && mv "$tmp" "$settings_file"
    log "Claude Code Hook 配置完成 ✓"
  else
    warn "推荐安装 jq: brew install jq"
    warn "或手动在 $settings_file 添加 Stop Hook："
    echo "  { \"type\": \"command\", \"command\": \"$hook_cmd\", \"timeout\": 30 }"
  fi
}

configure_codex() {
  local hooks_file="$HOME/.codex/hooks.json"
  local hook_cmd="bash $HOME/.config/iSpeak/hook-speak.sh codex"

  if [[ ! -f "$hooks_file" ]]; then
    warn "未找到 Codex 配置 ($hooks_file)，跳过"
    return
  fi

  log "配置 Codex Hook..."

  if grep -q "hook-speak.sh" "$hooks_file" 2>/dev/null; then
    warn "Codex Hook 已配置，跳过"
    return
  fi

  if command -v jq &>/dev/null; then
    local tmp=$(mktemp)
    jq --arg cmd "$hook_cmd" '.hooks.Stop = [.hooks.Stop[] | .hooks += [{"type": "command", "command": $cmd, "timeout": 30}]]' "$hooks_file" > "$tmp" 2>/dev/null && mv "$tmp" "$hooks_file"
    log "Codex Hook 配置完成 ✓"
  else
    warn "推荐安装 jq: brew install jq"
    warn "或手动在 $hooks_file 添加 Stop Hook："
    echo "  { \"type\": \"command\", \"command\": \"$hook_cmd\", \"timeout\": 30 }"
  fi
}

configure_claude_code
configure_codex

# ========== 5. 启动服务 ==========
log "启动服务..."
launchctl unload ~/Library/LaunchAgents/com.iSpeak.plist 2>/dev/null || true
sed "s|BINARY_PATH_PLACEHOLDER|$HOME/.local/bin/ispeakd|" "$HOME/.config/iSpeak/../iSpeak/com.iSpeak.plist" 2>/dev/null || \
sed "s|BINARY_PATH_PLACEHOLDER|$HOME/.local/bin/ispeakd|" "$(dirname "$0")/configs/com.iSpeak.plist" > ~/Library/LaunchAgents/com.iSpeak.plist 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/com.iSpeak.plist 2>/dev/null || true
sleep 0.5

# ========== 6. 自检 ==========
echo ""
echo "============================================"
echo "  自检"
echo "============================================"
echo ""

ispeak status
echo ""

log "一键安装完成！"
echo ""
echo "下一步："
echo "  1. 重启 Claude Code / Codex 让 Hook 生效"
echo "  2. 试试: ispeak test \"iSpeak 工作正常\""
echo ""
