<div align="center">

# Mitto

[![Tests](https://github.com/inercia/mitto/actions/workflows/tests.yml/badge.svg)](https://github.com/inercia/mitto/actions/workflows/tests.yml)
[![Release](https://github.com/inercia/mitto/actions/workflows/release.yml/badge.svg)](https://github.com/inercia/mitto/actions/workflows/release.yml)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**A modern client for AI coding agents**

CLI • Web Interface • Native macOS App

[Features](#features) • [Quick Start](#quick-start) • [Documentation](#documentation)

</div>

---

## Features

🤖 **Multi-Agent Support** — Connect to Claude Code, Auggie or any [ACP-compatible](https://agentcommunicationprotocol.dev/) agent

🖥️ **Three Interfaces** — Terminal CLI, web browser, and native macOS app

🖥️ **Mobile fiendly** — Connect from your mobile phone to the sessions in your laptop, and continue your work on the go. Support for gestures for switching between conversations and more.

💬 **Session Management** — Automatic conversation history with resume capability

🎨 **Rich Rendering** — Syntax-highlighted code blocks and Markdown support

⚡ **Streaming** — Real-time responses with live updates

🔒 **Permission Control** — Review and approve agent actions

<!-- Screenshots will go here -->

## Quick Start

### Install

```bash
git clone https://github.com/inercia/mitto.git
cd mitto
make build
```

### Configure

Create `~/.mittorc`:

```yaml
acp:
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
```

### Run

```bash
# Terminal
mitto cli

# Web browser
mitto web

# macOS app
make build-mac-app && open Mitto.app
```

## Documentation

| | |
|---|---|
| 📖 [Usage Guide](docs/usage.md) | Commands, flags, examples |
| ⚙️ [Configuration](docs/config.md) | ACP servers, settings |
| 🌐 [Web Interface](docs/config-web.md) | Auth, hooks, themes |
| 🍎 [macOS App](docs/config-mac.md) | Hotkeys, notifications |
| 🔧 [Development](docs/development.md) | Building, testing |
| 🏗️ [Architecture](docs/architecture.md) | Design internals |

## Requirements

- Go 1.24+
- macOS, Linux, or Windows
- An ACP-compatible agent ([Claude Code](https://github.com/zed-industries/claude-code-acp), [Auggie](https://augmentcode.com/), etc.)

## License

[MIT](LICENSE)
