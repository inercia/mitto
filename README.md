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

🤖 **Agents & Workspaces**

- **Multi-Agent Support** — Connect to Claude Code, Copilot CLI, Auggie or any [ACP-compatible](https://agentcommunicationprotocol.dev/) agent
- **Multi-Workspace Support** — Configure multiple ACP agents and workspaces, each with their own settings, prompts, and processors

💬 **Conversations**

- **Session Management** — Automatic conversation history with resume capability
- **Parent/Children Conversations** — Spawn child conversations to delegate work to faster/cheaper models, wait for results, and synthesize — enabling multi-agent workflows
- **Periodic Conversations** — Schedule recurring prompts (every N minutes/hours/days) for automated tasks like daily reports or periodic checks, with HTTP callback URLs for on-demand triggering from webhooks, cron jobs, or CI pipelines
- **Message Queue** — Queue messages while the agent is busy, with auto-generated titles and automatic delivery when the agent becomes idle

🖥️ **User Interface**

- **Multiple Interfaces** — Native macOS app and Web Browser
- **Rich Rendering** — Syntax-highlighted code blocks and Markdown support
- **Streaming** — Real-time responses with live updates
- **Keyboard Shortcuts** — Create, delete, or navigate between conversations
- **Slash Commands** — Type `/` to access quick commands provided by the agent (cancel, web search, etc.)
- **Follow-up Suggestions** — AI-generated action buttons appear after agent responses, enabling one-tap continuation especially useful on mobile

🔧 **Customization & Extensibility**

- **Quick Actions & Pre-defined Prompts** — Customizable prompt buttons (per-project or global) with conditional visibility rules using CEL expressions. Built-in prompts for common workflows like "Run Tests", "Commit Changes", "Code Review", and more
- **Message Processors & Hooks** — Transform messages with declarative text injection or external command processors. Auto-prepend system prompts, append reminders, or pipe messages through custom scripts
- **MCP Server** — Built-in Model Context Protocol server for cross-conversation automation, interactive UI prompts, and agent-to-agent orchestration

📎 **Input**

- **Image & File Uploads** — Attach images (PNG, JPEG, GIF, WebP) and files to your conversations via paste, drag-and-drop, or file picker
- **Permission Control** — Review and approve agent actions

🌐 **[External Access](docs/config/ext-access.md)** — Access your conversations from anywhere

- **Mobile Friendly** — Connect from your phone or tablet with touchscreen gestures for switching between conversations
- **Auto-Tunnels** — Automatic tunnel setup/teardown (Cloudflare, ngrok, or custom) — no manual port forwarding needed
- **Security** — Scanner defense with rate limiting and IP blocking, restricted runners for sandboxed agent execution, authentication for remote access

## Quick Start

### Install

#### Homebrew (macOS and Linux)

```bash
brew tap inercia/mitto
brew install mitto              # CLI only
brew install --cask mitto-app   # macOS app (includes CLI)
```

#### Manual Download

Download the latest release from the [releases page](https://github.com/inercia/mitto/releases):

- **macOS**: Download `Mitto-darwin-*.dmg` for the native app, or `mitto-darwin-*.tar.gz` for CLI only
- **Linux**: Download `mitto-linux-*.tar.gz`

#### Build from Source

Requires Go 1.24+ and (for the macOS app) Xcode Command Line Tools.

```bash
# CLI only
make build
./mitto web

# macOS app (includes CLI)
make build-mac-app
open Mitto.app
# Or run directly: ./Mitto.app/Contents/MacOS/mitto-app
```

### Configure

- For **Mac OS**, just open the Mitto application from your `/Applications` folder and follow the instructions.

- For **Linux**, follow the instructions [here](docs/config/web/README.md).

## Documentation

- 📖 [Usage Guide](docs/usage.md): Commands, flags, examples
- ⚙️ [Configuration](docs/config/README.md): ACP servers, settings
  - 🌐 [Linux/Web Interface](docs/config/web/README.md): Auth, hooks, themes, security
  - 🍎 [macOS App](docs/config/mac/README.md): Hotkeys, notifications
- 🔧 [Development](docs/development.md): Building, testing
- 🏗️ [Architecture](docs/devel/README.md): Design internals

## Requirements

- macOS, Linux
- An ACP-compatible agent ([Claude Code](https://github.com/zed-industries/claude-code-acp), [Auggie](https://augmentcode.com/), etc.)

## License

[MIT](LICENSE)

## Disclaimer

This software is provided "as is", without warranty of any kind, express or implied. In no event shall the authors or copyright holders be liable for any claim, damages, or other liability arising from the use of this software. Use at your own risk.
