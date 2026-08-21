/**
 * Tests for auth.js — the pre-auth login page module (mitto-7gta.19.1 Test
 * phase). auth.js had no test file before this bead (it was a non-module,
 * raw-`fetch` classic script); this pins the ES-module rewrite's behavior:
 * the auth-info UI adaptation and the login submit success/error paths.
 *
 * auth.js runs its init logic as an import-time side effect (guarded by
 * `document.readyState`), so each scenario dynamically imports it under a
 * unique query string after seeding the DOM + a mocked `globalThis.fetch` —
 * the same cache-busting convention used by
 * components/beads/detail/*.test.js for other import-time-side-effect
 * modules, needed here so each test gets a fresh module evaluation bound to
 * its own DOM/fetch instead of the first test's cached one.
 */
import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "./utils/testing/testGlobals.js";
import { fakeResponse } from "./sdk/testing/fake-server.js";

function buildDom() {
  document.body.innerHTML =
    '<div id="error" class="hidden"></div>' +
    '<div id="cloudflare-message" style="display: none;"></div>' +
    '<form id="loginForm">' +
    '<input id="username" />' +
    '<input id="password" />' +
    '<button id="submitBtn" type="submit">Sign In</button>' +
    "</form>";
}

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

function submitLogin(username, password) {
  document.getElementById("username").value = username;
  document.getElementById("password").value = password;
  document
    .getElementById("loginForm")
    .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
}

let seq = 0;
/** Seeds the DOM, then imports auth.js fresh (bypassing the ESM module
 *  cache) so its readyState-guarded init runs against THIS test's DOM. */
async function loadAuthPage() {
  seq += 1;
  buildDom();
  await import(`./auth.js?mitto-7gta-19-1-test-${seq}`);
  await flush();
}

describe("auth.js — pre-auth login page (mitto-7gta.19.1)", () => {
  let originalFetch;
  let originalHref;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    originalHref = window.location.href;
    window.mittoApiPrefix = "";
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    window.location.href = originalHref;
    delete window.mittoApiPrefix;
  });

  describe("auth-info UI adaptation", () => {
    test("simple auth: leaves the login form visible, cloudflare message hidden", async () => {
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: true, cloudflare: false } });
      await loadAuthPage();
      expect(document.getElementById("loginForm").style.display).not.toBe("none");
      expect(document.getElementById("cloudflare-message").style.display).toBe("none");
    });

    test("cloudflare-only: hides the form and reveals the cloudflare message", async () => {
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: false, cloudflare: true } });
      await loadAuthPage();
      expect(document.getElementById("loginForm").style.display).toBe("none");
      expect(document.getElementById("cloudflare-message").style.display).toBe("");
    });

    test("no auth configured: shows a generic notice and hides the form", async () => {
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: false, cloudflare: false } });
      await loadAuthPage();
      const errorDiv = document.getElementById("error");
      expect(errorDiv.textContent).toBe("No authentication method is configured.");
      expect(errorDiv.classList.contains("hidden")).toBe(false);
      expect(document.getElementById("loginForm").style.display).toBe("none");
    });

    test("auth-info fetch failure fails open: form stays visible", async () => {
      globalThis.fetch = async () => {
        throw new Error("offline");
      };
      await loadAuthPage();
      expect(document.getElementById("loginForm").style.display).not.toBe("none");
    });
  });

  describe("login submit", () => {
    function mockFetch({ loginStatus = 200, loginBody = { success: true } } = {}) {
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info")) return fakeResponse({ body: { simple: true } });
        if (u.includes("/api/login")) return fakeResponse({ status: loginStatus, body: loginBody });
        throw new Error("unexpected url " + u);
      };
    }

    test("success: redirects to /", async () => {
      mockFetch();
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
    });

    test("401 bad credentials: shows the server's message and re-enables the button", async () => {
      mockFetch({ loginStatus: 401, loginBody: { error: "Invalid username or password" } });
      await loadAuthPage();
      submitLogin("alice", "wrong");
      await flush();
      const errorDiv = document.getElementById("error");
      expect(errorDiv.classList.contains("hidden")).toBe(false);
      expect(errorDiv.textContent).toBe("Invalid username or password");
      const btn = document.getElementById("submitBtn");
      expect(btn.disabled).toBe(false);
      expect(btn.textContent).toBe("Sign In");
    });

    test("network failure: shows a generic network-error message", async () => {
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info")) return fakeResponse({ body: { simple: true } });
        if (u.includes("/api/login")) throw new Error("offline");
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      submitLogin("alice", "x");
      await flush();
      expect(document.getElementById("error").textContent).toBe(
        "Network error. Please try again.",
      );
    });

    test("disables the submit button and shows progress text while in flight", async () => {
      let resolveLogin;
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info")) return fakeResponse({ body: { simple: true } });
        if (u.includes("/api/login")) {
          return new Promise((resolve) => {
            resolveLogin = () => resolve(fakeResponse({ status: 200, body: { success: true } }));
          });
        }
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      submitLogin("alice", "x");
      await flush();
      const btn = document.getElementById("submitBtn");
      expect(btn.disabled).toBe(true);
      expect(btn.textContent).toBe("Signing in...");
      resolveLogin();
      await flush();
    });
  });

  test("missing required elements: logs an error and does not throw", async () => {
    document.body.innerHTML = "";
    globalThis.fetch = async () => fakeResponse({ status: 204 });
    const errSpy = jest.spyOn(console, "error").mockImplementation(() => {});
    seq += 1;
    await expect(import(`./auth.js?mitto-7gta-19-1-test-${seq}`)).resolves.toBeDefined();
    expect(errSpy).toHaveBeenCalledWith("Required form elements not found");
    errSpy.mockRestore();
  });
});
