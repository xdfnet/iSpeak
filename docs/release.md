# 发布流程

iSpeak 通过 `make release` 发布 Git tag 和 npm 包。

## 发布前检查

确认当前版本：

```bash
node -p "require('./package.json').version"
npm view @xdfnet/ispeak version
```

确认工作区：

```bash
git status --short --branch
```

运行完整测试：

```bash
make test
```

`make test` 会执行：

- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go build ./...`
- `bash scripts/test-hook-speak.sh`
- `npm pack --dry-run`

## 改版本号

唯一版本源是：

```text
package.json
```

同时更新 README badge：

```text
![Version](https://img.shields.io/badge/version-x.y.z-blue)
```

## 提交

```bash
git add -A
git commit -m "release: vx.y.z — <简述>"
```

发布提交通常包含：

- 版本号
- README badge
- 源码改动
- 文档改动
- 初始化配置改动

## 发布

```bash
make release
```

`make release` 会：

1. 运行 `make test`
2. 检查工作区是否干净
3. 检查 npm 是否已有该版本
4. 创建 `vX.Y.Z` tag
5. 推送当前分支
6. 推送 tag
7. 执行 `npm publish --access public`

## 验证发布

```bash
npm view @xdfnet/ispeak version
git tag --points-at HEAD
git status --short --branch
```

预期：

- npm 版本等于 `package.json`
- HEAD 上有对应 `vX.Y.Z` tag
- 工作区干净

## 失败处理

如果 npm 版本已存在：

- 不要覆盖旧版本
- 升 patch 版本后重新提交发布

如果 tag 已推送但 npm publish 失败：

- 确认失败原因
- 如果代码无需变化，可修复 npm 登录/权限后手动执行 `npm publish --access public`
- 如果需要代码变化，升新版本发布，不复用已推送 tag

如果 Git push 成功但 npm 未发布：

```bash
npm view @xdfnet/ispeak@x.y.z version
```

确认 npm 是否缺失该版本，再决定手动 publish 或发新 patch。

## npm 包内容

发布前确认 dry-run 包含关键文件：

```bash
npm pack --dry-run
```

必须包含：

- `scripts/ispeak`
- `scripts/ispeak-claude`
- `scripts/ispeak-codex`
- `scripts/ispeak-copilot`
- `scripts/ispeak-pi`
- `configs/hook-speak.sh`
- `configs/ispeak.ts`
- `configs/com.ispeak.plist`
- `configs/config.example.json`
- `docs/`
