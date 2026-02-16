# External Access Configuration

This document covers methods for exposing Mitto's web interface to the internet, enabling
access from mobile devices, remote machines, or anywhere outside your local network.

## Table of Contents

- [Overview](#overview)
- [Built-in External Listener](#built-in-external-listener)
- [Tunneling Providers](#tunneling-providers)
- [Security Considerations](#security-considerations)
  - [Scanner Defense](#scanner-defense)

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

When external access is enabled, **Scanner Defense** is automatically activated to protect
against automated vulnerability scanners. See [Scanner Defense](ext-access/scanner-defense.md) for details.

---

## Tunneling Providers

| Provider              | Description                                              | Documentation                             |
| --------------------- | -------------------------------------------------------- | ----------------------------------------- |
| **Tailscale Funnel**  | Exposes through Tailscale's network with automatic HTTPS | [tailscale.md](ext-access/tailscale.md)   |
| **ngrok**             | Quick public URLs with inspection dashboard              | [ngrok.md](ext-access/ngrok.md)           |
| **Cloudflare Tunnel** | Global CDN, Zero Trust, custom domains                   | [cloudflare.md](ext-access/cloudflare.md) |
| **Other**             | localtunnel, bore, SSH, reverse proxy                    | [other.md](ext-access/other.md)           |

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
