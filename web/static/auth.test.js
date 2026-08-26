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
    "</form>" +
    '<button id="passkeyBtn" style="display: none;">Sign in with a passkey</button>';
}

/** Makes isWebAuthnSupported() (utils/webauthn.js) return true for the
 *  duration of the test: a secure context + a stubbed PublicKeyCredential. */
function stubWebAuthnSupported() {
  const orig = Object.getOwnPropertyDescriptor(window, "isSecureContext");
  Object.defineProperty(window, "isSecureContext", {
    value: true,
    configurable: true,
  });
  window.PublicKeyCredential = function () {};
  return () => {
    delete window.PublicKeyCredential;
    if (orig) Object.defineProperty(window, "isSecureContext", orig);
  };
}

/** Makes supportsConditionalCreate() (utils/webauthn.js) resolve true: a
 *  secure context + PublicKeyCredential.getClientCapabilities() reporting
 *  conditionalCreate. Builds on stubWebAuthnSupported(). */
function stubConditionalCreateSupported() {
  const restoreSupported = stubWebAuthnSupported();
  window.PublicKeyCredential.getClientCapabilities = async () => ({
    conditionalCreate: true,
  });
  return restoreSupported;
}

/** Makes isConditionalMediationAvailable() (utils/webauthn.js) resolve
 *  true: a secure context + PublicKeyCredential.isConditionalMediationAvailable()
 *  resolving true. Builds on stubWebAuthnSupported(). */
function stubConditionalMediationAvailable() {
  const restoreSupported = stubWebAuthnSupported();
  window.PublicKeyCredential.isConditionalMediationAvailable = async () =>
    true;
  return restoreSupported;
}

