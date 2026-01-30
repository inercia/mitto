# Web Configuration

This document covers configuration options for the Mitto web interface.

## Basic Configuration

```yaml
web:
  host: 127.0.0.1 # Server host (default: 127.0.0.1)
  port: 8080 # Server port (default: 8080)
  theme: v2 # UI theme: "default" or "v2"
```

### Host

- `127.0.0.1` (default) - Only accept local connections
- `0.0.0.0` - Accept connections from any interface (required for remote access)

### Port

The HTTP server port. Default is `8080`.

### Theme

- `default` - Original Tailwind-based theme with dark mode
- `v2` - Modern theme with a cleaner visual style

## Starting the Web Server

```bash
# Start with default settings (localhost:8080)
mitto web

# Specify a different port
mitto web --port 3000

# Listen on all interfaces (for remote access)
mitto web --host 0.0.0.0

# Use a specific ACP server
mitto web --server claude-code
```

## Predefined Prompts

Configure quick-access prompts that appear in the chat interface. Prompts are a
**top-level** configuration section (not under `web`):

```yaml
prompts:
  - name: "Continue"
    prompt: "Please continue with the current task."
  - name: "Propose a plan"
    prompt: "Please propose a plan for the current task."
    backgroundColor: "#E8F5E9"  # Optional: custom background color
  - name: "Write tests"
    prompt: "Please write tests for the code you just created."
    backgroundColor: "#FFF3E0"
```

These prompts appear as quick-action buttons, making it easy to send common
instructions.

### Prompt Options

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | **Required.** Display name for the button |
| `prompt` | string | **Required.** The prompt text to insert |
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

### Using ngrok

[ngrok](https://ngrok.com/) creates secure tunnels to expose your local server to the
internet.

```yaml
web:
  port: 8080
  hooks:
    up:
      command: "ngrok http ${PORT} --log=stdout"
      name: "ngrok"
    down:
      command: "pkill -f 'ngrok http'"
      name: "stop-ngrok"
  auth:
    simple:
      username: admin
      password: your-secure-password
```

> **Important:** Always enable authentication when using ngrok since your server becomes
> publicly accessible.

For background operation with URL display:

```yaml
web:
  hooks:
    up:
      command:
        "ngrok http ${PORT} > /dev/null 2>&1 & sleep 2 && curl -s
        localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'"
      name: "ngrok"
    down:
      command: "pkill -f 'ngrok http'"
      name: "stop-ngrok"
```

### Using Tailscale Funnel

[Tailscale Funnel](https://tailscale.com/kb/1223/tailscale-funnel/) exposes your server
through your Tailscale network with automatic HTTPS.

```yaml
web:
  port: 8080
  hooks:
    up:
      command: "tailscale funnel ${PORT}"
      name: "tailscale-funnel"
    down:
      command: "tailscale funnel ${PORT} off"
      name: "stop-funnel"
```

Benefits:

- Automatic HTTPS with valid certificates
- No separate authentication needed (Tailscale handles identity)
- Integration with your Tailscale network

For background operation:

```yaml
web:
  hooks:
    up:
      command: "tailscale funnel ${PORT} &"
      name: "tailscale-funnel"
    down:
      command: "tailscale funnel --off ${PORT}"
      name: "stop-funnel"
```

### Using Cloudflare Tunnel

[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
provides secure tunnels through Cloudflare's network.

```yaml
web:
  hooks:
    up:
      command: "cloudflared tunnel --url http://localhost:${PORT}"
      name: "cloudflare-tunnel"
    down:
      command: "pkill -f 'cloudflared tunnel'"
      name: "stop-tunnel"
  auth:
    simple:
      username: admin
      password: your-secure-password
```

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

## Related Documentation

- [Configuration Overview](config.md) - Main configuration documentation
- [macOS Configuration](config-mac.md) - Desktop app settings
