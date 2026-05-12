.PHONY: build test pack release push install deploy uninstall clean help

VERSION  := $(shell grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' package.json | head -1 | sed 's/.*: *"\(.*\)"/\1/')
TAG      := v$(VERSION)
NPM_PKG  := @xdfnet/ispeak
BIN     := build/ispeakd
BIN_DIR := $(HOME)/.local/bin
DST     := $(BIN_DIR)/ispeakd
PLIST   := $(HOME)/Library/LaunchAgents/com.ispeak.plist
LEGACY_PLIST := $(HOME)/Library/LaunchAgents/com.iSpeak.plist
CONFIG  := $(HOME)/.config/iSpeak
LOG     := $(HOME)/.config/iSpeak/ispeak.log
CLI_SRC := scripts/ispeak
HOOK_SRC := configs/hook-speak.sh
PI_EXT_SRC := configs/ispeak.ts
PLIST_SRC := configs/com.ispeak.plist
CONFIG_SRC := configs/config.example.json

help:
	@echo "iSpeak $(VERSION)"
	@echo ""
	@echo "  make build      # 编译 ispeakd"
	@echo "  make test       # 运行 Go 测试、race 测试、构建、npm 打包预检"
	@echo "  make release    # 推送 GitHub tag 并发布 npm latest"
	@echo "  make push       # 同 release"
	@echo "  make install    # 安装并启动服务（首次运行会部署配置和 hook）"
	@echo "  make deploy     # 同 install"
	@echo "  make uninstall  # 卸载（停止服务 + 删除文件）"
	@echo "  make clean      # 清理编译产物"

build:
	@mkdir -p build
	@go build -ldflags="-s -w" -o $(BIN) .
	@echo "编译完成: $(BIN)"

test:
	@go test -count=1 ./...
	@go test -race -count=1 ./...
	@go build ./...
	@bash scripts/test-hook-speak.sh
	@npm pack --dry-run

pack:
	@npm pack --dry-run

release: test
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "工作区不干净，请先提交或暂存改动"; \
		git status --short; \
		exit 1; \
	fi
	@if npm view $(NPM_PKG)@$(VERSION) version >/dev/null 2>&1; then \
		echo "npm 版本已存在: $(NPM_PKG)@$(VERSION)"; \
		exit 1; \
	fi
	@if git rev-parse "$(TAG)" >/dev/null 2>&1; then \
		echo "tag 已存在: $(TAG)"; \
	else \
		git tag "$(TAG)"; \
	fi
	@git push origin HEAD
	@git push origin "$(TAG)"
	@npm publish --access public
	@echo "已发布: $(NPM_PKG)@$(VERSION) / $(TAG)"

push: release

install: build
	@# 停止旧服务
	@launchctl unload $(LEGACY_PLIST) 2>/dev/null || true
	@launchctl unload $(PLIST) 2>/dev/null || true
	@rm -f $(LEGACY_PLIST)
	@# 安装二进制和 CLI
	@mkdir -p $(BIN_DIR)
	@install -m 0755 $(BIN) $(DST)
	@install -m 0755 $(CURDIR)/$(CLI_SRC) $(BIN_DIR)/ispeak
	@# 部署配置文件（首次不覆盖已有）
	@mkdir -p $(CONFIG)
	@if [ ! -f $(CONFIG)/config.json ]; then \
		cp $(CONFIG_SRC) $(CONFIG)/config.json; \
		echo "配置文件已创建: $(CONFIG)/config.json"; \
	else \
		echo "配置文件已存在: $(CONFIG)/config.json"; \
	fi
	@if grep -q '"endpoint"[[:space:]]*:[[:space:]]*"https://openspeech.bytedance.com/api/v3/tts/unidirectional"' $(CONFIG)/config.json; then \
		cp $(CONFIG)/config.json $(CONFIG)/config.json.bak; \
		perl -pi -e 's|"https://openspeech.bytedance.com/api/v3/tts/unidirectional"|"https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse"|g' $(CONFIG)/config.json; \
		echo "配置 endpoint 已迁移到 SSE，旧配置备份: $(CONFIG)/config.json.bak"; \
	fi
	@# 部署 hook 脚本（覆盖安装；如有本地改动先备份）
	@if [ -f $(CONFIG)/hook-speak.sh ] && ! cmp -s $(HOOK_SRC) $(CONFIG)/hook-speak.sh; then \
		cp $(CONFIG)/hook-speak.sh $(CONFIG)/hook-speak.sh.bak; \
		echo "旧 Hook 已备份: $(CONFIG)/hook-speak.sh.bak"; \
	fi
	@cp $(HOOK_SRC) $(CONFIG)/hook-speak.sh
	@chmod +x $(CONFIG)/hook-speak.sh
	@echo "Hook 脚本已安装: $(CONFIG)/hook-speak.sh"
	@# 部署 Pi Extension
	@cp $(PI_EXT_SRC) $(CONFIG)/ispeak.ts
	@echo "Pi Extension 已安装: $(CONFIG)/ispeak.ts"
	@# 安装 launchd plist
	@sed 's|BINARY_PATH_PLACEHOLDER|$(DST)|' $(PLIST_SRC) > $(PLIST)
	@# 启动
	@launchctl load $(PLIST)
	@sleep 0.5
	@# 自检
	@$(BIN_DIR)/ispeak status && echo "" && echo "安装成功！" || { echo "安装失败，请检查日志: $(LOG)"; exit 1; }

deploy: install

uninstall:
	@echo "停止服务..."
	@launchctl unload $(LEGACY_PLIST) 2>/dev/null || true
	@launchctl unload $(PLIST) 2>/dev/null || true
	@rm -f $(LEGACY_PLIST)
	@rm -f $(PLIST)
	@echo "删除文件..."
	@rm -f $(BIN_DIR)/ispeakd $(BIN_DIR)/ispeak
	@echo "保留配置目录: $(CONFIG)"
	@echo "卸载完成"

clean:
	@rm -rf build
	@echo "清理完成"
