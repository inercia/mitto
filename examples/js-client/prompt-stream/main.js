#!/usr/bin/env node
/**
 * Runnable proof that the Mitto JS SDK (web/static/sdk/) is genuinely
 * environment-agnostic and that programmatic shared-bearer-token auth works
 * end to end: authenticates, lists conversations, creates one, sends a
 * prompt, and streams the agent's response to stdout as it arrives. Runs
 * under both Bun and Node — no dependencies, no build step.
 *
 *   bun run examples/js-client/prompt-stream/main.js \
 *     --url http://localhost:8080 --token "$MITTO_TOKEN" \
 *     --dir /path/to/project --prompt "What does this project do?"
 *
 * Node needs the optional `ws` package for realtime streaming (Node's
 * global WebSocket cannot set the Authorization header the WS upgrade
 * needs); Bun does not.
 *
 * Press Ctrl-C to cancel early. The created conversation is deleted on exit.
 * See ../README.md and docs/api/go-sdk.md for the equivalent Go example.
 */
import { createClient, sharedTokenAuth, EVENTS } from "../../../web/static/sdk/index.js";

/**
 * Bun's native `WebSocket` reads `{ headers }` from its 2nd constructor
 * argument, not the 3rd (ws/WHATWG convention) that
 * SessionStream._openSocket() targets. Without this adapter the shared-token
 * Authorization header is silently dropped on the WS upgrade under Bun and
 * the handshake gets a 401. Node's `ws` package needs no such wrapper.
 * (Same adapter as tests/integration/sdkcontract/driver.js.)
 */
class BunWebSocket extends WebSocket {
  constructor(url, protocols, options) {
    const merged = { ...(options || {}) };
    if (protocols !== undefined) merged.protocols = protocols;
    super(url, merged);
  }
}

async function resolveWebSocketImpl() {
  if (typeof globalThis.Bun !== "undefined") return BunWebSocket;
  try {
    const { WebSocket: NodeWs } = await import("ws");
    return NodeWs;
  } catch {
    throw new Error(
      "This example needs the optional 'ws' package to stream over " +
        "WebSocket under Node (Node's built-in WebSocket cannot send the " +
        "Authorization header the upgrade needs). Install it with " +
        "`npm install ws`, or run this example with Bun instead " +
        "(`bun run main.js ...`), which needs no extra package.",
    );
  }
}

function parseArgs(argv) {
  const opts = {
    url: "http://localhost:8080",
    token: process.env.MITTO_TOKEN || "",
    dir: ".",
    prompt: "What does this project do?",
    timeoutMs: 120_000,
  };
  const flagToKey = { url: "url", token: "token", dir: "dir", prompt: "prompt" };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (!arg.startsWith("--")) continue;
    const name = arg.slice(2);
    const value = argv[++i];
    if (name in flagToKey) opts[flagToKey[name]] = value;
    else if (name === "timeout") opts.timeoutMs = Math.round(parseFloat(value) * 1000);
    else throw new Error(`unknown flag --${name}`);
  }
  return opts;
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const WebSocketImpl = await resolveWebSocketImpl();

  // No wsBaseUrl needed: wsUrlFor() (sdk/realtime/ws-transport.js) derives
  // ws(s):// automatically from an absolute http(s):// baseUrl like this
  // one. wsBaseUrl only matters for a relative baseUrl (same-origin browser
  // client).
  const client = createClient({
    baseUrl: opts.url,
    WebSocket: WebSocketImpl,
    auth: sharedTokenAuth({ getToken: () => opts.token }),
  });

  const sessions = await client.sessions.list();
  console.log(`Found ${sessions.length} existing conversation(s).`);

  const created = await client.sessions.create({
    name: "prompt-stream-example",
    working_dir: opts.dir,
  });
  const sessionId = created.session_id;
  console.log(`Created conversation ${sessionId}.`);

  let cleaned = false;
  const cleanup = async () => {
    if (cleaned) return;
    cleaned = true;
    await client.sessions.remove(sessionId).catch(() => {});
  };
  process.on("SIGINT", async () => {
    await cleanup();
    process.exit(130);
  });

  const timeoutTimer = setTimeout(async () => {
    console.error(`prompt-stream: timed out after ${opts.timeoutMs}ms`);
    await cleanup();
    process.exit(1);
  }, opts.timeoutMs);

  try {
    const stream = client.sessionStream(sessionId);
    const opened = new Promise((resolve, reject) => {
      stream.once("open", resolve);
      stream.once("error", reject);
    });
    stream.connect();
    await opened;

    await new Promise((resolve, reject) => {
      stream.on("message", (msg) => {
        if (msg.type === EVENTS.AGENT_MESSAGE && msg.data?.html) {
          process.stdout.write(msg.data.html);
        } else if (msg.type === EVENTS.PROMPT_COMPLETE) {
          process.stdout.write("\n");
          resolve();
        } else if (msg.type === EVENTS.ERROR) {
          reject(new Error(msg.data?.message || "agent error"));
        }
      });
      stream.sendPrompt({ message: opts.prompt }).catch(reject);
    });

    stream.close();
  } finally {
    clearTimeout(timeoutTimer);
    await cleanup();
  }
}

main().catch((err) => {
  console.error(`prompt-stream: ${err?.message || err}`);
  process.exitCode = 1;
});
