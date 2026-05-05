.PHONY: build install deploy uninstall clean help

VERSION  := 1.3.0
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
	@echo "  make install    # 安装并启动服务（首次运行会部署配置和 hook）"
	@echo "  make deploy     # 同 install"
	@echo "  make uninstall  # 卸载（停止服务 + 删除文件）"
	@echo "  make clean      # 清理编译产物"

build:
	@mkdir -p build
	@go build -ldflags="-s -w" -o $(BIN) .
	@echo "编译完成: $(BIN)"

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
