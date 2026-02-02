# External Access Configuration

This document covers methods for exposing Mitto's web interface to the internet, enabling
access from mobile devices, remote machines, or anywhere outside your local network.

## Table of Contents

- [Overview](#overview)
- [Built-in External Listener](#built-in-external-listener)
- [Tailscale Funnel](#tailscale-funnel)
- [ngrok](#ngrok)
- [Cloudflare Tunnel](#cloudflare-tunnel)
- [Other Methods](#other-methods)
- [Security Considerations](#security-considerations)

## Overview

By default, Mitto's web interface only accepts connections from localhost (`127.0.0.1`).
To access Mitto from other devices, you need to either:

1. **Use the built-in external listener** - Opens a port on all interfaces (`0.0.0.0`)
2. **Use a tunneling service** - Creates a secure tunnel to your local server

**Important:** Always enable authentication when exposing Mitto to the network. See
[Authentication](web/README.md#authentication) for details.

## Built-in External Listener

Mitto has built-in support for external access via a secondary listener that binds to
`0.0.0.0`. This listener requires authentication for all connections, even from localhost.

### Configuration

```yaml
web:
  port: 8080 # Local port (127.0.0.1)
  external_port: 8443 # External port (0.0.0.0) - requires auth

  auth:
    simple:
      username: admin
      password: your-secure-password
```

### Port Values

| Value | Behavior                 |
| ----- | ------------------------ |
| `-1`  | Disabled (default)       |
| `0`   | Random port (OS chooses) |
| `>0`  | Specific port number     |

### CLI Usage

```bash
# Start with external access on port 8443
mitto web --port-external 8443

# Use random external port
mitto web --port-external 0
```

### Security

The external listener always requires authentication, even for connections from localhost
through that port. This prevents authentication bypass attacks.

---

## Tailscale Funnel

[Tailscale Funnel](https://tailscale.com/kb/1223/tailscale-funnel/) exposes your server
through Tailscale's network with automatic HTTPS certificates.

### Prerequisites

1. Install [Tailscale](https://tailscale.com/download)
2. Enable Funnel in your Tailscale admin console
3. Run `tailscale up` to connect to your tailnet

### Configuration

```yaml
web:
  port: 8080
  hooks:
    up:
      command: "tailscale funnel ${PORT}"
      name: "tailscale-funnel"
```

For background operation:

```yaml
web:
  hooks:
    up:
      command: "tailscale funnel ${PORT} &"
      name: "tailscale-funnel"
```

### Benefits

- **Automatic HTTPS** - Valid TLS certificates without configuration
- **No separate auth needed** - Tailscale handles identity (optional)
- **Tailnet integration** - Accessible only to your Tailscale network or publicly
- **No account/limits** - Part of your Tailscale plan

### Security Considerations

- You can restrict access to your Tailnet only (no public exposure)
- ACLs control who can access the funnel
- No additional Mitto authentication required if restricted to trusted users

---

## ngrok

[ngrok](https://ngrok.com/) creates secure tunnels to expose your local server to the
internet with a public URL.

### Prerequisites

1. [Sign up for ngrok](https://dashboard.ngrok.com/signup)
2. Install ngrok: `brew install ngrok` (macOS) or download from ngrok.com
3. Authenticate: `ngrok config add-authtoken YOUR_TOKEN`

### Configuration

```yaml
web:
  port: 8080
  hooks:
    up:
      command: "ngrok http ${PORT} --log=stdout"
      name: "ngrok"
  auth:
    simple:
      username: admin
      password: your-secure-password
```

For background operation with URL display:

```yaml
web:
  hooks:
    up:
      command: "ngrok http ${PORT} > /dev/null 2>&1 & sleep 2 && curl -s localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'"
      name: "ngrok"
```

### Benefits

- **Quick setup** - No DNS or infrastructure needed
- **Stable URLs** - Paid plans offer custom/reserved domains
- **Inspection UI** - Local dashboard at `localhost:4040`
- **Request replay** - Debug requests from the ngrok dashboard

### Security Considerations

- **Always enable Mitto authentication** - ngrok URLs are publicly accessible
- **Use ngrok's OAuth/OIDC** - Add additional authentication layer
- **IP restrictions** - ngrok paid plans support IP allowlisting
- **TLS termination** - ngrok handles HTTPS automatically

Configure allowed origins for WebSocket connections:

```yaml
web:
  security:
    allowed_origins:
      - https://your-tunnel.ngrok.io
```

---

## Cloudflare Tunnel

[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
provides secure tunnels through Cloudflare's global network.

### Prerequisites

1. [Sign up for Cloudflare](https://dash.cloudflare.com/sign-up)
2. Install cloudflared: `brew install cloudflared` (macOS)
3. Authenticate: `cloudflared tunnel login`

### Configuration

Quick tunnel (temporary URL):

```yaml
web:
  port: 8080
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

Named tunnel (persistent URL):

```bash
# Create a named tunnel
cloudflared tunnel create mitto

# Configure DNS (if you have a domain on Cloudflare)
cloudflared tunnel route dns mitto mitto.yourdomain.com
```

```yaml
web:
  hooks:
    up:
      command: "cloudflared tunnel run mitto"
      name: "cloudflare-tunnel"
    down:
      command: "pkill -f 'cloudflared tunnel'"
      name: "stop-tunnel"
```

### Benefits

- **Cloudflare's network** - Global CDN and DDoS protection
- **Zero Trust integration** - Use Cloudflare Access for authentication
- **Custom domains** - Use your own domain with free HTTPS
- **No exposed ports** - Outbound-only connections

### Security Considerations

- **Cloudflare Access** - Add SSO/OAuth authentication layer
- **Zero Trust policies** - Define who can access the tunnel
- **Service tokens** - For automated/API access

---

## Other Methods

### localtunnel

[localtunnel](https://localtunnel.me/) is a simple, free alternative:

```bash
# Install
npm install -g localtunnel

# Start tunnel
lt --port 8080
```

```yaml
web:
  hooks:
    up:
      command: "lt --port ${PORT} --subdomain mitto"
      name: "localtunnel"
    down:
      command: "pkill -f 'lt --port'"
      name: "stop-localtunnel"
  auth:
    simple:
      username: admin
      password: your-password
```

### bore

[bore](https://github.com/ekzhang/bore) is a lightweight, self-hostable tunnel:

```bash
# Install
cargo install bore-cli

# Use public server
bore local 8080 --to bore.pub
```

```yaml
web:
  hooks:
    up:
      command: "bore local ${PORT} --to bore.pub"
      name: "bore"
    down:
      command: "pkill -f 'bore local'"
      name: "stop-bore"
```

### SSH Remote Port Forwarding

If you have a server with a public IP:

```bash
ssh -R 8080:localhost:8080 user@your-server.com
```

### Reverse Proxy (nginx/Caddy)

For permanent deployments, see [Reverse Proxy Setup](web/README.md#reverse-proxy-setup).

---

## Security Considerations

When exposing Mitto externally, follow these security best practices:

### Always Enable Authentication

```yaml
web:
  auth:
    simple:
      username: admin
      password: your-secure-password
```

### Use Strong Passwords

- Minimum 16 characters
- Mix of letters, numbers, and symbols
- Use a password manager

### Configure Allowed Origins

Prevent CSRF attacks by specifying allowed WebSocket origins:

```yaml
web:
  security:
    allowed_origins:
      - https://your-tunnel.example.com
```

### Enable Rate Limiting

Protect against brute-force attacks:

```yaml
web:
  security:
    rate_limit_rps: 10
    rate_limit_burst: 20
```

### Consider IP Allowlisting

Bypass authentication only for trusted IPs:

```yaml
web:
  auth:
    allow:
      ips:
        - 192.168.0.0/24
```

### Use Multiple Authentication Layers

Combine Mitto's authentication with tunnel-level auth:

- ngrok: OAuth/OIDC integration
- Cloudflare: Cloudflare Access
- Tailscale: Tailnet ACLs

### Monitor Access

Enable debug logging to monitor external connections:

```bash
mitto web --debug
```

---

## Related Documentation

| Topic             | Location                                              |
| ----------------- | ----------------------------------------------------- |
| Web Configuration | [web/README.md](web/README.md)                        |
| Authentication    | [web/README.md](web/README.md#authentication)         |
| Lifecycle Hooks   | [web/README.md](web/README.md#lifecycle-hooks)        |
| Security Settings | [web/README.md](web/README.md#security-configuration) |
