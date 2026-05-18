# 运维手册

本文记录 iSpeak 本机运行时的常用维护命令。

## 状态

```bash
ispeak status
```

输出示例：

```text
== iSpeak ==
  进程: 73524 ✓
  Socket: ✓
```

## 重启服务

```bash
ispeak restart
```

或直接使用 launchd：

```bash
launchctl unload ~/Library/LaunchAgents/com.ispeak.plist 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/com.ispeak.plist
```

## 日志

daemon 日志：

```bash
tail -n 120 ~/.config/iSpeak/ispeak.log
```

hook 日志：

```bash
tail -n 120 ~/.config/iSpeak/hook.log 2>/dev/null || true
```

日志轮转配置在 `main.go` 中：

- 单文件最大 10MB
- 保留 3 份
- 压缩归档

## 手动播报

```bash
ispeak "默认音色"
ispeak-claude "Claude 音色"
ispeak-codex "Codex 音色"
ispeak-copilot "Copilot 音色"
ispeak-pi "Pi 音色"
```

只看 payload，不发 socket：

```bash
ISPEAK_CLI_PRINT_PAYLOAD=1 ispeak-copilot "测试"
```

## 安装与重新部署

```bash
make install
```

会执行：

- 编译 `build/ispeakd`
- 安装 `~/.local/bin/ispeakd`
- 安装五个 CLI 入口
- 首次创建 `~/.config/iSpeak/config.json`
- 覆盖安装 hook，旧 hook 不同时备份为 `.bak`
- 部署 Pi Extension
- 写入 launchd plist
- 启动并自检

## 卸载

```bash
make uninstall
```

会删除：

- `~/Library/LaunchAgents/com.ispeak.plist`
- `~/.local/bin/ispeakd`
- `~/.local/bin/ispeak`
- `~/.local/bin/ispeak-claude`
- `~/.local/bin/ispeak-codex`
- `~/.local/bin/ispeak-copilot`
- `~/.local/bin/ispeak-pi`

会保留：

```text
~/.config/iSpeak/
```

## 清理构建产物

```bash
make clean
```

会删除：

```text
build/
```

`.gitignore` 已忽略：

```text
build/
iSpeak
xdfnet-ispeak-*.tgz
.DS_Store
```

## 更新本机配置

编辑：

```bash
open ~/.config/iSpeak/config.json
```

配置热更新，无需重启服务。下一次 socket 连接会加载新配置。

如果修改 hook 脚本或 launchd plist，则需要重新安装或重启对应客户端。

## 发布后本机升级

如果通过 npm 使用：

```bash
npm i -g @xdfnet/ispeak@latest
```

如果通过源码使用：

```bash
git pull
make install
```
