// Bun test preload — register happy-dom as the global DOM.
// Wired via bunfig.toml `[test] preload = ["./scripts/bun-happy-dom.js"]`.
// Only runs under `bun test`; Jest ignores this file entirely.
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({
  url: "http://localhost/",
});
