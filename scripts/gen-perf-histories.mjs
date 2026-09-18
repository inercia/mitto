#!/usr/bin/env node
// Generates deterministic seeded history snapshots for the UI responsiveness
// benchmark suite's history-load.perf.spec.ts (mitto-sus.1.2). Each snapshot
// is a JSON array of {role, text} message pairs simulating a conversation of
// a given size; tests/ui/helpers/seed-perf-history.go reads a snapshot and
// writes it directly into the session store (bypassing ACP/UI) so the spec
// can measure DOM/frame cost of an already-large conversation without
// spending test time sending thousands of messages one at a time.
//
// Deterministic: the same --seed always produces byte-identical output, so
// CI regressions surface as fixture diffs, not flakes. Run manually to
// regenerate (outputs are committed, not built on the fly):
//   node scripts/gen-perf-histories.mjs [--seed N] [--out DIR]
//
// History cap: web/static/ has no exported MAX_HISTORY constant at the time
// of writing, so "max" below (5000 messages / 2500 exchanges) is a pragmatic
// stand-in for the conversation-size ceiling, documented here and in
// docs/devel/ui-responsiveness-benchmarks.md. Revisit if a real cap is later
// exposed.
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(__dirname, "..");

function parseArgs(argv) {
  let seed = 42;
  let outDir = join(repoRoot, "tests/ui/perf/fixtures/histories");
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--seed" && argv[i + 1]) seed = Number(argv[++i]);
    else if (argv[i] === "--out" && argv[i + 1]) outDir = resolve(argv[++i]);
  }
  return { seed, outDir };
}

// mulberry32: tiny deterministic PRNG (public-domain algorithm), sufficient
// for reproducible-but-varied filler text; not cryptographic.
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const TOPICS = [
  "the streaming buffer",
  "conversation switch latency",
  "the composer draft state",
  "render post-processors",
  "the WebSocket reconnection path",
  "session history pagination",
  "the perf mark instrumentation",
  "long-task budgets",
];

function buildMessages(count, rand) {
  const messages = [];
  const pairs = Math.floor(count / 2);
  for (let i = 0; i < pairs; i++) {
    const topic = TOPICS[Math.floor(rand() * TOPICS.length)];
    messages.push({
      role: "user",
      text: `History message ${i + 1}/${pairs}: what changed in ${topic}?`,
    });
    messages.push({
      role: "agent",
      text:
        `Reply ${i + 1}/${pairs} about ${topic}: this is deterministic ` +
        `filler content (seed-derived) sized to approximate a real agent ` +
        `reply of a few sentences, so DOM node count and layout cost scale ` +
        `realistically with conversation size.`,
    });
  }
  return messages;
}

function main() {
  const { seed, outDir } = parseArgs(process.argv.slice(2));
  mkdirSync(outDir, { recursive: true });

  // (name, message count) — see header comment re: the "max" pragmatic cap.
  const sizes = [
    ["small", 10],
    ["medium", 1000],
    ["max", 5000],
  ];

  for (const [name, count] of sizes) {
    const rand = mulberry32(seed + count); // distinct-but-deterministic per size
    const snapshot = {
      name,
      seed,
      messageCount: count,
      messages: buildMessages(count, rand),
    };
    const outPath = join(outDir, `${name}.json`);
    writeFileSync(outPath, JSON.stringify(snapshot, null, 2) + "\n");
    console.log(`wrote ${outPath}: ${snapshot.messages.length} messages`);
  }
}

main();
