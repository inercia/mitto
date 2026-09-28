/**
 * Unit tests for uiPromptDraftStore (mitto-osmb): per-session, per-request
 * drafts for MCP UI prompt inputs (mitto_ui_options free text,
 * mitto_ui_textbox, mitto_ui_form), so typed text survives switching
 * conversations while a prompt is still pending.
 */

import { describe, test, expect, beforeEach } from "./testing/testGlobals.js";
import {
  getUIPromptDraft,
  setUIPromptDraftField,
  clearUIPromptDraft,
  resolveUIPromptValues,
  collectFormValues,
  applyFormValues,
  _resetUIPromptDraftStoreForTests,
} from "./uiPromptDraftStore.js";

beforeEach(() => {
  _resetUIPromptDraftStoreForTests();
});

describe("get/set/clear", () => {
  test("returns null when nothing is stored", () => {
    expect(getUIPromptDraft("A", "r1")).toBeNull();
  });

  test("round-trips fields for the same session + request", () => {
    setUIPromptDraftField("A", "r1", "freeText", "hello");
    setUIPromptDraftField("A", "r1", "textbox", "edited");
    expect(getUIPromptDraft("A", "r1")).toEqual({
      freeText: "hello",
      textbox: "edited",
    });
  });

  test("is isolated between sessions", () => {
    setUIPromptDraftField("A", "r1", "freeText", "from A");
    setUIPromptDraftField("B", "r1", "freeText", "from B");
    expect(getUIPromptDraft("A", "r1").freeText).toBe("from A");
    expect(getUIPromptDraft("B", "r1").freeText).toBe("from B");
    clearUIPromptDraft("A");
    expect(getUIPromptDraft("A", "r1")).toBeNull();
    expect(getUIPromptDraft("B", "r1").freeText).toBe("from B");
  });

  test("returns null for a different requestId", () => {
    setUIPromptDraftField("A", "r1", "freeText", "hello");
    expect(getUIPromptDraft("A", "r2")).toBeNull();
  });

  test("a new requestId replaces (discards) the old entry", () => {
    setUIPromptDraftField("A", "r1", "freeText", "old");
    setUIPromptDraftField("A", "r1", "textbox", "old box");
    setUIPromptDraftField("A", "r2", "freeText", "new");
    expect(getUIPromptDraft("A", "r2")).toEqual({ freeText: "new" });
    expect(getUIPromptDraft("A", "r1")).toBeNull();
  });

  test("clear with a matching requestId removes the entry", () => {
    setUIPromptDraftField("A", "r1", "freeText", "hello");
    clearUIPromptDraft("A", "r1");
    expect(getUIPromptDraft("A", "r1")).toBeNull();
  });

  test("clear with a mismatched requestId is a no-op", () => {
    setUIPromptDraftField("A", "r1", "freeText", "hello");
    clearUIPromptDraft("A", "r2");
    expect(getUIPromptDraft("A", "r1").freeText).toBe("hello");
  });

  test("clear without requestId removes whatever is stored", () => {
    setUIPromptDraftField("A", "r1", "freeText", "hello");
    clearUIPromptDraft("A");
    expect(getUIPromptDraft("A", "r1")).toBeNull();
  });

  test("ignores writes without a requestId", () => {
    setUIPromptDraftField("A", undefined, "freeText", "hello");
    expect(getUIPromptDraft("A", undefined)).toBeNull();
  });

  test("numeric and string request ids are treated as equal", () => {
    setUIPromptDraftField("A", 7, "freeText", "hello");
    expect(getUIPromptDraft("A", "7").freeText).toBe("hello");
  });

  test("form values round-trip", () => {
    const form = { name: "Ada", subscribe: "true", color: "blue" };
    setUIPromptDraftField("A", "r1", "form", form);
    expect(getUIPromptDraft("A", "r1").form).toEqual(form);
  });
});

