/**
 * JS-SDK contract-smoke driver (mitto-7gta.25).
 *
 * Run under Bun as a subprocess by
 * tests/integration/inprocess/sdk_contract_test.go — NOT part of `bun test
 * web/static` (lives outside web/static so make test-js never discovers it).
 * Exercises web/static/sdk against a real in-process Mitto server (mock ACP)
 * and prints ONE JSON document to stdout: a normalized "trace" of curated,
 * order-stable observations plus the sorted top-level key set of every raw
 * REST response seen, so the Go side can assert its structs only require a
 * subset of what the server actually returns (response-shape drift guard).
 *
 * Config is passed via environment variables (no CLI flags needed):
 *   MITTO_BASE_URL   - e.g. "http://127.0.0.1:12345"
 *   MITTO_WS_BASE_URL - e.g. "ws://127.0.0.1:12345"
 *   MITTO_API_PREFIX  - e.g. "/mitto" (defaults to "/mitto")
 *   MITTO_TOKEN       - shared bearer token
 *   MITTO_WORKING_DIR - working_dir for session creation
 */
import { createClient, sharedTokenAuth } from "../../../web/static/sdk/index.js";

/**
 * Bun's native `WebSocket` reads `{ headers }` from its 2nd constructor
 * argument, not the 3rd — unlike Node's `ws` package, which SessionStream's
 * `_openSocket()` targets (`new WebSocketImpl(url, protocols, options)`, the
 * `ws`/WHATWG convention). Without this adapter the shared-token
 * `Authorization` header is silently dropped on the WS upgrade under Bun,
 * and the handshake gets a 401. See github.com/oven-sh/bun issues on this
 * (headers-in-3rd-arg support). Node's `ws` package needs no such wrapper.
 */
class BunWebSocket extends WebSocket {
  constructor(url, protocols, options) {
    const merged = { ...(options || {}) };
    if (protocols !== undefined) merged.protocols = protocols;
    super(url, merged);
  }
}

const trace = [];
const keySets = {};

function record(kind, fields = {}) {
  trace.push({ kind, ...fields });
}

function recordKeys(label, body) {
  if (body && typeof body === "object" && !Array.isArray(body)) {
    keySets[label] = Object.keys(body).sort();
  }
}

async function main() {
  const baseUrl = process.env.MITTO_BASE_URL;
  const wsBaseUrl = process.env.MITTO_WS_BASE_URL;
  const apiPrefix = process.env.MITTO_API_PREFIX || "/mitto";
  const token = process.env.MITTO_TOKEN;
  const workingDir = process.env.MITTO_WORKING_DIR;

  const client = createClient({
    baseUrl,
    apiPrefix,
    wsBaseUrl,
    WebSocket: BunWebSocket,
    auth: sharedTokenAuth({ getToken: () => token }),
  });

  // 1. Create conversation.
  const created = await client.sessions.create({ working_dir: workingDir });
  recordKeys("session_create", created);
  record("session_created", { hasId: !!created.session_id });
  const sessionId = created.session_id;

  // 2. Open realtime + register as observer (load_events), then send prompt
  //    and collect the streamed agent text to completion.
  const stream = client.sessionStream(sessionId);
  const agentChunks = [];
  let promptCompleteEventCount = null;

  const opened = new Promise((resolve) => stream.once("open", resolve));
  const completed = new Promise((resolve) => {
    stream.on("message", (msg) => {
      if (msg.type === "agent_message" && msg.data?.html) {
        agentChunks.push(msg.data.html);
      }
      if (msg.type === "prompt_complete") {
        promptCompleteEventCount = msg.data?.event_count ?? 0;
        resolve();
      }
    });
  });

  stream.connect();
  await opened;
  record("ws_connected", {});

  await stream.sendWhenConnected({ type: "load_events", data: { limit: 50 } });
  await new Promise((r) => setTimeout(r, 50));

  await stream.sendPrompt({ message: "Hello" });
  await completed;
  record("agent_message", { text: agentChunks.join("") });
  record("prompt_complete", { hasEventCount: typeof promptCompleteEventCount === "number" });

  // 3. Queue add/list/clear.
  const queued = await client.sessions.queue.add(sessionId, { message: "queued message" });
  recordKeys("queue_add", queued);
  record("queue_added", { hasId: !!queued.id });

  const listed = await client.sessions.queue.list(sessionId);
  recordKeys("queue_list", listed);
  record("queue_listed", { count: listed.count });

  await client.sessions.queue.clear(sessionId);
  record("queue_cleared", {});

  // 4. Loop set/get/detach. No explicit `triggers` — pkg/api's SetLoopRequest
  // has no way to set it (only a dead legacy "trigger" singular field the
  // backend no longer accepts), so both clients rely on the server default
  // (["schedule"]) to keep the two comparable.
  const loopSet = await client.sessions.loop.set(sessionId, {
    prompt: "contract smoke loop",
    frequency: { value: 1, unit: "hours" },
    enabled: true,
  });
  recordKeys("loop_set", loopSet);
  // The server omits "trigger" from the response entirely when unset
  // (Go json:",omitempty"), so default to "schedule" here — the same
  // implicit default EffectiveTriggers() applies server-side — rather than
  // recording the field's mere on-wire presence.
  record("loop_set", { trigger: loopSet.trigger || "schedule" });

  const loopGot = await client.sessions.loop.get(sessionId);
  recordKeys("loop_get", loopGot);
  record("loop_get", { enabled: loopGot.enabled });

  await client.sessions.loop.detach(sessionId);
  record("loop_detached", {});

  // 5. Close.
  stream.close();
  await client.sessions.remove(sessionId);
  record("session_removed", {});

  process.stdout.write(JSON.stringify({ trace, keySets }) + "\n");
}

main().catch((err) => {
  process.stderr.write(`driver failed: ${err?.stack || err}\n`);
  process.exit(1);
});
