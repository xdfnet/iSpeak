.PHONY: build install deploy

BIN    := build/ispeakd
DST    := /usr/local/bin/ispeakd
PLIST  := $(HOME)/Library/LaunchAgents/com.iSpeak.plist
CONFIG := $(HOME)/.config/iSpeak

build:
	mkdir -p build
	go build -o $(BIN) .

install: build
	@# 1) 停止
	@if [ -n "$$SUDO_USER" ]; then \
		sudo -u "$$SUDO_USER" launchctl unload $(PLIST) 2>/dev/null || true; \
	else \
		launchctl unload $(PLIST) 2>/dev/null || true; \
	fi
	rm -f /tmp/ispeak.sock
	@# 2) 卸载（清理旧安装）
	sudo rm -f /usr/local/bin/iSpeak /usr/local/bin/ispeakd /usr/local/bin/ispeak /usr/local/bin/speak
	@# 3) 安装
	sudo install -m 0755 $(BIN) /usr/local/bin/ispeakd
	sudo install -m 0755 $(CURDIR)/scripts/ispeak /usr/local/bin/ispeak
	cp configs/com.iSpeak.plist $(PLIST)
	@# 4) 启动
	@if [ -n "$$SUDO_USER" ]; then \
		sudo -u "$$SUDO_USER" launchctl load $(PLIST); \
	else \
		launchctl load $(PLIST); \
	fi
	@echo "iSpeak install complete"

deploy: install
	mkdir -p $(CONFIG)
	cp -n configs/config.example.json $(CONFIG)/config.json 2>/dev/null || true
	cp configs/hook-speak.sh $(CONFIG)/hook-speak.sh
	cp configs/com.iSpeak.plist $(PLIST)
	@if [ -n "$$SUDO_USER" ]; then \
		sudo -u "$$SUDO_USER" launchctl load $(PLIST); \
	else \
		launchctl load $(PLIST); \
	fi
	@echo "部署完成 — 编辑 $(CONFIG)/config.json 填入 TTS 凭证"
