# browser-snippet

Minimal browser example of the [Mitto JS SDK](../../../web/static/sdk):
lists conversations and streams one prompt's response, with no build step.

## Run it

Serve `index.html` from any static file server (or open it directly with
`file://`) and point it at a running Mitto server:

The base URL is the Mitto host **origin only** — never append `/api`, since
the SDK's resource paths already start with it.

- **Same origin as Mitto** (e.g. served by Mitto itself, or a reverse proxy
  in front of it): leave the base URL and the token field empty — the
  browser's session cookie authenticates.
- **Different origin**: set the base URL to the Mitto host origin (e.g.
  `https://mitto.example.com`) and fill in a
  [shared bearer token](../../../docs/api/authentication.md#sharedtokenauth-bearer-token).
  The target host's CORS allowlist must include this page's origin (see
  [docs/config/web/README.md](../../../docs/config/web/README.md#cors-and-cross-origin-access)).

Click the button: it lists conversations, connects to the first one's
realtime stream, and sends a short prompt, printing the agent's reply.

See [docs/api/getting-started.md](../../../docs/api/getting-started.md) for
the full getting-started guide.
