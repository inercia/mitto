# ngrok

[ngrok](https://ngrok.com/) creates secure tunnels to expose your local server to the
internet with a public URL.

## Prerequisites

1. [Sign up for ngrok](https://dashboard.ngrok.com/signup)
2. Install ngrok: `brew install ngrok` (macOS) or download from ngrok.com
3. Authenticate: `ngrok config add-authtoken YOUR_TOKEN`

## Configuration

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

## Benefits

- **Quick setup** - No DNS or infrastructure needed
- **Stable URLs** - Paid plans offer custom/reserved domains
- **Inspection UI** - Local dashboard at `localhost:4040`
- **Request replay** - Debug requests from the ngrok dashboard

## Security Considerations

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
