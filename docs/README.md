# Mitto Documentation

This directory contains documentation for Mitto, a CLI client for the Agent
Communication Protocol (ACP).

## Documentation Index

- [Commands, flags, and usage examples](usage.md)
- [Configuration documentation](config/README.md)
- [Building, testing, and contributing](development.md)
- [Architecture and developer documentation](devel/README.md)

## Quick Links

### Getting Started

- See the main [README.md](../README.md) for installation and basic usage
- Run `mitto config create` to create a default configuration file (`~/.mittorc`)

### Configuration

[This directory](config/README.md) contains all configuration documentation:

- [Overview](config/overview.md) - Configuration file locations and formats
- [ACP Servers](config/acp.md) - Claude Code, Auggie, GitHub Copilot setup
- [Web Interface](config/web.md) - Authentication, hooks, security, themes
- [macOS App](config/mac.md) - Hotkeys, notifications, desktop app
- [Workspaces](config/workspace.md) - Project-specific `.mittorc` files
- [Conversations](config/conversations.md) - Message processing rules
- [Hooks](config/hooks.md) - External command-based message hooks

### Architecture

[This directory](devel/README.md) contains developer documentation:

- [Architecture](devel/architecture.md) - System design and components
- [Session Management](devel/session-management.md) - Recording, playback, queues
- [Web Interface](devel/web-interface.md) - Streaming, mobile support
- [WebSocket Messaging](devel/websocket-messaging.md) - Protocol, sync, reconnection
- [Workspaces](devel/workspaces.md) - Multi-workspace architecture

### Development

The [development document](development.md) covers:

- Building CLI, web server, and macOS app
- Running tests
- Project structure
- Contributing guidelines

## Contributing

When adding new features, please update the relevant documentation:

1. Update existing docs if modifying a feature
2. Add new docs for major new features
3. Update this README.md to include new documents
