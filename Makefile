.PHONY: build install deploy

BIN     := build/ispeakd
BIN_DIR := $(HOME)/.local/bin
DST     := $(BIN_DIR)/ispeakd
PLIST   := $(HOME)/Library/LaunchAgents/com.iSpeak.plist
CONFIG  := $(HOME)/.config/iSpeak

build:
	mkdir -p build
	go build -o $(BIN) .

install: build
	@# 1) 停止旧服务
	launchctl unload $(PLIST) 2>/dev/null || true
	rm -f /tmp/ispeak.sock
	@# 2) 清理旧版本
	rm -f $(BIN_DIR)/iSpeak $(BIN_DIR)/ispeakd $(BIN_DIR)/ispeak $(BIN_DIR)/speak
	@# 3) 安装
	mkdir -p $(BIN_DIR)
	install -m 0755 $(BIN) $(DST)
	install -m 0755 $(CURDIR)/scripts/ispeak $(BIN_DIR)/ispeak
	sed 's|BINARY_PATH_PLACEHOLDER|$(DST)|' configs/com.iSpeak.plist > $(PLIST)
	@# 4) 启动
	launchctl load $(PLIST)
	@echo "iSpeak install complete"

deploy: install
	mkdir -p $(CONFIG)
	cp -n configs/config.example.json $(CONFIG)/config.json 2>/dev/null || true
	cp configs/hook-speak.sh $(CONFIG)/hook-speak.sh
	sed 's|BINARY_PATH_PLACEHOLDER|$(DST)|' configs/com.iSpeak.plist > $(PLIST)
	launchctl load $(PLIST)
	@echo "部署完成 — 编辑 $(CONFIG)/config.json 填入 TTS 凭证"
