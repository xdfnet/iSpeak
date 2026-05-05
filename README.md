# iSpeak

A lightweight local TTS service that converts text to speech via ByteDance Volcano Engine and plays audio sequentially. Built for AI coding assistants (Claude Code, Codex) to vocalize their responses.

## Features

- **Cost-saving interruption**: New messages cancel in-flight TTS synthesis and interrupt playback — pay only for what you actually hear
- **Sequential playback**: Global sequence numbers ensure audio plays in order, never overlapping
- **Hot config reload**: Configuration changes take effect immediately without restarting the service
- **Multi-voice support**: Different voices for different sources (e.g., Claude uses one voice, Codex uses another)
- **Unix Socket communication**: Minimal dependencies, works reliably on macOS

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  ispeak / ispeak-claude / ispeak-codex (CLI)       │
│         nc -U /tmp/ispeak.sock                      │
└─────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────┐
│  ispeakd (Daemon)                                  │
│  ┌─────────────────────────────────────────────┐   │
│  │  TTS Context Manager                         │   │
│  │  (single in-flight request, cancel on new)   │   │
│  └─────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────┐   │
│  │  Playback Worker                             │   │
│  │  (sequential by seq#, buffered reorder)     │   │
│  └─────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
                          │
                          ▼
              ┌───────────────────┐
              │  afplay (macOS)   │
              └───────────────────┘
```

## Quick Start

```bash
# Clone and install
git clone https://github.com/yourname/ispeak.git
cd ispeak
make install

# Verify
ispeak status
ispeak test "Hello, world"
```

## Usage

```bash
ispeak "Task completed"           # Speak with default voice
ispeak test                       # Self-test
ispeak test "Custom message"      # Self-test with custom text
ispeak status                     # Check service and socket status
ispeak restart                    # Restart the daemon
ispeak recover                    # Restart + status + test
ispeak logs 80                   # View last 80 log lines
ispeak tail                       # Live log stream
```

### Voice Selection

```bash
ispeak "message"           # Default voice
ispeak-claude "message"    # Claude's dedicated voice
ispeak-codex "message"     # Codex's dedicated voice
```

## Configuration

Create `~/.config/iSpeak/config.json`:

```json
{
  "apiKey": "your-volcengine-api-key",
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

**Get an API key**: Sign up at [Volcengine Console](https://console.volcengine.com/tts) and create a TTS instance.

## Claude Code / Codex Integration

Add a Stop hook to your AI assistant's settings:

**Claude Code** (`~/.claude/settings.json`):
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

**Codex** (`~/.codex/hooks.json`):
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

## Development

```bash
make build    # Compile the daemon
make install  # Install to ~/.local/bin and start service
make deploy   # Full deployment (install + config files)
```

## File Locations

| Path | Description |
|------|-------------|
| `~/Library/LaunchAgents/com.iSpeak.plist` | macOS launchd service |
| `/tmp/ispeak.sock` | Unix domain socket |
| `/tmp/iSpeak.log` | Log output |
| `~/.config/iSpeak/config.json` | Configuration |
| `~/.config/iSpeak/hook-speak.sh` | Claude/Codex hook script |
| `~/.local/bin/ispeakd` | Daemon binary |
| `~/.local/bin/ispeak*` | CLI symlinks |

## License

MIT
