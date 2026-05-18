# 排障手册

先从这三条开始：

```bash
ispeak status
tail -n 80 ~/.config/iSpeak/ispeak.log
tail -n 80 ~/.config/iSpeak/hook.log 2>/dev/null || true
```

## 服务未运行

现象：

```text
进程: 未运行
Socket: ✗
```

处理：

```bash
ispeak restart
```

如果仍失败：

```bash
launchctl unload ~/Library/LaunchAgents/com.ispeak.plist 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/com.ispeak.plist
tail -n 120 ~/.config/iSpeak/ispeak.log
```

必要时重新安装：

```bash
cd /Users/admin/iCode/iSpeak
make install
```

## Socket 不存在

现象：

```text
Socket: ✗
ispeak: socket 不可用
```

处理：

```bash
ispeak restart
ls -l ~/.config/iSpeak/ispeak.sock
```

如果 `ispeakd` 正在运行但 socket 不存在，查看日志：

```bash
tail -n 120 ~/.config/iSpeak/ispeak.log
```

## 没有声音

先确认 daemon 是否收到文本：

```bash
tail -n 80 ~/.config/iSpeak/ispeak.log
```

如果看到：

```text
TTS [source]: ...
TTS: 完成
```

说明 socket、TTS、播放链路大概率已经完成。再检查 macOS 输出设备、系统音量、是否连接了蓝牙耳机。

如果没有看到 `TTS [...]`，说明文本没进 socket，继续查 hook 或 CLI。

## API Key 或配置错误

常见日志：

```text
配置错误: apiKey 未设置
配置错误，跳过本次播报
http status 401
http status 403
```

处理：

```bash
open ~/.config/iSpeak/config.json
```

确认：

- `apiKey` 非空
- `endpoint` 是 `/unidirectional/sse`
- `defaultVoice.voice_type` 和 `resourceId` 都存在
- `sourceVoices` 中每个来源都包含 `voice_type` 和 `resourceId`

校验 JSON：

```bash
node -e "JSON.parse(require('fs').readFileSync(process.env.HOME + '/.config/iSpeak/config.json','utf8')); console.log('ok')"
```

## TTS 返回 no audio data

常见原因：

- endpoint 配错
- 音色和 `resourceId` 不匹配
- API 返回了错误事件但没有音频块
- 文本被清洗后为空

处理：

```bash
tail -n 120 ~/.config/iSpeak/ispeak.log
```

然后用默认音色做最小测试：

```bash
ispeak "测试"
```

如果默认音色正常，单独测试来源音色：

```bash
ispeak-codex "测试"
ispeak-copilot "测试"
```

## Claude 或 Codex 不触发

确认 hook 配置文件存在。

Claude Code：

```bash
cat ~/.claude/settings.json
```

Codex：

```bash
cat ~/.codex/hooks.json
```

Codex 的 `~/.codex/hooks.json` 需要使用官方三层结构：`Stop` → matcher group → `hooks` handler。Codex 首次加载 hook 后需要在 `/hooks` 中信任。若信任后仍不触发，重启 Codex 或新开 thread。

手动测试 hook 提取：

```bash
printf '{"last_assistant_message":"hook 测试"}' \
  | ISPEAK_HOOK_PRINT_TEXT=1 bash ~/.config/iSpeak/hook-speak.sh codex
```

## Copilot 播上一条回复

原因：Copilot CLI 的 `agentStop` 可能早于最新 transcript 写入。iSpeak 会只读取最新 `user.message` 之后的 assistant，并通过 `~/.config/iSpeak/hook.last` 记录已播 assistant id，等待新 `assistant.message` 落盘。

确认安装的是新版 hook：

```bash
wc -c ~/.config/iSpeak/hook-speak.sh
rg -n "waitForFreshCopilotTranscriptText|extractLatestCopilotAssistant|hook.last" ~/.config/iSpeak/hook-speak.sh
```

如果刚更新过 hook，重启 Copilot CLI。

手动模拟：

```bash
tmpdir=$(mktemp -d)
cat > "$tmpdir/events.jsonl" <<'JSONL'
{"type":"assistant.message","data":{"content":"Copilot 当前回复"}}
JSONL

printf '{"transcriptPath":"%s/events.jsonl"}' "$tmpdir" \
  | ISPEAK_HOOK_PRINT_TEXT=1 bash ~/.config/iSpeak/hook-speak.sh copilot
```

## Copilot 完全不触发

确认配置：

```bash
cat ~/.copilot/hooks/ispeak-hook.json
```

应包含：

```json
{
  "version": 1,
  "hooks": {
    "agentStop": [
      {
        "type": "command",
        "bash": "bash $HOME/.config/iSpeak/hook-speak.sh copilot",
        "timeoutSec": 10
      }
    ]
  }
}
```

改动后重启 Copilot CLI。

## 播错音色

查看当前本机配置：

```bash
node - <<'NODE'
const fs = require('fs');
const cfg = JSON.parse(fs.readFileSync(process.env.HOME + '/.config/iSpeak/config.json', 'utf8'));
console.log(cfg.defaultVoice);
console.log(cfg.sourceVoices);
NODE
```

测试 wrapper 是否注入正确来源：

```bash
ISPEAK_CLI_PRINT_PAYLOAD=1 ispeak-copilot "测试"
```

应输出：

```text
{source:copilot}测试
```

## Hook 脚本本机版本不对

重新安装项目版本：

```bash
cd /Users/admin/iCode/iSpeak
make install
```

`make install` 会备份旧 hook：

```text
~/.config/iSpeak/hook-speak.sh.bak
```

然后覆盖安装新 hook。
