# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## 项目概述

iSpeak — 字节跳动 TTS 本地播报服务。守护进程 `ispeakd` 监听 Unix Socket，接收文本后调用火山引擎 TTS 流式 API，边合成边播放。

## 常用命令

```bash
make build      # 编译 ispeakd
make test       # Go 测试、race 测试、构建、hook fixture、npm dry-run
make install    # 安装 + 启动 launchd 服务
make deploy     # 同 install
make uninstall  # 卸载（停止服务 + 删除文件）
make clean      # 清理编译产物
make help       # 显示帮助
```

## 命令行测试约定

- 测试 Claude：`claude -p "你好"`
- 测试 Codex：`codex exec "你好"`
- 测试 Copilot：重启 Copilot CLI 后触发一次 agent 回复
- 手动测试来源音色：`ispeak-claude "你好"` / `ispeak-codex "你好"` / `ispeak-copilot "你好"` / `ispeak-pi "你好"`

## 架构

```
ispeak / ispeak-claude / ispeak-codex / ispeak-copilot / ispeak-pi (CLI, bash)
  └─ nc -U ~/.config/iSpeak/ispeak.sock
      └─ ispeakd (Go daemon)
           └─ Player (channel, buffer=1)
                └─ loop goroutine: 单 AVAudioEngine 实例复用
                     └─ SSE PCM chunk → AVAudioEngine
```

- **Socket**: `~/.config/iSpeak/ispeak.sock`
- **日志**: `~/.config/iSpeak/ispeak.log` (lumberjack 轮转, 10MB/份, 保留3份)
- **Launchd PLIST**: `~/Library/LaunchAgents/com.ispeak.plist`

## 核心文件

- `main.go` — 守护进程、Player (channel 驱动)、TTS 流式请求、SSE 解析
- `avaudioengine_player_darwin.go` — macOS 原生 `AVAudioEngine` PCM 播放器
- `clean_text.go` — TTS 播报文本清洗
- `main_test.go` — 测试套件
- `scripts/ispeak` — 主 CLI 入口，通过 nc 发送文本到 socket
- `scripts/ispeak-claude` / `scripts/ispeak-codex` / `scripts/ispeak-copilot` / `scripts/ispeak-pi` — 来源 wrapper，自动注入 `{source:...}`
- `configs/hook-speak.sh` — Claude/Codex/Copilot CLI Hook，bash + Node 解析输入；Copilot 会等待最新 transcript 落盘
- `configs/ispeak.ts` — Pi Extension，发送 `{source:pi}` 到 socket

## 发布指南

```bash
# 1. 改版本号（唯一需要改的地方）
#    编辑 package.json → "version": "x.y.z"

# 2. 更新 README badge（跟一下即可）
#    README.md → version-x.y.z-blue

# 3. 提交并发布
git add -A && git commit -m "release: vx.y.z — <简述>"
make release
```

`make release` 会自动：测试 → 检查工作区 → 创建 tag → 推送 → npm 发布。
Makefile 和 `scripts/ispeak` 自动从 `package.json` 读取版本号，无需手动同步。

## 消息格式

CLI 与 daemon 通过 socket 传输原始文本，支持音色前缀：

```
{source:claude}文本   → 使用 claude 来源音色
{source:codex}文本   → 使用 codex 来源音色
{source:copilot}文本  → 使用 copilot 来源音色
{source:pi}文本       → 使用 pi 来源音色
文本                 → 使用默认音色
```

## 任务策略（节省 TTS 费用）

新消息到达时：
1. 丢弃 channel 中排队的旧消息
2. 不打断当前正在合成/播放的任务
3. 新消息入队

## 失败策略

- 合成失败：记录日志，继续处理下一条，不重试
- 播放器写入失败：关闭旧实例，新建 AVAudioEngine 继续工作
- loop goroutine panic：自动重启

## 配置

`~/.config/iSpeak/config.json`:

```json
{
  "apiKey": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse",
  "defaultVoice": { "voice_type": "zh_female_mizai_uranus_bigtts", "resourceId": "seed-tts-2.0" },
  "sourceVoices": {
    "claude": { "voice_type": "zh_female_tianmeitaozi_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "codex": { "voice_type": "zh_female_xiaohe_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "copilot": { "voice_type": "zh_male_dayi_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "pi": { "voice_type": "zh_male_taocheng_uranus_bigtts", "resourceId": "seed-tts-2.0" }
  }
}
```

## 稳定性设计

- 单 Player goroutine，合成与播放同链路，降低首播延迟
- loop recover：goroutine 崩溃后自动重启
- AVAudioEngine 实例复用，写入失败自动重建
- AVAudioPCMBuffer completion 后释放，避免长期播报按 chunk 泄漏
- Channel buffer=1 + drain，新消息自动丢弃旧排队消息
- Copilot `agentStop` 可能先于 transcript 最新消息落盘，hook 通过 `hook.last` 文本 hash 等待新回复
- 配置热更新（mtime 缓存，每次连接重新加载）
- TTS HTTP Client 复用，30s 超时
- 日志轮转，防止文件过大
