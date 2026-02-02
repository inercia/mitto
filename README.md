<div align="center">

<img src="icon.png" alt="Mitto" width="128" height="128">

# Mitto

[![Tests](https://github.com/inercia/mitto/actions/workflows/tests.yml/badge.svg)](https://github.com/inercia/mitto/actions/workflows/tests.yml)
[![Release](https://github.com/inercia/mitto/actions/workflows/release.yml/badge.svg)](https://github.com/inercia/mitto/actions/workflows/release.yml)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**A modern interface for your team of AI coding agents**

CLI • Web Interface • Native macOS App

[Features](#features) • [Quick Start](#quick-start) • [Documentation](#documentation)

</div>

---

## Use case

So you have installed some ACP compatible agents, like Claude Code, or Copilot CLI, or Auggie, or any
other AI coding agent, and you want to have multiple instances running at the same time, each one in
its own workspace (ie, folder)

<img align="center" src="docs/videos/desktop.gif" alt="Mitto" width="1024"/>

but you also want to be able to continue your work from your browser, or go for a coffee and
continue talking to your agents from your phone or from your tablet, and you want to be able
to switch between them easily, and you want to be able to do it all without having to
install yet another AI coding agent...

<img align="center" src="docs/videos/mobile.gif" alt="Mitto" width="480" />

---

## Features

🤖 **Multi-Agent Support** — Connect to Claude Code, Copilot CLI, Auggie or any [ACP-compatible](https://agentcommunicationprotocol.dev/) agent

🖥️ **Multiple Interfaces** — Native macOS app and Web Browser

🖥️ **Mobile fiendly** — Connect from your mobile phone to the sessions in your laptop, and [continue your work on the go](docs/config/ext-access.md). Support touchscreen gestures for switching between conversations and more.

💬 **Session Management** — Automatic conversation history with resume capability

🎨 **Rich Rendering** — Syntax-highlighted code blocks and Markdown support

⚡ **Streaming** — Real-time responses with live updates

🔒 **Permission Control** — Review and approve agent actions

🖥️ **Keyboard shortcuts** — Use keyboard shortcuts to create, delete or navigate between conversations.

## Quick Start

### Install

For Mac OS, install the application from the [releases page](https://github.com/inercia/mitto/releases).

For Linux, install the binary from the [releases page](https://github.com/inercia/mitto/releases).

### Configure

- For **Mac OS**, just open the Mitto application and follow the instructions.

- For **Linux**, follow the instructions [here](docs/config/web/README.md).

## Documentation

| | |
|---|---|
| 📖 [Usage Guide](docs/usage.md) | Commands, flags, examples |
| ⚙️ [Configuration](docs/config/README.md) | ACP servers, settings |
| 🌐 [Web Interface](docs/config/web/README.md) | Auth, hooks, themes, security |
| 🍎 [macOS App](docs/config/mac/README.md) | Hotkeys, notifications |
| 🔧 [Development](docs/development.md) | Building, testing |
| 🏗️ [Architecture](docs/devel/README.md) | Design internals |

## Requirements

- macOS, Linux
- An ACP-compatible agent ([Claude Code](https://github.com/zed-industries/claude-code-acp), [Auggie](https://augmentcode.com/), etc.)

## License

[MIT](LICENSE)
