# Web Interface Configuration

> **Note:** This documentation applies to the **Web Interface** (which runs on any platform: Linux, macOS, Windows). For macOS Desktop App-specific configuration (hotkeys, notifications), see [macOS App Configuration](../mac/README.md).

## Overview

The web interface provides a browser-based UI that works identically on Linux, macOS, or any other platform.
This document covers web server settings, authentication, security, and deployment options.

## Table of Contents

- [Basic Configuration](#basic-configuration)
- [Starting the Web Server](#starting-the-web-server)
- [Predefined Prompts](#predefined-prompts)
- [Authentication](#authentication)
- [Security Configuration](#security-configuration)
- [Lifecycle Hooks](#lifecycle-hooks)
- [Multi-Workspace Support](#multi-workspace-support)
- [Reverse Proxy Setup](#reverse-proxy-setup)
- [Complete Example](#complete-example)

## Related Documentation

| Topic                   | Location                             |
| ----------------------- | ------------------------------------ |
| External Access         | [../ext-access.md](../ext-access.md) |
| ACP Servers             | [acp.md](acp.md)                     |
| Workspace Config        | [workspace.md](workspace.md)         |
| Configuration Overview  | [../overview.md](../overview.md)     |
| Prompts & Quick Actions | [../prompts.md](../prompts.md)       |
| Message Hooks           | [../hooks.md](../hooks.md)           |
| macOS App               | [../mac/README.md](../mac/README.md) |

---

## Basic Configuration

Start by creating a `~/.mittorc` like:

```yaml
acp:
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest

web:
  host: 127.0.0.1 # Server host (default: 127.0.0.1)
  port: 8080 # Server port (default: 8080)
  theme: v2 # UI theme: "default" or "v2"
```

and then start the Web server with:

```bash
# Start with default settings in a specific directory
mitto web --dir /some/directory

# Specify a different port
mitto web --port 3000  --dir /some/directory

# Create multiple workspaces
mitto web  --dir /some/directory-a  --dir /some/directory-b
```

and then connect to `http://localhost:8080` in your browser.

## Multi-Workspace Support

Configure multiple workspaces via CLI:

```bash
# Single workspace
mitto web --dir /path/to/project

# Multiple workspaces
mitto web --dir /path/to/project1 --dir /path/to/project2

# Specify ACP server per workspace
mitto web --dir auggie:/path/to/project1 --dir claude-code:/path/to/project2
```

## Predefined Prompts

Configure quick-access prompts that appear in the chat interface. Prompts are a
**top-level** configuration section:

```yaml
prompts:
  - name: "Continue"
    prompt: "Please continue with the current task."
  - name: "Propose a plan"
    prompt: "Please propose a plan for the current task."
    backgroundColor: "#E8F5E9" # Optional: custom background color
  - name: "Write tests"
    prompt: "Please write tests for the code you just created."
    backgroundColor: "#FFF3E0"
```

These prompts appear as quick-action buttons, making it easy to send common
instructions.

### Prompt Options

| Field             | Type   | Description                                                   |
| ----------------- | ------ | ------------------------------------------------------------- |
| `name`            | string | **Required.** Display name for the button                     |
| `prompt`          | string | **Required.** The prompt text to insert                       |
| `backgroundColor` | string | Optional. Hex color for button background (e.g., `"#E8F5E9"`) |

When a `backgroundColor` is set, the prompt button will display with that color
and automatically adjust text color for readability.

## Authentication

When exposing Mitto to the network, enable authentication to protect access.

### Simple Authentication

```yaml
web:
  auth:
    simple:
      username: admin
      password: your-secure-password
```

When enabled:

- Users are redirected to a login page
- Sessions are stored in secure HTTP-only cookies
- Sessions expire after 24 hours

### IP Allowlist

Bypass authentication for trusted IP addresses:

```yaml
web:
  auth:
    simple:
      username: admin
      password: secret
    allow:
      ips:
        - 127.0.0.1 # localhost IPv4
        - ::1 # localhost IPv6
        - 192.168.0.0/24 # local network
```

### Rate Limiting

Authentication includes automatic rate limiting:

- **3 failed attempts** within 1 minute triggers a **5-minute lockout**
- Returns `429 Too Many Requests` when blocked

## Security Configuration

Additional security settings for internet-exposed deployments.

### Trusted Proxies

When behind a reverse proxy, configure trusted proxies for correct client IP detection:

```yaml
web:
  security:
    trusted_proxies:
      - 127.0.0.1
      - 10.0.0.0/8
      - 172.16.0.0/12
```

### WebSocket Origin Validation

Allow WebSocket connections from specific origins:

```yaml
web:
  security:
    allowed_origins:
      - https://your-domain.com
      - https://abc123.ngrok.io
```

### Rate Limiting

```yaml
web:
  security:
    rate_limit_rps: 10 # Requests per second (default: 10)
    rate_limit_burst: 20 # Maximum burst (default: 20)
```

### Connection Limits

```yaml
web:
  security:
    max_ws_connections_per_ip: 10 # Default: 10
    max_ws_message_size: 65536 # Default: 64KB
```

## Lifecycle Hooks

Run commands at specific points in the server lifecycle.

### Up Hook

Runs **after** the server starts (asynchronously):

```yaml
web:
  hooks:
    up:
      command: "echo 'Server started on port ${PORT}'"
      name: "startup"
```

### Down Hook

Runs **before** the server shuts down (synchronously):

```yaml
web:
  hooks:
    down:
      command: "echo 'Server stopping'"
      name: "cleanup"
```

### Variable Substitution

- `${PORT}` - The port number the server is listening on

### External Access Tunnels

See [External Access Configuration](../ext-access.md) for details.

> **Important:** Always enable authentication when exposing Mitto externally.

## Development Mode

Serve static files from a directory for hot-reloading:

```bash
mitto web --static-dir ./web/static
```

Or in config:

```yaml
web:
  static_dir: ./web/static
```

## Reverse Proxy Setup

### nginx

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

Configure trusted proxies in Mitto:

```yaml
web:
  security:
    trusted_proxies:
      - 127.0.0.1
```

### Caddy

```caddy
example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy automatically handles WebSocket upgrades and HTTPS.

## Complete Example

```yaml
# Global prompts (top-level section)
prompts:
  - name: "Continue"
    prompt: "Please continue with the current task."
  - name: "Propose a plan"
    prompt: "Please propose a plan for the current task."
    backgroundColor: "#E8F5E9"
  - name: "Code Review"
    prompt: "Please review this code for issues."
    backgroundColor: "#FFF3E0"

# Web interface configuration
web:
  host: 0.0.0.0
  port: 8080
  theme: v2

  auth:
    simple:
      username: admin
      password: your-secure-password

  security:
    trusted_proxies:
      - 127.0.0.1
    allowed_origins:
      - https://your-tunnel.ngrok.io
    rate_limit_rps: 10
    rate_limit_burst: 20

  hooks:
    up:
      command: "ngrok http ${PORT}"
      name: "ngrok"
    down:
      command: "pkill -f 'ngrok http'"
      name: "stop-ngrok"
```

## Security Headers

Mitto automatically sets security headers:

- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Content-Security-Policy` (restricts script sources)
- `Cross-Origin-Opener-Policy: same-origin`
- `Cross-Origin-Resource-Policy: same-origin`

HSTS is enabled when using HTTPS.
