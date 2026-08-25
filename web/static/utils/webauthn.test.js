/**
 * Unit tests for utils/webauthn.js — native WebAuthn (passkey) helpers
 * (mitto-4mz.6). Covers feature detection, base64url round-tripping, and the
 * ceremony option/result (de)serializers shared by auth.js and
 * SettingsDialog.js.
 */
import { describe, test, expect } from "./testing/testGlobals.js";
import {
  isWebAuthnSupported,
  base64urlToBuffer,
  bufferToBase64url,
  decodeCreationOptions,
  decodeRequestOptions,
  serializeCreatedCredential,
  serializeAssertion,
} from "./webauthn.js";

describe("isWebAuthnSupported", () => {
  test("true when secure context + PublicKeyCredential are both present", () => {
    const orig = Object.getOwnPropertyDescriptor(window, "isSecureContext");
    Object.defineProperty(window, "isSecureContext", {
      value: true,
      configurable: true,
    });
    window.PublicKeyCredential = function () {};
    expect(isWebAuthnSupported()).toBe(true);
    delete window.PublicKeyCredential;
    if (orig) Object.defineProperty(window, "isSecureContext", orig);
  });

  test("false when the context is not secure", () => {
    const orig = Object.getOwnPropertyDescriptor(window, "isSecureContext");
    Object.defineProperty(window, "isSecureContext", {
      value: false,
      configurable: true,
    });
    window.PublicKeyCredential = function () {};
    expect(isWebAuthnSupported()).toBe(false);
    delete window.PublicKeyCredential;
    if (orig) Object.defineProperty(window, "isSecureContext", orig);
  });

  test("false when PublicKeyCredential is not defined", () => {
    const orig = Object.getOwnPropertyDescriptor(window, "isSecureContext");
    Object.defineProperty(window, "isSecureContext", {
      value: true,
      configurable: true,
    });
    delete window.PublicKeyCredential;
    expect(isWebAuthnSupported()).toBe(false);
    if (orig) Object.defineProperty(window, "isSecureContext", orig);
  });
});

describe("base64url round-trip", () => {
  test("bufferToBase64url -> base64urlToBuffer returns the original bytes", () => {
    const original = new Uint8Array([0, 1, 2, 253, 254, 255, 16, 32]);
    const encoded = bufferToBase64url(original.buffer);
    const decoded = new Uint8Array(base64urlToBuffer(encoded));
    expect(Array.from(decoded)).toEqual(Array.from(original));
  });

  test("encoding never contains padding or +/ characters", () => {
    const bytes = new Uint8Array(37).fill(0xff); // odd length forces padding in std base64
    const encoded = bufferToBase64url(bytes.buffer);
    expect(encoded).not.toMatch(/[+/=]/);
  });

  test("decodes a value with no padding chars back to the exact byte length", () => {
    // "AQID" = base64url("\x01\x02\x03"), 3 bytes, no padding needed.
    const decoded = new Uint8Array(base64urlToBuffer("AQID"));
    expect(Array.from(decoded)).toEqual([1, 2, 3]);
  });
});

describe("decodeCreationOptions", () => {
  test("decodes challenge, user.id, and excludeCredentials[].id to ArrayBuffers", () => {
    const json = {
      publicKey: {
        rp: { id: "example.org", name: "Mitto" },
        challenge: "AQID", // [1,2,3]
        user: { id: "AQID", name: "admin", displayName: "admin" },
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        excludeCredentials: [{ id: "AQID", type: "public-key" }],
      },
    };
    const publicKey = decodeCreationOptions(json);
    expect(publicKey.challenge).toBeInstanceOf(ArrayBuffer);
    expect(Array.from(new Uint8Array(publicKey.challenge))).toEqual([1, 2, 3]);
    expect(publicKey.user.id).toBeInstanceOf(ArrayBuffer);
    expect(publicKey.user.name).toBe("admin");
    expect(publicKey.excludeCredentials[0].id).toBeInstanceOf(ArrayBuffer);
    expect(publicKey.rp.id).toBe("example.org");
  });

  test("tolerates a missing excludeCredentials array", () => {
    const json = {
      publicKey: { challenge: "AQID", user: { id: "AQID" } },
    };
    expect(() => decodeCreationOptions(json)).not.toThrow();
  });
});

describe("decodeRequestOptions", () => {
  test("decodes challenge and allowCredentials[].id to ArrayBuffers", () => {
    const json = {
      publicKey: {
        challenge: "AQID",
        allowCredentials: [{ id: "AQID", type: "public-key" }],
      },
    };
    const publicKey = decodeRequestOptions(json);
    expect(publicKey.challenge).toBeInstanceOf(ArrayBuffer);
    expect(publicKey.allowCredentials[0].id).toBeInstanceOf(ArrayBuffer);
  });

  test("tolerates a missing allowCredentials array (discoverable login)", () => {
    const json = { publicKey: { challenge: "AQID" } };
    const publicKey = decodeRequestOptions(json);
    expect(publicKey.allowCredentials).toBeUndefined();
  });
});

describe("serializeCreatedCredential", () => {
  test("serializes id/rawId/type/response fields to the W3C JSON shape", () => {
    const raw = new Uint8Array([1, 2, 3]).buffer;
    const credential = {
      id: "AQID",
      rawId: raw,
      type: "public-key",
      response: {
        clientDataJSON: new Uint8Array([4, 5]).buffer,
        attestationObject: new Uint8Array([6, 7]).buffer,
      },
    };
    const out = serializeCreatedCredential(credential);
    expect(out).toEqual({
      id: "AQID",
      rawId: bufferToBase64url(raw),
      type: "public-key",
      response: {
        clientDataJSON: bufferToBase64url(credential.response.clientDataJSON),
        attestationObject: bufferToBase64url(
          credential.response.attestationObject,
        ),
      },
    });
  });
});

describe("serializeAssertion", () => {
  test("serializes id/rawId/type/response fields, including userHandle when present", () => {
    const credential = {
      id: "AQID",
      rawId: new Uint8Array([1, 2, 3]).buffer,
      type: "public-key",
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        authenticatorData: new Uint8Array([5]).buffer,
        signature: new Uint8Array([6]).buffer,
        userHandle: new Uint8Array([7, 8]).buffer,
      },
    };
    const out = serializeAssertion(credential);
    expect(out.response.userHandle).toBe(
      bufferToBase64url(credential.response.userHandle),
    );
  });

  test("userHandle is undefined when the authenticator does not return one", () => {
    const credential = {
      id: "AQID",
      rawId: new Uint8Array([1]).buffer,
      type: "public-key",
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        authenticatorData: new Uint8Array([5]).buffer,
        signature: new Uint8Array([6]).buffer,
        userHandle: null,
      },
    };
    const out = serializeAssertion(credential);
    expect(out.response.userHandle).toBeUndefined();
  });
});
