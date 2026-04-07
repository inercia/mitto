# Cloudflare Tunnel

[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
provides secure tunnels through Cloudflare's global network. Traffic stays encrypted
end-to-end and rides Cloudflare's network. No router or firewall changes required —
`cloudflared` makes outbound-only connections to Cloudflare's edge.

## Why Use a Tunnel?

| Local network only   | Cloudflare Tunnel          |
| -------------------- | -------------------------- |
| Same Wi-Fi required  | Access from anywhere       |
| IP changes           | Stable URL                 |
| ws:// only           | wss:// by default          |
| Router config needed | No router/firewall changes |

## Prerequisites

Install `cloudflared`:

- **macOS**: `brew install cloudflared`
- **Linux (Debian/Ubuntu)**: See [Cloudflare's releases page](https://pkg.cloudflare.com/)
- **Windows**: Download from [Cloudflare's releases page](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)

For quick tunnels, no Cloudflare account is needed. For named tunnels with a custom
domain, [sign up for Cloudflare](https://dash.cloudflare.com/sign-up) and authenticate:

```bash
cloudflared tunnel login
```

## How It Works with Mitto

Tunnels must always go through Mitto's **external listener**, not the local listener.
The local listener (`127.0.0.1`) is reserved for direct local access only — tunneling
services should never use it.

When `web.auth` is configured and `external_port` is enabled (≥ 0), Mitto starts a
secondary listener on `0.0.0.0` that requires authentication for **all** connections,
including those from localhost. This is the listener that `cloudflared` should connect to.

Mitto's `web.hooks.up` runs a command when the server starts and passes the `${PORT}`
variable. When the external listener is active, `${PORT}` is automatically set to the
**external port** — so `cloudflared tunnel --url http://localhost:${PORT}` points to
the right place. The external port is typically random (assigned by the OS), but
`${PORT}` resolves it automatically.

## Quick Tunnel (Temporary URL)

Quick tunnels create a random `*.trycloudflare.com` URL each time — no account needed.
The URL changes every restart, so this is best for testing or occasional use.

```yaml
web:
  external_port: 0  # Random port for external access (required for tunneling)
  hooks:
    up:
      command: "cloudflared tunnel --url http://localhost:${PORT}"
      name: "cloudflare-tunnel"
  auth:
    simple:
      username: admin
      password: your-secure-password
```

When Mitto starts, you'll see the URL in the terminal output:

```
Your quick Tunnel has been created! Visit it at:
https://some-random-words.trycloudflare.com
```

> **Note:** The `up` hook process is automatically killed when Mitto shuts down,
> so no `down` hook is needed.

Since `external_port: 0` picks a random port each time, `${PORT}` resolves to whatever
the OS assigned. This works perfectly with quick tunnels since the URL is also random.

## Named Tunnel (Stable URL)

For a persistent URL with your own domain:

### 1. Create the tunnel

```bash
cloudflared tunnel login
cloudflared tunnel create mitto
```

This generates a credentials file at `~/.cloudflared/<TUNNEL_ID>.json`.

### 2. Route DNS

```bash
cloudflared tunnel route dns mitto mitto.yourdomain.com
```

This creates a CNAME record pointing to the tunnel. DNS propagation may take a few
minutes.

### 3. Configure Mitto with a fixed external port

Named tunnels need a known port in `~/.cloudflared/config.yml`. Since the external
port is random by default, set a fixed `external_port`:

```yaml
web:
  external_port: 8443  # Fixed port for the tunnel to connect to
  hooks:
    up:
      command: "cloudflared tunnel run mitto"
      name: "cloudflare-tunnel"
  auth:
    simple:
      username: admin
      password: your-secure-password
```

### 4. Create `~/.cloudflared/config.yml`

Point the tunnel to the fixed external port:

```yaml
tunnel: <YOUR_TUNNEL_ID>
credentials-file: /path/to/.cloudflared/<TUNNEL_ID>.json
ingress:
  - hostname: mitto.yourdomain.com
    service: http://localhost:8443
  - service: http_status:404
```

> **Important:** The `service` port must match the `external_port` configured in Mitto.
> Do not point the tunnel to the local port — the local listener is reserved for
> direct local access and should not be exposed through tunnels.

## Run as a Service

Instead of using Mitto's `up` hook, you can run `cloudflared` as a system service for
maximum reliability. In this case, **do not** configure an `up` hook — the tunnel runs
independently of Mitto.

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

When running as a service, use a fixed `external_port` in Mitto's config so the
tunnel always knows where to connect.

## Benefits

- **Automatic HTTPS** — Valid TLS certificates via Cloudflare
- **Global CDN** — DDoS protection and low latency via nearest edge
- **Zero Trust integration** — Add Cloudflare Access for SSO/OAuth authentication
- **Custom domains** — Use your own domain with free HTTPS
- **No exposed ports** — Outbound-only connections, no firewall changes
- **WebSocket support** — Works out of the box, including `wss://`

### Performance

Cloudflare tunnels are noticeably faster than alternatives like Tailscale Funnel.
The main reasons:

- **Edge proximity** — Cloudflare has edge nodes in 300+ cities worldwide. Your
  browser connects to the nearest edge (often in the same city), which then routes
  through the tunnel to your machine. Tailscale's DERP relay network has far fewer
  points of presence, so traffic often travels further before reaching a relay.
- **HTTP-optimized infrastructure** — Cloudflare is fundamentally a CDN company.
  Their network is purpose-built for HTTP/WebSocket traffic with optimized routing,
  connection pooling, and TLS termination at the edge. Tailscale is designed for
  mesh VPN (device-to-device WireGuard), and Funnel adds HTTP proxying on top of
  that, which introduces overhead.
- **QUIC transport** — The `cloudflared` tunnel uses QUIC for the connection between
  your machine and Cloudflare's edge, which reduces connection setup latency and
  handles packet loss better than TCP-based alternatives.
- **TLS termination at the edge** — Cloudflare terminates TLS close to the user and
  uses optimized internal routing to the tunnel. With Tailscale Funnel, the full TLS
  handshake and encrypted payload must traverse the relay network.

## Troubleshooting

### Quick tunnel shows 404

If `~/.cloudflared/config.yml` exists with a named tunnel config, the catch-all
`http_status:404` rule may intercept requests meant for the quick tunnel. Either:

- Temporarily rename the config: `mv ~/.cloudflared/config.yml ~/.cloudflared/config.yml.bak`
- Or remove the catch-all rule from the config

### Named tunnel shows "Invalid tunnel secret"

The local credentials file is out of sync with Cloudflare. Delete and recreate:

```bash
cloudflared tunnel cleanup mitto
cloudflared tunnel delete mitto
cloudflared tunnel create mitto
cloudflared tunnel route dns mitto mitto.yourdomain.com
```

### Named tunnel shows "control stream" errors

Another connector may already be running for the same tunnel (e.g., on another machine
or as a service). Clean up stale connections:

```bash
cloudflared tunnel cleanup mitto
```

Then try again. If the issue persists, check for a running `cloudflared` service:

```bash
# macOS
sudo launchctl list | grep cloudflare
# Linux
sudo systemctl status cloudflared
```

### Tunnel connects but pages don't load

Verify Mitto's external listener is running. Find the actual port:

```bash
lsof -i -P -n | grep mitto-app | grep LISTEN
```

Then test direct access to the external port:

```bash
curl -v http://localhost:<external_port>/
```

### DNS not resolving

DNS may take a few minutes after `cloudflared tunnel route dns`. Verify:

```bash
dig mitto.yourdomain.com
```

### Connection drops

- Run `cloudflared` as a system service for reliability
- Check your internet connection stability
- On macOS, Mitto automatically acquires a power assertion to prevent sleep from
  interrupting the external listener

## Adding Cloudflare Access (SSO/OAuth)

[Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/)
adds an SSO/OAuth authentication layer at Cloudflare's edge, in front of the tunnel.
This provides identity-provider-based login (Google, GitHub, one-time PIN, etc.)
before requests even reach Mitto.

### How it works

1. **User visits** `https://mitto.yourdomain.com`
2. **Cloudflare Access intercepts** the request at the edge and presents a login screen
   (SSO, OAuth, one-time PIN, etc.)
3. **After authentication**, Cloudflare forwards the request through the tunnel with
   identity headers (`Cf-Access-Jwt-Assertion`, `Cf-Access-Authenticated-User-Email`)
4. **`cloudflared` connects** to Mitto's **external listener** on localhost
5. **Mitto validates the JWT** — checking signature (RS256 via JWKS), issuer, audience,
   and expiry. If valid, access is granted without an additional login page.

### Authentication modes

**Cloudflare auth only** (`cloudflare` configured, no `simple`):
Users authenticate once through Cloudflare Access. Mitto validates the JWT and grants
access — no login page is shown.

**Both configured** (`cloudflare` + `simple`):
Either method grants access. Cloudflare Access users authenticate through Cloudflare's
login flow and reach Mitto directly. Users who access directly (e.g., bypassing
Cloudflare) can log in with username/password.

**Simple auth only** (`simple` configured, no `cloudflare`):
Traditional username/password login. No Cloudflare JWT validation.

### Setup

#### 1. Create a Cloudflare Access application

1. Go to [Cloudflare One](https://dash.cloudflare.com/) → **Access Control** → **Applications**
2. Click **Add an application** → **Self-hosted**
3. Set the **Publish hostname** to your tunnel hostname (e.g., `mitto.yourdomain.com`)
4. Configure an **Access policy** — for example:
   - **Allow** emails ending in `@yourdomain.com`
   - **Allow** specific email addresses
   - **Require** a one-time PIN sent via email
5. Save the application

#### 2. Configure Mitto

Add a `cloudflare` section under `web.auth` with your team domain and audience tag
(found in Cloudflare Access → Applications → your app → Overview):

```yaml
web:
  external_port: 0
  hooks:
    up:
      command: "cloudflared tunnel --url http://localhost:${PORT}"
      name: "cloudflare-tunnel"
  auth:
    cloudflare:
      team_domain: yourteam.cloudflareaccess.com
      audience: 32eafc7626e737...  # From Cloudflare Access app settings
```

Mitto fetches signing keys from `https://<team_domain>/cdn-cgi/access/certs` (JWKS)
and validates each JWT on every request. No additional login page is shown to users
who have already authenticated through Cloudflare Access.

To also allow direct username/password access, add `simple` alongside `cloudflare`:

```yaml
  auth:
    cloudflare:
      team_domain: yourteam.cloudflareaccess.com
      audience: 32eafc7626e737...
    simple:
      username: admin
      password: your-secure-password
```

### Security model

| Layer                      | What it protects                                                       |
| -------------------------- | ---------------------------------------------------------------------- |
| **Cloudflare Access**      | Edge-level SSO/OAuth — blocks unauthenticated requests at the edge     |
| **Mitto JWT validation**   | Validates Cloudflare JWTs at the origin — signature, issuer, audience, expiry |
| **Mitto simple auth**      | Optional username/password for direct access (standalone or fallback)  |
| **Tunnel encryption**      | Data in transit — TLS between browser and edge                         |
| **External listener**      | All tunnel traffic requires auth, even from localhost                  |
| **API prefix**             | Obscures API endpoints (`/mitto/api/...`)                              |

With `cloudflare` auth configured, users authenticate once through Cloudflare Access.
Mitto validates the JWT to confirm identity — no additional login page is required.
If `simple` auth is also configured, it provides an additional access method for
users who access Mitto directly without going through Cloudflare.

### Cloudflare Access headers (reference)

When a user authenticates through Cloudflare Access, these headers are added to
requests forwarded to the origin:

| Header                                | Description                                    |
| ------------------------------------- | ---------------------------------------------- |
| `Cf-Access-Jwt-Assertion`             | Signed JWT token (RS256) — validated by Mitto  |
| `Cf-Access-Authenticated-User-Email`  | Authenticated user's email                     |
| `CF_Authorization` (cookie)           | Same JWT (set in browser, used as fallback)    |

Mitto validates the `Cf-Access-Jwt-Assertion` header (falling back to the
`CF_Authorization` cookie) against Cloudflare's public signing keys fetched from
`https://<team_domain>/cdn-cgi/access/certs` (JWKS). Validation checks:

- **Signature** — RS256 signature verified against JWKS public keys
- **Issuer** — must match `https://<team_domain>`
- **Audience** — must match the configured `audience` value
- **Expiry** — token must not be expired

---

## Security Considerations

> **Warning:** A tunnel exposes your agent to the internet. Always enable authentication.

- **Mitto auth** — Configure `web.auth.cloudflare` and/or `web.auth.simple` (required for external listener)
- **Cloudflare Access** — Add SSO/OAuth as an authentication layer at the edge (recommended)
- **Non-guessable hostnames** — Use something like `mitto-abc123.yourdomain.com`
- **API prefix** — Keep `api_prefix: /mitto` for security through obscurity
- **Monitor access** — Check Mitto's `access.log` and the Cloudflare dashboard
