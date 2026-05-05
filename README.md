# iSpeak

**iSpeak** turns your AI coding assistant's responses into voice — so you can listen while you code instead of staring at the screen.

Built for developers who keep Claude Code or Codex running in the background. When your AI finishes a task, it speaks the result. When you send a new message, it automatically cancels the old one — no wasted API calls, no interruption lag.

## What It Sounds Like

```
# Default: warm female voice
ispeak "Pull request merged, 3 tests passed"

# Claude mode: different voice for Claude Code
ispeak-claude "Code review complete, 2 suggestions"

# Codex mode: yet another voice
ispeak-codex "Build finished in 12 seconds"
```

## Why iSpeak

| Problem | Solution |
|---------|----------|
| TTS bills add up when AI generates multiple responses | New message cancels in-flight TTS — pay only for the final one |
| Audio plays out of order when responses arrive at different speeds | Global sequence numbers guarantee sequential playback |
| Config changes require daemon restart | Hot reload — edit `config.json`, changes take effect immediately |
| Generic voice gets boring | Per-source voices — Claude and Codex each sound distinct |

## Get Started

```bash
# 1. Install
git clone https://github.com/yourname/ispeak.git && cd ispeak && make install

# 2. Configure
# Edit ~/.config/iSpeak/config.json with your Volcengine API key
# Get one free at https://console.volcengine.com/tts

# 3. Verify
ispeak status
ispeak test "iSpeak is ready"

# 4. Connect to Claude Code or Codex
# (add the Stop hook — see Integration section below)
```

## How It Works

```
You: "refactor the auth module"
        │
        ▼
┌─────────────────────────────────────────────────────┐
│  ispeakd — single daemon on your Mac               │
│                                                       │
│   Receives text via Unix Socket                    │
│         │                                            │
│         ▼                                           │
│   TTS Context Manager                              │
│   (cancels previous request on new message)        │
│         │                                            │
│         ▼                                           │
│   Playback Worker                                  │
│   (sequential by seq#, buffers out-of-order)        │
│         │                                            │
│         ▼                                           │
│   afplay → your speakers/headphones                │
└─────────────────────────────────────────────────────┘
```

## All Commands

```bash
ispeak "message"           # Speak with default voice
ispeak test               # Self-test with default message
ispeak test "hi"          # Self-test with custom message
ispeak status             # Check daemon, socket, voice config
ispeak restart            # Restart daemon
ispeak recover           # Restart + status check + test
ispeak logs 80          # Tail last 80 log lines
ispeak tail              # Live log stream
```

Voice-specific shortcuts (symlinks to `ispeak`):
```bash
ispeak-claude "msg"      # Claude's dedicated voice
ispeak-codex "msg"       # Codex's dedicated voice
```

## Configuration

`~/.config/iSpeak/config.json`:

```json
{
  "apiKey": "volcengine-api-key",
  "endpoint": "https://openspeech.bytedance.com/api/v3/tts/unidirectional",
  "defaultVoice": {
    "voice_type": "zh_female_mizai_uranus_bigtts",
    "resourceId": "seed-tts-2.0"
  },
  "sourceVoices": {
    "claude": {
      "voice_type": "zh_female_tianmeitaozi_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    },
    "codex": {
      "voice_type": "zh_male_shaonianzixin_uranus_bigtts",
      "resourceId": "seed-tts-2.0"
    }
  }
}
```

Browse available voices at [Volcengine TTS](https://console.volcengine.com/tts). Any voice with `voice_type` and `resourceId` from their catalog works.

## Integration

### Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash $HOME/.config/iSpeak/hook-speak.sh claude",
        "timeout": 30
      }]
    }]
  }
}
```

### Codex

Add to `~/.codex/hooks.json`:

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "bash $HOME/.config/iSpeak/hook-speak.sh codex",
        "timeout": 30
      }]
    }]
  }
}
```

## Developer Commands

```bash
make build    # Compile ispeakd
make install  # Install to ~/.local/bin and start launchd service
make deploy   # Full deployment (install + copy config examples)
```

## File Locations

| File | Purpose |
|------|---------|
| `~/Library/LaunchAgents/com.iSpeak.plist` | macOS auto-start service |
| `/tmp/ispeak.sock` | Daemon listens here |
| `/tmp/iSpeak.log` | All logs |
| `~/.config/iSpeak/config.json` | Your API key and voices |
| `~/.config/iSpeak/hook-speak.sh` | Claude/Codex hook script |

## License

MIT — use it freely, change it freely, break it freely.
