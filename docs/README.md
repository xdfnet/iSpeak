# iSpeak 文档索引

这里放项目维护和排障文档。README 只保留快速上手，细节看对应专题。

## 使用与集成

- [安装与集成](install-and-integration.md)：npm/source 安装，以及 Claude Code、Codex、Copilot CLI、Pi 的接入方式。
- [配置说明](configuration.md)：`config.json` 字段、默认音色、来源音色、热更新与初始化配置。
- [排障手册](troubleshooting.md)：听不到声音、hook 不触发、Copilot 播上一条、TTS 报错等问题。
- [运维手册](operations.md)：服务状态、重启、日志、卸载、清理与本机验证。

## 开发与发布

- [架构文档](architecture.md)：daemon、socket、Player、SSE、AVAudioEngine、Hook 链路。
- [Hook 文本提取链路](hook-text-extraction.md)：Claude/Codex/Copilot Hook 输入和文本提取策略。
- [发布流程](release.md)：版本号、测试、提交、tag、npm publish、回滚注意事项。

## 火山引擎资料

- [豆包/火山引擎资料索引](doubao/README.md)
- [SSE/HTTP Chunked TTS API](doubao/http-chunked-sse-unidirectional-v3.md)
- [音色复刻 API](doubao/tts-voice-clone-api-v3.md)
- [在线音色列表](doubao/voice-list.md)
