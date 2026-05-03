# iSpeak

小而稳的本地 TTS 播报服务：接收文本，转换语音并顺序播放。

## 组成

- `/usr/local/bin/ispeakd`：守护进程（监听 `/tmp/ispeak.sock`）
- `/usr/local/bin/ispeak`：命令入口（status/test/restart/logs/tail/say）

## 快速安装

```bash
cd /path/to/iSpeak
sudo make install
make status
ispeak test "飞哥你好"
```

## 常用命令

```bash
ispeak "任务完成"          # 日常播报（等价于 ispeak say "任务完成"）
ispeak test               # 自检播报（默认测试文案）
ispeak test "飞哥你好"     # 自检播报（自定义文案）
ispeak status             # 查看服务/socket/二进制
ispeak restart            # 重启服务
ispeak recover            # 重启 + 状态检查 + 测试播报
ispeak logs 80            # 查看最近 80 行日志
ispeak tail               # 实时日志
```

## Makefile 命令

```bash
make build    # 构建 build/ispeakd
make install  # 停止 -> 卸载旧版本 -> 安装 -> 启动
make deploy   # install + 配置文件部署（config/hook/plist）
```

说明：`make` 只负责“构建与安装”，运行态操作统一走 `ispeak`。

## 配置

配置文件路径：`~/.config/iSpeak/config.json`

```json
{
  "appId": "3059945724",
  "accessToken": "...",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "resourceId": "seed-tts-2.0",
  "voiceType": "zh_female_tianmeitaozi_uranus_bigtts"
}
```

也支持环境变量：
- `IAGENT_TTS_APP_ID`
- `IAGENT_TTS_ACCESS_TOKEN`
- `IAGENT_TTS_ENDPOINT`
- `IAGENT_TTS_RESOURCE_ID`
- `IAGENT_TTS_VOICE_TYPE`

## Claude / Codex Hook

Stop Hook 命令：

```json
{
  "type": "command",
  "command": "bash $HOME/.config/iSpeak/hook-speak.sh",
  "timeout": 30
}
```

部署时会安装到：`~/.config/iSpeak/hook-speak.sh`。

## 稳定性策略

- TTS 并发、播放串行（避免音频重叠）
- TTS 并发上限：`4`
- 失败自动重试：`1` 次
- 关键 worker 带 `panic recover`

## 路径速查

- `~/Library/LaunchAgents/com.iSpeak.plist`
- `/tmp/ispeak.sock`
- `/tmp/iSpeak.log`
- `/usr/local/bin/ispeakd`
- `/usr/local/bin/ispeak`

## License

MIT
