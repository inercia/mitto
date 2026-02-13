# Other Tunneling Methods

## localtunnel

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

## bore

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

## SSH Remote Port Forwarding

If you have a server with a public IP:

```bash
ssh -R 8080:localhost:8080 user@your-server.com
```

## Reverse Proxy (nginx/Caddy)

For permanent deployments, see [Reverse Proxy Setup](../web/README.md#reverse-proxy-setup).
