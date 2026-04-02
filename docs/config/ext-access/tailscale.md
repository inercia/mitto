# Tailscale Funnel

[Tailscale Funnel](https://tailscale.com/kb/1223/tailscale-funnel/) exposes your server
through Tailscale's network with automatic HTTPS certificates.

## Prerequisites

1. Install [Tailscale](https://tailscale.com/download)
2. Enable Funnel in your Tailscale admin console
3. Run `tailscale up` to connect to your tailnet

## Configuration

```yaml
web:
  port: 8080
  hooks:
    up:
      command: "tailscale funnel ${PORT}"
      name: "tailscale-funnel"
```

## Benefits

- **Automatic HTTPS** - Valid TLS certificates without configuration
- **No separate auth needed** - Tailscale handles identity (optional)
- **Tailnet integration** - Accessible only to your Tailscale network or publicly
- **No account/limits** - Part of your Tailscale plan

### Performance

Tailscale Funnel may feel slower than alternatives like Cloudflare Tunnel. This is
because Tailscale is designed primarily for mesh VPN (direct WireGuard connections
between devices), and Funnel adds HTTP proxying through Tailscale's DERP relay
network. DERP relays have fewer points of presence than Cloudflare's 300+ edge
cities, so traffic often travels further before reaching a relay. Additionally,
the WireGuard encapsulation adds overhead for HTTP traffic that isn't present in
purpose-built HTTP tunnel solutions. For latency-sensitive use, consider
[Cloudflare Tunnel](cloudflare.md) as an alternative.

## Security Considerations

> **Warning:** Tailscale Funnel exposes your Mitto instance to the public internet.
> Automated bots continuously scan for open services and will find yours within
> minutes. You will see credential stuffing attempts, path probing, and vulnerability
> scans in your access logs. Always enable strong authentication (`web.auth.simple`
> with a non-guessable password) and consider enabling Mitto's
> [scanner defense](scanner-defense.md) to automatically block malicious IPs.

- You can restrict access to your Tailnet only (no public exposure) — this avoids
  the bot problem entirely but limits access to your Tailscale devices
- ACLs control who can access the funnel
- No additional Mitto authentication required if restricted to trusted users
- If using public Funnel, always configure `web.auth` — without it, anyone on the
  internet can access your AI agent
