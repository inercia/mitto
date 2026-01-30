# Mitto Documentation

This directory contains documentation for Mitto, a CLI client for the Agent Communication Protocol (ACP).

## Documentation Index

| Document | Description |
|----------|-------------|
| [usage.md](usage.md) | Commands, flags, and usage examples |
| [config.md](config.md) | Configuration overview and main settings |
| [config-acp.md](config-acp.md) | ACP server setup (Claude, Auggie, Copilot) |
| [config-web.md](config-web.md) | Web server, authentication, hooks, and security |
| [config-mac.md](config-mac.md) | macOS desktop app (building, hotkeys, notifications) |
| [development.md](development.md) | Building, testing, and contributing |
| [architecture.md](architecture.md) | System architecture, component design, and internal structure |

## Quick Links

### Getting Started

- See the main [README.md](../README.md) for installation and basic usage
- See [sample.mittorc](../sample.mittorc) for a complete configuration example

### Configuration

The [config.md](config.md) document provides an overview of all configuration options:

- ACP server configuration
- Links to detailed configuration sections

### ACP Servers

The [config-acp.md](config-acp.md) document covers:

- Claude Code setup and configuration
- Auggie setup and features
- GitHub Copilot ACP (public preview)

### Web Interface

The [config-web.md](config-web.md) document covers:

- Starting and configuring the web server
- Authentication (username/password, IP allowlist, rate limiting)
- Lifecycle hooks with examples for:
  - [ngrok](config-web.md#using-ngrok)
  - [Tailscale Funnel](config-web.md#using-tailscale-funnel)
  - [Cloudflare Tunnel](config-web.md#using-cloudflare-tunnel)
- Security settings (trusted proxies, origin validation, rate limiting)
- Themes and predefined prompts
- Reverse proxy setup (nginx, Caddy)
- Multi-workspace support

### macOS Desktop App

The [config-mac.md](config-mac.md) document covers:

- Building and running the app
- Environment variable overrides
- Global hotkeys (show/hide app)
- Notification sounds (agent completed)
- Settings dialog UI tab

### Architecture

The [architecture.md](architecture.md) document covers:

- Package structure and responsibilities
- Session management and persistence
- ACP protocol integration
- Background sessions and WebSocket handling
- Frontend component structure

### Development

The [development.md](development.md) document covers:

- Building CLI, web server, and macOS app
- Running tests
- Project structure
- Contributing guidelines

## Contributing

When adding new features, please update the relevant documentation:

1. Update existing docs if modifying a feature
2. Add new docs for major new features
3. Update this README.md to include new documents