// Mirrors how ChatInput.js renders UI prompt inputs: values come from
// resolveUIPromptValues(sessionId, activeUIPrompt) on every render and each
// keystroke writes through via setUIPromptDraftField.
describe("conversation-switch scenarios", () => {
  const optionsPrompt = (requestId) => ({
    requestId,
    promptType: "options_buttons",
    allowFreeText: true,
    options: [{ id: "a", label: "A" }],
  });
  const textboxPrompt = (requestId, text) => ({
    requestId,
    promptType: "textbox",
    text,
  });

  test("options free text is restored after switching away and back", () => {
    const promptA = optionsPrompt("rA");
    expect(resolveUIPromptValues("A", promptA).freeText).toBe("");
    setUIPromptDraftField("A", "rA", "freeText", "my answer");

    // Switch to B (its own prompt, then no prompt).
    expect(resolveUIPromptValues("B", optionsPrompt("rB")).freeText).toBe("");
    expect(resolveUIPromptValues("B", null).freeText).toBe("");

    // Back to A with the same pending request.
    expect(resolveUIPromptValues("A", promptA).freeText).toBe("my answer");
  });

  test("options free text starts empty for a new request", () => {
    setUIPromptDraftField("A", "rA", "freeText", "my answer");
    expect(resolveUIPromptValues("A", optionsPrompt("rA2")).freeText).toBe("");
  });

  test("textbox edit is restored; new request falls back to server text", () => {
    const promptA = textboxPrompt("tA", "server text");
    expect(resolveUIPromptValues("A", promptA).textbox).toBe("server text");
    setUIPromptDraftField("A", "tA", "textbox", "server text, edited");

    expect(
      resolveUIPromptValues("B", textboxPrompt("tB", "other")).textbox,
    ).toBe("other");
    expect(resolveUIPromptValues("A", promptA).textbox).toBe(
      "server text, edited",
    );

    expect(
      resolveUIPromptValues("A", textboxPrompt("tA2", "fresh")).textbox,
    ).toBe("fresh");
  });

  test("an intentionally emptied textbox stays empty (not reset to server text)", () => {
    const promptA = textboxPrompt("tA", "server text");
    setUIPromptDraftField("A", "tA", "textbox", "");
    expect(resolveUIPromptValues("A", promptA).textbox).toBe("");
  });

  test("answering clears the draft", () => {
    const promptA = optionsPrompt("rA");
    setUIPromptDraftField("A", "rA", "freeText", "my answer");
    clearUIPromptDraft("A", "rA");
    expect(resolveUIPromptValues("A", promptA).freeText).toBe("");
  });

  test("defaults when there is no active prompt", () => {
    expect(resolveUIPromptValues("A", null)).toEqual({
      freeText: "",
      textbox: "",
      combo: "",
      form: null,
    });
  });
});

describe("form value helpers", () => {
  const FORM_HTML = `
    <input type="text" name="name" value="default">
    <textarea name="notes">initial</textarea>
    <select name="size">
      <option value="s">S</option>
      <option value="m">M</option>
      <option value="l">L</option>
    </select>
    <input type="checkbox" name="subscribe">
    <input type="radio" name="color" value="red" checked>
    <input type="radio" name="color" value="blue">
    <input type="file" name="upload">
    <input type="text" placeholder="unnamed">
  `;

  function makeForm() {
    const el = document.createElement("div");
    el.innerHTML = FORM_HTML;
    return el;
  }

  test("collects values in the submit shape", () => {
    const el = makeForm();
    const values = collectFormValues(el);
    expect(values).toEqual({
      name: "default",
      notes: "initial",
      size: "s",
      subscribe: "false",
      color: "red",
      upload: "",
    });
  });

  test("collect → apply round-trips onto a freshly injected form", () => {
    const original = makeForm();
    original.querySelector('[name="name"]').value = "Ada";
    original.querySelector('[name="notes"]').value = "some notes";
    original.querySelector('[name="size"]').value = "l";
    original.querySelector('[name="subscribe"]').checked = true;
    original.querySelector('[name="color"][value="blue"]').checked = true;
    const saved = collectFormValues(original);

    const remounted = makeForm();
    applyFormValues(remounted, saved);
    expect(collectFormValues(remounted)).toEqual(saved);
    expect(remounted.querySelector('[name="color"][value="red"]').checked).toBe(
      false,
    );
  });

  test("fields absent from the saved values keep their defaults", () => {
    const el = makeForm();
    applyFormValues(el, { name: "Ada" });
    expect(collectFormValues(el)).toEqual({
      name: "Ada",
      notes: "initial",
      size: "s",
      subscribe: "false",
      color: "red",
      upload: "",
    });
  });

  test("tolerates missing container or values", () => {
    expect(collectFormValues(null)).toEqual({});
    expect(() => applyFormValues(null, { a: "1" })).not.toThrow();
    expect(() => applyFormValues(makeForm(), null)).not.toThrow();
  });
});
