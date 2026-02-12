# Cloudflare Tunnel

[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
provides secure tunnels through Cloudflare's global network. Traffic stays encrypted
end-to-end and rides Cloudflare's network.

## Why Use a Tunnel?

| Local network only   | Cloudflare Tunnel          |
| -------------------- | -------------------------- |
| Same Wi-Fi required  | Access from anywhere       |
| IP changes           | Stable URL                 |
| ws:// only           | wss:// by default          |
| Router config needed | No router/firewall changes |

## Prerequisites

1. [Sign up for Cloudflare](https://dash.cloudflare.com/sign-up)
2. Install cloudflared:
   - **macOS**: `brew install cloudflared`
   - **Linux (Debian/Ubuntu)**: See [Cloudflare's releases page](https://pkg.cloudflare.com/)
   - **Windows**: Download from Cloudflare's releases page
3. Authenticate: `cloudflared tunnel login`

## Quick Tunnel (Temporary URL)

Quick tunnels generate a new URL every run - useful for testing:

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

> **Warning:** Quick tunnels generate a new URL every run. Use a named tunnel for
> a stable hostname.

## Named Tunnel (Stable URL)

For a persistent URL with your own domain:

1. **Login:**

   ```bash
   cloudflared tunnel login
   ```

2. **Create the tunnel:**

   ```bash
   cloudflared tunnel create mitto
   ```

   Save the generated Tunnel ID.

3. **Route DNS to the tunnel:**

   ```bash
   cloudflared tunnel route dns mitto mitto.yourdomain.com
   ```

4. **Create `~/.cloudflared/config.yml`:**

   ```yaml
   tunnel: <YOUR_TUNNEL_ID>
   credentials-file: /Users/yourname/.cloudflared/<TUNNEL_ID>.json
   ingress:
     - hostname: mitto.yourdomain.com
       service: http://localhost:8080
     - service: http_status:404
   ```

5. **Mitto configuration:**
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

## Run as a Service

For reliability, run cloudflared as a system service:

- **macOS:**

  ```bash
  cloudflared service install
  sudo launchctl start com.cloudflare.cloudflared
  ```

- **Linux:**

  ```bash
  sudo cloudflared service install
  sudo systemctl enable --now cloudflared
  ```

- **Windows:**
  ```bash
  cloudflared service install
  net start cloudflared
  ```

## Benefits

- **Cloudflare's network** - Global CDN and DDoS protection
- **Zero Trust integration** - Use Cloudflare Access for authentication
- **Custom domains** - Use your own domain with free HTTPS
- **No exposed ports** - Outbound-only connections
- **Low latency** - Via the nearest Cloudflare edge

## Troubleshooting

### Tunnel connects but WebSocket fails

Ensure your local server is running:

```bash
curl http://localhost:8080
```

### DNS not resolving

DNS may take a few minutes after `cloudflared tunnel route dns`. Verify with:

```bash
dig mitto.yourdomain.com
```

### Connection drops

- Check your internet stability
- Increase WebSocket ping interval
- Run cloudflared as a service for reliability

## Security Considerations

> **Warning:** A tunnel exposes your agent to the internet. Protect it with
> authentication and a non-guessable hostname.

- **Cloudflare Access** - Add SSO/OAuth authentication layer
- **Zero Trust policies** - Define who can access the tunnel
- **Service tokens** - For automated/API access
- **Unique subdomains** - Use non-guessable hostnames (e.g., `mitto-abc123.yourdomain.com`)
- **Monitor access logs** - Use the Cloudflare dashboard
