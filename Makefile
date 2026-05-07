.PHONY: build test pack push install deploy uninstall clean help

VERSION  := 1.6.3
TAG      := v$(VERSION)
BIN     := build/ispeakd
BIN_DIR := $(HOME)/.local/bin
DST     := $(BIN_DIR)/ispeakd
PLIST   := $(HOME)/Library/LaunchAgents/com.iSpeak.plist
CONFIG  := $(HOME)/.config/iSpeak
LOG     := $(HOME)/.config/iSpeak/ispeak.log

help:
	@echo "iSpeak $(VERSION)"
	@echo ""
	@echo "  make build      # 编译 ispeakd"
	@echo "  make test       # 运行 Go 测试、race 测试、构建、npm 打包预检"
	@echo "  make push       # 推送 GitHub tag 并发布 npm latest"
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
	@npm pack --dry-run

pack:
	@npm pack --dry-run

push: test
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "工作区不干净，请先提交或暂存改动"; \
		git status --short; \
		exit 1; \
	fi
	@if git rev-parse "$(TAG)" >/dev/null 2>&1; then \
		echo "tag 已存在: $(TAG)"; \
	else \
		git tag "$(TAG)"; \
	fi
	@if npm view @xdfnet/ispeak@$(VERSION) version >/dev/null 2>&1; then \
		echo "npm 版本已存在: @xdfnet/ispeak@$(VERSION)"; \
		exit 1; \
	fi
	@git push origin HEAD
	@git push origin "$(TAG)"
	@npm publish --access public
	@echo "已发布: @xdfnet/ispeak@$(VERSION) / $(TAG)"

install: build
	@# 停止旧服务
	@launchctl unload $(PLIST) 2>/dev/null || true
	@# 安装二进制和 CLI
	@mkdir -p $(BIN_DIR)
	@install -m 0755 $(BIN) $(DST)
	@install -m 0755 $(CURDIR)/scripts/ispeak $(BIN_DIR)/ispeak
	@ln -sf $(BIN_DIR)/ispeak $(BIN_DIR)/ispeak-claude
	@ln -sf $(BIN_DIR)/ispeak $(BIN_DIR)/ispeak-codex
	@# 部署配置文件（首次不覆盖已有）
	@mkdir -p $(CONFIG)
	@if [ ! -f $(CONFIG)/config.json ]; then \
		cp configs/config.example.json $(CONFIG)/config.json; \
		echo "配置文件已创建: $(CONFIG)/config.json"; \
	else \
		echo "配置文件已存在: $(CONFIG)/config.json"; \
	fi
	@# 部署 hook 脚本（首次不覆盖已有）
	@if [ ! -f $(CONFIG)/hook-speak.sh ]; then \
		cp configs/hook-speak.sh $(CONFIG)/hook-speak.sh; \
		chmod +x $(CONFIG)/hook-speak.sh; \
		echo "Hook 脚本已创建: $(CONFIG)/hook-speak.sh"; \
	else \
		echo "Hook 脚本已存在: $(CONFIG)/hook-speak.sh"; \
	fi
	@# 安装 launchd plist
	@sed 's|BINARY_PATH_PLACEHOLDER|$(DST)|' configs/com.iSpeak.plist > $(PLIST)
	@# 启动
	@launchctl load $(PLIST)
	@sleep 0.5
	@# 自检
	@$(BIN_DIR)/ispeak status && echo "" && echo "安装成功！" || { echo "安装失败，请检查日志: $(LOG)"; exit 1; }

deploy: install

uninstall:
	@echo "停止服务..."
	@launchctl unload $(PLIST) 2>/dev/null || true
	@rm -f $(PLIST)
	@echo "删除文件..."
	@rm -f $(BIN_DIR)/ispeakd $(BIN_DIR)/ispeak $(BIN_DIR)/ispeak-claude $(BIN_DIR)/ispeak-codex
	@echo "保留配置目录: $(CONFIG)"
	@echo "卸载完成"

clean:
	@rm -rf build
	@echo "清理完成"
