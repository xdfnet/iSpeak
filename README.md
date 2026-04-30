# iSpeak

TTS 播报守护进程。监听 Unix Socket，收到文本 → 字节跳动 TTS → 播放。

**260 行 Go · 0 外部依赖 · 8.2MB 二进制 · 开机自启。**

## 架构

```
Hook / 终端                    iSpeak
┌──────────┐     nc -U        ┌─────────────────────┐
│  speak   │ ──────────────→  │  Unix Socket 监听    │
│  Hook    │                  │  ↓                   │
└──────────┘                  │  拆句                │
                              │  ↓                   │
                              │  字节 TTS SSE API     │
                              │  ↓                   │
                              │  afplay 播放          │
                              └─────────────────────┘
```

## 安装

```bash
cd /Users/admin/iCode/iSpeak
go build -o iSpeak .
sudo cp iSpeak /usr/local/bin/iSpeak
sudo ln -sf /Users/admin/iCode/iSpeak/speak /usr/local/bin/speak
```

## 配置

`~/.config/iSpeak/config.json`:

```json
{
  "appId": "...",
  "accessToken": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "resourceId": "seed-tts-2.0",
  "voiceType": "zh_female_tianmeitaozi_uranus_bigtts"
}
```

也支持环境变量: `IAGENT_TTS_APP_ID`、`IAGENT_TTS_ACCESS_TOKEN`、`IAGENT_TTS_ENDPOINT`、`IAGENT_TTS_RESOURCE_ID`、`IAGENT_TTS_VOICE_TYPE`。

## 使用

```bash
speak "飞哥你好"
echo "任务完成" | speak
```

## 自启动

```bash
# 加载
launchctl load ~/Library/LaunchAgents/com.iSpeak.plist
# 卸载
launchctl unload ~/Library/LaunchAgents/com.iSpeak.plist
# 查看日志
tail -f /tmp/iSpeak.log
```
