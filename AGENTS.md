# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## 项目概述

iSpeak — 字节跳动 TTS 本地播报服务。守护进程 `ispeakd` 监听 Unix Socket，接收文本后调用火山引擎 TTS 流式 API，边合成边播放。

## 常用命令

```bash
make build      # 编译 ispeakd
make install    # 安装 + 启动 launchd 服务
make deploy     # 同 install
make uninstall  # 卸载（停止服务 + 删除文件）
make clean      # 清理编译产物
make help       # 显示帮助
```

## 命令行测试约定

- 测试 Claude：`claude -p "你好"`
- 测试 Codex：`codex exec "你好"`

## 架构

```
ispeak (CLI, bash)
  └─ nc -U ~/.config/iSpeak/ispeak.sock
      └─ ispeakd (Go daemon)
           └─ Player (channel, buffer=1)
                └─ loop goroutine: 单 AVAudioEngine 实例复用
                     └─ SSE PCM chunk → AVAudioEngine
```

- **Socket**: `~/.config/iSpeak/ispeak.sock`
- **日志**: `~/.config/iSpeak/ispeak.log` (lumberjack 轮转, 10MB/份, 保留3份)
- **Temp**: 进程级 tempDir，退出时清理
- **Launchd PLIST**: `~/Library/LaunchAgents/com.ispeak.plist`

## 核心文件

- `main.go` — 守护进程、Player (channel 驱动)、TTS 流式请求、SSE 解析
- `avaudioengine_player_darwin.go` — macOS 原生 `AVAudioEngine` PCM 播放器
- `clean_text.go` — TTS 播报文本清洗
- `main_test.go` — 任务引擎关键行为测试
- `scripts/ispeak` — CLI 入口，通过 nc 发送文本到 socket
- `configs/hook-speak.sh` — Claude/Codex Hook，bash + Node 解析输入

## 消息格式

CLI 与 daemon 通过 socket 传输原始文本，支持音色前缀：

```
{source:claude}文本  → 使用 claude 来源音色
{source:codex}文本   → 使用 codex 来源音色
文本                 → 使用默认音色
```

## 任务策略（节省 TTS 费用）

新消息到达时：
1. 丢弃 channel 中排队的旧消息
2. 不打断当前正在合成/播放的任务
3. 新消息入队

## 失败策略

- 流式合成/播放失败：日志记录，继续处理下一条，不重试

## 配置

`~/.config/iSpeak/config.json`:

```json
{
  "apiKey": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse",
  "defaultVoice": { "voice_type": "zh_female_mizai_uranus_bigtts", "resourceId": "seed-tts-2.0" },
  "sourceVoices": {
    "claude": { "voice_type": "zh_female_tianmeitaozi_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "codex": { "voice_type": "zh_male_shaonianzixin_uranus_bigtts", "resourceId": "seed-tts-2.0" }
  }
}
```

## 稳定性设计

- 单 Player goroutine，合成与播放同链路，降低首播延迟
- AVAudioEngine 实例复用，避免重复初始化开销
- Channel buffer=1 + drain，新消息自动丢弃旧排队消息
- 配置热更新（mtime 缓存 + 自动重载）
- TTS HTTP Client 复用，减少连接开销
- 主链路使用 macOS 原生 `AVAudioEngine` 播放 PCM
- 合成/播放失败直接跳过，不重试
- 日志轮转，防止文件过大
