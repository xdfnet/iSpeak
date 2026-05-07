# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

## 架构

```
ispeak (CLI, bash)
  └─ nc -U ~/.config/iSpeak/ispeak.sock
      └─ ispeakd (Go daemon)
           ├─ Task Engine (任务仓库)
           │    └─ pendingSynth FIFO
           └─ speakWorker (single)
                └─ pending_synth -> speaking -> delete
                     └─ SSE audio chunk -> ffplay stdin
```

- **Socket**: `~/.config/iSpeak/ispeak.sock`
- **日志**: `~/.config/iSpeak/ispeak.log` (lumberjack 轮转, 10MB/份, 保留3份)
- **Temp**: 进程级 tempDir，退出时清理
- **Launchd PLIST**: `~/Library/LaunchAgents/com.iSpeak.plist`

## 核心文件

- `main.go` — 守护进程、任务引擎、TTS 流式请求、SSE 解析、流式播放
- `main_test.go` — 任务引擎关键行为测试
- `scripts/ispeak` — CLI 入口，通过 nc 发送文本到 socket
- `configs/hook-speak.sh` — Claude/Codex Stop Hook，纯 bash 实现

## 消息格式

CLI 与 daemon 通过 socket 传输原始文本，支持音色前缀：

```
{source:claude}文本  → 使用 claude 来源音色
{source:codex}文本   → 使用 codex 来源音色
文本                 → 使用默认音色
```

## 任务与打断策略（节省 TTS 费用）

新消息到达时：
1. 删除所有 `pending_synth` 任务（未开始合成）
2. 打断当前 `speaking` 任务（取消合成/停止播放）
3. 创建新任务并进入 `pending_synth`

**任务状态流转：**
```
pending_synth → speaking → delete
```

## 失败策略

- 合成失败：直接删除任务，不重试
- 播放失败：直接删除任务，不重试

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

- 单 speak worker，合成与播放同链路，降低首播延迟
- 关键 goroutine 有 `panic recover`
- 配置热更新（mtime 缓存 + 自动重载）
- TTS HTTP Client 复用，减少连接开销
- 播放器子进程回执保障“播完再删任务”
- 日志轮转，防止文件过大
- 进程级 temp 目录，退出时自动清理
