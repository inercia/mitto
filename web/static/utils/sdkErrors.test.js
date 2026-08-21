/**
 * Unit tests for the SDK error adapter (mitto-7gta.17 slice S0).
 */
import {
  errorStatus,
  isNotFoundError,
  errorMessage,
  beadsErrorFrom,
} from "./sdkErrors.js";
import { MittoApiError, MittoAuthError, MittoNetworkError } from "../sdk/index.js";

describe("sdkErrors", () => {
  describe("errorStatus", () => {
    test("returns the status of a MittoApiError", () => {
      const err = new MittoApiError("nope", { status: 404 });
      expect(errorStatus(err)).toBe(404);
    });

    test("returns undefined for an error without a numeric status", () => {
      expect(errorStatus(new MittoNetworkError("boom"))).toBeUndefined();
      expect(errorStatus(new Error("plain"))).toBeUndefined();
      expect(errorStatus(null)).toBeUndefined();
      expect(errorStatus(undefined)).toBeUndefined();
    });
  });

  describe("isNotFoundError", () => {
    test("true for a 404 MittoApiError", () => {
      expect(isNotFoundError(new MittoApiError("gone", { status: 404 }))).toBe(
        true,
      );
    });

    test("false for other statuses and non-SDK errors", () => {
      expect(isNotFoundError(new MittoApiError("no", { status: 500 }))).toBe(
        false,
      );
      expect(isNotFoundError(new MittoAuthError("no", { status: 401 }))).toBe(
        false,
      );
      expect(isNotFoundError(new Error("plain"))).toBe(false);
    });
  });

  describe("errorMessage", () => {
    test("returns the error's message when present", () => {
      const err = new MittoApiError("Failed to load issue", { status: 500 });
      expect(errorMessage(err, "fallback")).toBe("Failed to load issue");
    });

    test("falls back when the error has no usable message", () => {
      const err = new MittoApiError("", { status: 500 });
      expect(errorMessage(err, "fallback")).toBe("fallback");
      expect(errorMessage(null, "fallback")).toBe("fallback");
      expect(errorMessage(undefined, "fallback")).toBe("fallback");
    });
  });

  describe("beadsErrorFrom", () => {
    test("maps a MittoApiError's status/code/details/message onto the flat beads shape", () => {
      const err = new MittoApiError("Issue not found", {
        status: 404,
        code: "not_found",
        details: { stderr: "bd: no such issue" },
      });
      expect(beadsErrorFrom(err)).toEqual({
        error: "Issue not found",
        code: "not_found",
        stderr: "bd: no such issue",
        details: { stderr: "bd: no such issue" },
      });
    });

    test("omits stderr when details carries none", () => {
      const err = new MittoApiError("Bad request", {
        status: 400,
        code: "bad_request",
      });
      const flat = beadsErrorFrom(err);
      expect(flat.stderr).toBeUndefined();
      expect(flat.details).toBeUndefined();
    });

    test("uses the fallback message when the error carries none", () => {
      const err = new MittoApiError("", { status: 500 });
      expect(beadsErrorFrom(err, "Failed to load issue").error).toBe(
        "Failed to load issue",
      );
    });

    test("defaults the fallback message to 'Request failed'", () => {
      const err = new MittoApiError("", { status: 500 });
      expect(beadsErrorFrom(err).error).toBe("Request failed");
    });
  });
});
