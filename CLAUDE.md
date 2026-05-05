# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

iSpeak — 字节跳动 TTS 本地播报服务。守护进程 `ispeakd` 监听 Unix Socket，接收文本后调用火山引擎 TTS API，生成音频后通过 `afplay` 串行播放。

## 常用命令

```bash
make build    # 编译 ispeakd
make install  # 安装 + 启动 launchd 服务
make deploy   # 完整部署（install + 配置文件）
```

## 架构

```
ispeak (CLI, bash)
  └─ nc -U /tmp/ispeak.sock
      └─ ispeakd (Go daemon)
           ├─ TTS worker pool (并发上限 4)
           └─ playbackWorker (串行播放队列)
```

- **Socket**: `/tmp/ispeak.sock`
- **日志**: `/tmp/iSpeak.log`
- **Launchd PLIST**: `~/Library/LaunchAgents/com.iSpeak.plist`

### 核心文件

- `main.go` — 全部逻辑：守护进程、TTS 请求、SSE 解析、音频播放
- `scripts/ispeak` — CLI 入口（bash），通过 nc 发送文本到 socket
- `configs/hook-speak.sh` — Claude Code/Codex Stop Hook，提取回复文本并发送

### 消息格式

CLI 与 daemon 通过 socket 传输原始文本，支持音色前缀：

```
{source:claude}文本  → 使用 claude 来源音色
{source:codex}文本   → 使用 codex 来源音色
文本                 → 使用默认音色
```

### TTS API

- 字节跳动火山引擎 `openspeech.bytedance.com/api/v3/tts/unidirectional`
- SSE 流式响应，base64 编码音频分片
- 重试策略：失败重试 1 次，400ms 退避

### 配置

`~/.config/iSpeak/config.json`:

```json
{
  "apiKey": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "defaultVoice": { "voice_type": "zh_female_xianhe_uranus_bigtts", "resourceId": "seed-tts-2.0" },
  "sourceVoices": {
    "claude": { "voice_type": "...", "resourceId": "seed-tts-1.0" },
    "codex": { "voice_type": "...", "resourceId": "seed-tts-2.0" }
  }
}
```

### 稳定性设计

- TTS 并发上限 4，播放串行（避免音频重叠）
- TTS 失败自动重试 1 次
- playbackWorker 和 handleConnection 均有 `panic recover`
