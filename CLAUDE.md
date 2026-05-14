# CLAUDE.md

这个仓库的共享工程约定、命令、架构和配置，统一看 [AGENTS.md](/Users/admin/iCode/iSpeak/AGENTS.md)。

这里仅补充 Claude Code 相关的最小约定：

- `configs/hook-speak.sh` 是 Claude/Codex/Copilot CLI 共用 hook
- `{source:claude}` 会走 Claude 音色
- 四个手动来源入口：`ispeak-claude`、`ispeak-codex`、`ispeak-copilot`、`ispeak-pi`
- 其余行为与 [AGENTS.md](/Users/admin/iCode/iSpeak/AGENTS.md) 保持一致