async function flush() {
  // 50 microtask ticks: the passkey auto-enroll chain (mitto-4mz.7) chains
  // considerably more sequential awaits than a plain login (supportsConditional
  // Create -> registerBegin fetch+decode -> credentials.create() ->
  // registerFinish fetch+decode) — 10 ticks left it unsettled by assertion
  // time, so the leftover promise chain resolved during a LATER test and
  // leaked its sessionStorage flag across tests.
  for (let i = 0; i < 50; i++) await Promise.resolve();
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

  // mitto-4mz: on the loopback/internal listener (native macOS app) the
  // backend bypasses auth, so the login page must never be shown. The server
  // injects window.mittoIsExternal === false there; auth.js redirects straight
  // back to the app instead of rendering the form. This unsticks a WKWebView
  // that restored a stale /auth.html URL across relaunch. External access
  // (true) and the plain browser/test default (undefined) still show the form.
  describe("loopback/native-app guard (window.mittoIsExternal === false)", () => {
    afterEach(() => {
      delete window.mittoIsExternal;
    });

    test("mittoIsExternal === false: redirects to / and never fetches auth-info", async () => {
      let fetched = false;
      globalThis.fetch = async () => {
        fetched = true;
        return fakeResponse({ body: { simple: true } });
      };
      window.mittoIsExternal = false;
      await loadAuthPage();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      expect(fetched).toBe(false);
    });

    test("mittoIsExternal === false honors the API prefix", async () => {
      globalThis.fetch = async () => fakeResponse({ body: { simple: true } });
      window.mittoApiPrefix = "/mitto";
      window.mittoIsExternal = false;
      await loadAuthPage();
      expect(window.location.href).toBe(new URL("/mitto/", originalHref).href);
    });

    test("mittoIsExternal === true (external access): still renders the login form", async () => {
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: true, cloudflare: false } });
      window.mittoIsExternal = true;
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

  describe("passkey login (mitto-4mz.6)", () => {
    let restoreWebAuthn;
    let originalCredentials;

    beforeEach(() => {
      originalCredentials = navigator.credentials;
    });

    afterEach(() => {
      if (restoreWebAuthn) restoreWebAuthn();
      restoreWebAuthn = undefined;
      Object.defineProperty(navigator, "credentials", {
        value: originalCredentials,
        configurable: true,
      });
    });

    test("stays hidden when the browser does not support WebAuthn, even if armed", async () => {
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: true, passkey: true } });
      await loadAuthPage();
      expect(document.getElementById("passkeyBtn").style.display).toBe(
        "none",
      );
    });

    test("stays hidden when supported but auth-info reports passkey:false", async () => {
      restoreWebAuthn = stubWebAuthnSupported();
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: true, passkey: false } });
      await loadAuthPage();
      expect(document.getElementById("passkeyBtn").style.display).toBe(
        "none",
      );
    });

    test("shown when armed (passkey:true) and the browser supports WebAuthn", async () => {
      restoreWebAuthn = stubWebAuthnSupported();
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: true, passkey: true } });
      await loadAuthPage();
      expect(document.getElementById("passkeyBtn").style.display).toBe("");
    });

    test("successful get() ceremony posts the assertion and redirects to /", async () => {
      restoreWebAuthn = stubWebAuthnSupported();
      const assertion = {
        id: "AQID",
        rawId: new Uint8Array([1, 2, 3]).buffer,
        type: "public-key",
        response: {
          clientDataJSON: new Uint8Array([4]).buffer,
          authenticatorData: new Uint8Array([5]).buffer,
          signature: new Uint8Array([6]).buffer,
          userHandle: null,
        },
      };
      Object.defineProperty(navigator, "credentials", {
        value: { get: jest.fn(async () => assertion) },
        configurable: true,
      });
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        if (u.includes("/api/webauthn/login/finish"))
          return fakeResponse({ body: { success: true } });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      document.getElementById("passkeyBtn").click();
      await flush();
      expect(navigator.credentials.get).toHaveBeenCalledTimes(1);
      expect(window.location.href).toBe(new URL("/", originalHref).href);
    });

    test("user cancel (NotAllowedError) resets the button without an error message", async () => {
      restoreWebAuthn = stubWebAuthnSupported();
      Object.defineProperty(navigator, "credentials", {
        value: {
          get: jest.fn(async () => {
            throw Object.assign(new Error("cancelled"), {
              name: "NotAllowedError",
            });
          }),
        },
        configurable: true,
      });
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      const btn = document.getElementById("passkeyBtn");
      btn.click();
      await flush();
      expect(document.getElementById("error").classList.contains("hidden")).toBe(
        true,
      );
      expect(btn.disabled).toBe(false);
    });

    test("a 429 from login/finish shows a rate-limited message", async () => {
      restoreWebAuthn = stubWebAuthnSupported();
      Object.defineProperty(navigator, "credentials", {
        value: {
          get: jest.fn(async () => ({
            id: "AQID",
            rawId: new Uint8Array([1]).buffer,
            type: "public-key",
            response: {
              clientDataJSON: new Uint8Array([1]).buffer,
              authenticatorData: new Uint8Array([1]).buffer,
              signature: new Uint8Array([1]).buffer,
              userHandle: null,
            },
          })),
        },
        configurable: true,
      });
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        if (u.includes("/api/webauthn/login/finish"))
          return fakeResponse({
            status: 429,
            body: { error: "Too many attempts. Please try again later." },
          });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      document.getElementById("passkeyBtn").click();
      await flush();
      const errorDiv = document.getElementById("error");
      expect(errorDiv.classList.contains("hidden")).toBe(false);
      expect(errorDiv.textContent).toBe(
        "Too many attempts. Please try again later.",
      );
    });
  });

  describe("passkey conditional mediation autofill (mitto-ykm)", () => {
    let restoreConditionalMediation;

    afterEach(() => {
      if (restoreConditionalMediation) restoreConditionalMediation();
      restoreConditionalMediation = undefined;
    });

    test("conditional mediation available: starts a background get() with mediation:'conditional' and a signal, posts the assertion, and redirects to / without ever revealing passkeyBtn", async () => {
      restoreConditionalMediation = stubConditionalMediationAvailable();
      const assertion = {
        id: "AQID",
        rawId: new Uint8Array([1, 2, 3]).buffer,
        type: "public-key",
        response: {
          clientDataJSON: new Uint8Array([4]).buffer,
          authenticatorData: new Uint8Array([5]).buffer,
          signature: new Uint8Array([6]).buffer,
          userHandle: null,
        },
      };
      const getMock = jest.fn(async () => assertion);
      Object.defineProperty(navigator, "credentials", {
        value: { get: getMock },
        configurable: true,
      });
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        if (u.includes("/api/webauthn/login/finish"))
          return fakeResponse({ body: { success: true } });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      expect(getMock).toHaveBeenCalledTimes(1);
      const callArgs = getMock.mock.calls[0][0];
      expect(callArgs.mediation).toBe("conditional");
      expect(callArgs.signal).toBeInstanceOf(AbortSignal);
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      // The explicit button is the degradation fallback only; it must never
      // be revealed when the browser offers conditional mediation.
      expect(document.getElementById("passkeyBtn").style.display).toBe(
        "none",
      );
    });

    test("conditional mediation unavailable: falls back to revealing #passkeyBtn, no background get() is attempted", async () => {
      // stubWebAuthnSupported() alone leaves PublicKeyCredential without an
      // isConditionalMediationAvailable static, so isConditionalMediationAvailable()
      // resolves false and the degradation fallback (the explicit button) applies.
      restoreConditionalMediation = stubWebAuthnSupported();
      const getMock = jest.fn(async () => {
        throw new Error("background get() should not be called");
      });
      Object.defineProperty(navigator, "credentials", {
        value: { get: getMock },
        configurable: true,
      });
      globalThis.fetch = async () =>
        fakeResponse({ body: { simple: true, passkey: true } });
      await loadAuthPage();
      expect(getMock).not.toHaveBeenCalled();
      expect(document.getElementById("passkeyBtn").style.display).toBe("");
    });

    test("AbortError/NotAllowedError from the background get() is swallowed silently (no error shown)", async () => {
      restoreConditionalMediation = stubConditionalMediationAvailable();
      Object.defineProperty(navigator, "credentials", {
        value: {
          get: jest.fn(async () => {
            throw Object.assign(new Error("dismissed"), {
              name: "NotAllowedError",
            });
          }),
        },
        configurable: true,
      });
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      expect(document.getElementById("error").classList.contains("hidden")).toBe(
        true,
      );
      // The password form must remain fully usable, untouched by the failed
      // background ceremony.
      expect(document.getElementById("submitBtn").disabled).toBe(false);
    });

    test("a 429 from the background get()'s login/finish surfaces a rate-limited message without disrupting the password form", async () => {
      restoreConditionalMediation = stubConditionalMediationAvailable();
      Object.defineProperty(navigator, "credentials", {
        value: {
          get: jest.fn(async () => ({
            id: "AQID",
            rawId: new Uint8Array([1]).buffer,
            type: "public-key",
            response: {
              clientDataJSON: new Uint8Array([1]).buffer,
              authenticatorData: new Uint8Array([1]).buffer,
              signature: new Uint8Array([1]).buffer,
              userHandle: null,
            },
          })),
        },
        configurable: true,
      });
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        if (u.includes("/api/webauthn/login/finish"))
          return fakeResponse({
            status: 429,
            body: { error: "Too many attempts. Please try again later." },
          });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      const errorDiv = document.getElementById("error");
      expect(errorDiv.classList.contains("hidden")).toBe(false);
      expect(errorDiv.textContent).toBe(
        "Too many attempts. Please try again later.",
      );
      expect(document.getElementById("submitBtn").disabled).toBe(false);
    });

    test("abort-before-create ordering: a password login aborts the pending conditional get() before starting its own conditional create()", async () => {
      restoreConditionalMediation = stubConditionalMediationAvailable();
      // Also arm Conditional Create so attemptConditionalCreate() proceeds
      // to its own create() ceremony after the password login succeeds.
      window.PublicKeyCredential.getClientCapabilities = async () => ({
        conditionalCreate: true,
      });
      document.cookie = "mitto_csrf=csrf-test-token; path=/";

      const order = [];
      const fakeCredential = {
        id: "AQID",
        rawId: new Uint8Array([1, 2, 3]).buffer,
        type: "public-key",
        response: {
          clientDataJSON: new Uint8Array([4]).buffer,
          attestationObject: new Uint8Array([5]).buffer,
        },
      };
      Object.defineProperty(navigator, "credentials", {
        value: {
          get: jest.fn(
            (opts) =>
              new Promise((_resolve, reject) => {
                order.push("get-called");
                opts.signal.addEventListener("abort", () => {
                  order.push("get-aborted");
                  reject(
                    Object.assign(new Error("aborted"), {
                      name: "AbortError",
                    }),
                  );
                });
              }),
          ),
          create: jest.fn(async () => {
            order.push("create-called");
            return fakeCredential;
          }),
        },
        configurable: true,
      });

      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/login"))
          return fakeResponse({ status: 200, body: { success: true } });
        if (u.includes("/api/webauthn/login/begin"))
          return fakeResponse({ body: { publicKey: { challenge: "AQID" } } });
        if (u.includes("/api/webauthn/register/begin"))
          return fakeResponse({
            body: {
              publicKey: { challenge: "AQID", user: { id: "AQID" } },
            },
          });
        if (u.includes("/api/webauthn/register/finish"))
          return fakeResponse({ status: 200, body: { success: true } });
        throw new Error("unexpected url " + u);
      };

      await loadAuthPage();
      // The background get() must be pending (started on page load) before
      // the password login runs.
      expect(order).toEqual(["get-called"]);

      submitLogin("alice", "hunter2");
      await flush();

      expect(order).toEqual(["get-called", "get-aborted", "create-called"]);
      expect(window.location.href).toBe(new URL("/", originalHref).href);

      sessionStorage.removeItem("mitto_passkey_autoenrolled");
      document.cookie =
        "mitto_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    });
  });

  describe("passkey auto-enroll (Conditional Create, mitto-4mz.7)", () => {
    let restoreConditionalCreate;

    function setCsrfCookie(token = "csrf-test-token") {
      document.cookie = `mitto_csrf=${token}; path=/`;
    }

    function clearCsrfCookie() {
      document.cookie = "mitto_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    }

    beforeEach(() => {
      setCsrfCookie();
    });

    afterEach(() => {
      if (restoreConditionalCreate) restoreConditionalCreate();
      restoreConditionalCreate = undefined;
      clearCsrfCookie();
    });

    /** Fetch mock covering auth-info/login plus register/begin+finish. */
    function mockFetchWithRegister({
      registerBeginBody = { publicKey: { challenge: "AQID", user: { id: "AQID" } } },
      registerBeginStatus = 200,
      registerFinishStatus = 200,
      registerFinishBody = { success: true },
      onRegisterBegin,
      onRegisterFinish,
    } = {}) {
      globalThis.fetch = async (url, init) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/login"))
          return fakeResponse({ status: 200, body: { success: true } });
        if (u.includes("/api/webauthn/register/begin")) {
          onRegisterBegin?.(init);
          return fakeResponse({
            status: registerBeginStatus,
            body: registerBeginBody,
          });
        }
        if (u.includes("/api/webauthn/register/finish")) {
          onRegisterFinish?.(init);
          return fakeResponse({
            status: registerFinishStatus,
            body: registerFinishBody,
          });
        }
        throw new Error("unexpected url " + u);
      };
    }

    function stubCredentialsCreate(impl) {
      const original = navigator.credentials;
      Object.defineProperty(navigator, "credentials", {
        value: { ...original, create: jest.fn(impl) },
        configurable: true,
      });
      return () => {
        Object.defineProperty(navigator, "credentials", {
          value: original,
          configurable: true,
        });
      };
    }

    const fakeCredential = {
      id: "AQID",
      rawId: new Uint8Array([1, 2, 3]).buffer,
      type: "public-key",
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        attestationObject: new Uint8Array([5]).buffer,
      },
    };

    test("unsupported browser: skips the ceremony entirely, still redirects, no flag set", async () => {
      // isWebAuthnSupported() stays false (no PublicKeyCredential stub).
      mockFetchWithRegister({
        onRegisterBegin: () => {
          throw new Error("register/begin should not be called");
        },
      });
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBeNull();
    });

    test("supported + armed: runs create() with mediation:'conditional', posts to register/finish, sets the flag, redirects", async () => {
      restoreConditionalCreate = stubConditionalCreateSupported();
      const restoreCredentials = stubCredentialsCreate(async () => fakeCredential);
      let beginCsrfHeader;
      let finishBody;
      mockFetchWithRegister({
        onRegisterBegin: (init) => {
          beginCsrfHeader = init.headers?.["X-CSRF-Token"];
        },
        onRegisterFinish: (init) => {
          finishBody = JSON.parse(init.body);
        },
      });

      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();

      expect(navigator.credentials.create).toHaveBeenCalledTimes(1);
      const callArgs = navigator.credentials.create.mock.calls[0][0];
      expect(callArgs.mediation).toBe("conditional");
      expect(callArgs.publicKey.challenge).toBeInstanceOf(ArrayBuffer);
      expect(beginCsrfHeader).toBe("csrf-test-token");
      expect(finishBody.id).toBe("AQID");
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBe("1");
      expect(window.location.href).toBe(new URL("/", originalHref).href);

      sessionStorage.removeItem("mitto_passkey_autoenrolled");
      restoreCredentials();
    });

    test("capability check false (e.g. conditionalCreate:false): never calls register/begin, no flag set", async () => {
      restoreConditionalCreate = stubWebAuthnSupported();
      window.PublicKeyCredential.getClientCapabilities = async () => ({
        conditionalCreate: false,
      });
      mockFetchWithRegister({
        onRegisterBegin: () => {
          throw new Error("register/begin should not be called");
        },
      });
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBeNull();
    });

    test("null credential (no discoverable state): skips register/finish and the flag, still redirects", async () => {
      restoreConditionalCreate = stubConditionalCreateSupported();
      const restoreCredentials = stubCredentialsCreate(async () => null);
      let finishCalled = false;
      mockFetchWithRegister({
        onRegisterFinish: () => {
          finishCalled = true;
        },
      });
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(finishCalled).toBe(false);
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBeNull();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      restoreCredentials();
    });

    test("registerBegin failure is swallowed: login still redirects, no flag set", async () => {
      restoreConditionalCreate = stubConditionalCreateSupported();
      globalThis.fetch = async (url) => {
        const u = String(url);
        if (u.includes("/api/auth-info"))
          return fakeResponse({ body: { simple: true, passkey: true } });
        if (u.includes("/api/login"))
          return fakeResponse({ status: 200, body: { success: true } });
        if (u.includes("/api/webauthn/register/begin"))
          return fakeResponse({ status: 500, body: { error: "boom" } });
        throw new Error("unexpected url " + u);
      };
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBeNull();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
    });

    test("navigator.credentials.create() rejection (e.g. user cancel) is swallowed: login still redirects", async () => {
      restoreConditionalCreate = stubConditionalCreateSupported();
      const restoreCredentials = stubCredentialsCreate(async () => {
        throw Object.assign(new Error("cancelled"), { name: "NotAllowedError" });
      });
      mockFetchWithRegister();
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBeNull();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      restoreCredentials();
    });

    test("registerFinish failure is swallowed: no flag set, login still redirects", async () => {
      restoreConditionalCreate = stubConditionalCreateSupported();
      const restoreCredentials = stubCredentialsCreate(async () => fakeCredential);
      mockFetchWithRegister({ registerFinishStatus: 500, registerFinishBody: { error: "boom" } });
      await loadAuthPage();
      submitLogin("alice", "hunter2");
      await flush();
      expect(sessionStorage.getItem("mitto_passkey_autoenrolled")).toBeNull();
      expect(window.location.href).toBe(new URL("/", originalHref).href);
      restoreCredentials();
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
