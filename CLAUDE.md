# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

iSpeak — 字节跳动 TTS 本地播报服务。守护进程 `ispeakd` 监听 Unix Socket，接收文本后调用火山引擎 TTS API，生成音频按序号顺序播放。

## 常用命令

```bash
make build      # 编译 ispeakd
make install    # 安装 + 启动 launchd 服务
make deploy    # 同 install
make uninstall # 卸载（停止服务 + 删除文件）
make clean     # 清理编译产物
make help      # 显示帮助
```

## 架构

```
ispeak (CLI, bash)
  └─ nc -U /tmp/ispeak.sock
      └─ ispeakd (Go daemon)
           ├─ TTS Context Manager (single in-flight, cancel on new request)
           └─ playbackWorker (sequential by seq#, buffered reorder)
                └─ afplay
```

- **Socket**: `/tmp/ispeak.sock`
- **日志**: `/tmp/iSpeak.log` (lumberjack 轮转, 10MB/份, 保留3份)
- **Temp**: 进程级 tempDir，退出时清理
- **Launchd PLIST**: `~/Library/LaunchAgents/com.iSpeak.plist`

## 核心文件

- `main.go` — 全部逻辑：守护进程、TTS 请求、SSE 解析、音频播放
- `scripts/ispeak` — CLI 入口，通过 nc 发送文本到 socket
- `configs/hook-speak.sh` — Claude Code/Codex Stop Hook，纯 bash 实现
- `setup.sh` — 一键安装脚本

## 消息格式

CLI 与 daemon 通过 socket 传输原始文本，支持音色前缀：

```
{source:claude}文本  → 使用 claude 来源音色
{source:codex}文本   → 使用 codex 来源音色
文本                 → 使用默认音色
```

## 打断机制（节省 TTS 费用）

新消息到达时：
1. 打断正在播放的 afplay (SIGKILL)
2. 取消正在进行的 TTS 合成 (context cancel)

只为最终想听的那条消息付费。

## 配置

`~/.config/iSpeak/config.json`:

```json
{
  "apiKey": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "defaultVoice": { "voice_type": "zh_female_mizai_uranus_bigtts", "resourceId": "seed-tts-2.0" },
  "sourceVoices": {
    "claude": { "voice_type": "zh_female_tianmeitaozi_uranus_bigtts", "resourceId": "seed-tts-2.0" },
    "codex": { "voice_type": "zh_male_shaonianzixin_uranus_bigtts", "resourceId": "seed-tts-2.0" }
  }
}
```

## 稳定性设计

- 播放严格按序号顺序，不乱序
- 缓冲队列上限 64 条，超时 60s 跳过
- 播放失败自动重试 1 次
- 关键 goroutine 有 `panic recover`
- 配置热更新，无需重启服务
- 日志轮转，防止文件过大
- 进程级 temp 目录，退出时自动清理
