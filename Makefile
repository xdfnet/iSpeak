.PHONY: build install uninstall deploy start stop restart clean log

BIN    := iSpeak
DST    := /usr/local/bin/$(BIN)
PLIST  := $(HOME)/Library/LaunchAgents/com.iSpeak.plist
CONFIG := $(HOME)/.config/iSpeak

build:
	go build -o $(BIN) .

install: build
	sudo cp $(BIN) $(DST)
	sudo ln -sf $(PWD)/speak /usr/local/bin/speak

uninstall:
	sudo rm -f $(DST) /usr/local/bin/speak
	launchctl unload $(PLIST) 2>/dev/null || true
	rm -f $(PLIST)

deploy: install
	mkdir -p $(CONFIG)
	cp -n configs/config.example.json $(CONFIG)/config.json 2>/dev/null || true
	cp configs/hook-speak.sh $(CONFIG)/hook-speak.sh
	cp configs/com.iSpeak.plist $(PLIST)
	launchctl load $(PLIST)
	@echo "部署完成 — 编辑 $(CONFIG)/config.json 填入 TTS 凭证"

start:
	launchctl load $(PLIST)

stop:
	launchctl unload $(PLIST)

restart: stop start

clean:
	rm -f $(BIN)

log:
	tail -f /tmp/iSpeak.log
