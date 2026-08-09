/**
 * Unit tests for SavePromptDialog's save/check-exists flow (mitto-7gta.17
 * slice S5: migrated from authFetch/secureFetch onto getSdkClient()).
 *
 * Because the component imports window.preact globals at module load, it
 * cannot be imported under jsdom. Following the pattern used by
 * ChatInput.test.js, LoopFrequencyPanel.test.js and
 * PromptParameterDialog.test.js, `doSave`/`handleSave` are duplicated here
 * (dependencies injected) and tested directly — keep in sync with
 * SavePromptDialog.js.
 */

// Jest is not injected as a global under --experimental-vm-modules (ESM); we
// must import it explicitly. testGlobals.js re-exports the lifecycle globals
// and `jest` from whichever runner is active (Jest or bun:test).
import { describe, test, expect, jest } from "../utils/testing/testGlobals.js";
import { errorMessage } from "../utils/sdkErrors.js";

// =============================================================================
// Duplicated doSave/handleSave logic.
// Mirrors web/static/components/SavePromptDialog.js:157-212 — keep in sync.
// `misc` mirrors `getSdkClient().misc` — `{saveFileToPath(path, content),
// checkFileExists(path)}` — both of which throw on any non-2xx
// (MittoApiError) instead of returning `{ok: false}`.
// =============================================================================

function makeDoSave({
  fullPath,
  content = "content",
  misc,
  onClose = () => {},
  setIsSaving = () => {},
  setError = () => {},
}) {
  return async function doSave() {
    if (!fullPath) return;
    setIsSaving(true);
    setError("");
    try {
      await misc.saveFileToPath(fullPath, content);
      onClose?.();
    } catch (err) {
      setError(errorMessage(err, "Failed to save file"));
    } finally {
      setIsSaving(false);
    }
  };
}

function makeHandleSave({
  name,
  fullPath,
  misc,
  doSave,
  setError = () => {},
  setIsSaving = () => {},
  setShowOverwriteConfirm = () => {},
}) {
  return async function handleSave() {
    if (!name.trim()) {
      setError("Name is required");
      return;
    }
    if (!fullPath) {
      setError("Invalid file path");
      return;
    }
    setError("");
    setIsSaving(true);
    try {
      const data = await misc.checkFileExists(fullPath);
      if (data?.exists) {
        setIsSaving(false);
        setShowOverwriteConfirm(true);
        return;
      }
      setIsSaving(false);
      await doSave();
    } catch (_err) {
      // If the existence check fails, try to save anyway.
      setIsSaving(false);
      await doSave();
    }
  };
}

// =============================================================================
// doSave
// =============================================================================

describe("doSave (misc.saveFileToPath migration)", () => {
  test("no-op when fullPath is empty", async () => {
    const misc = { saveFileToPath: jest.fn() };
    const handler = makeDoSave({ fullPath: "", misc });
    await handler();
    expect(misc.saveFileToPath).not.toHaveBeenCalled();
  });

  test("success: saves content to fullPath and closes the dialog", async () => {
    const misc = { saveFileToPath: jest.fn(async () => ({})) };
    const onClose = jest.fn();
    const setIsSaving = jest.fn();

    const handler = makeDoSave({
      fullPath: "/repo/.mitto/prompts/x.prompt.yaml",
      content: "name: x\n",
      misc,
      onClose,
      setIsSaving,
    });
    await handler();

    expect(misc.saveFileToPath).toHaveBeenCalledWith(
      "/repo/.mitto/prompts/x.prompt.yaml",
      "name: x\n",
    );
    expect(onClose).toHaveBeenCalled();
    expect(setIsSaving).toHaveBeenCalledWith(true);
    expect(setIsSaving).toHaveBeenLastCalledWith(false);
  });

  test("failure: a rejected saveFileToPath (MittoApiError) surfaces its message and does NOT close", async () => {
    const err = new Error("disk full");
    const misc = {
      saveFileToPath: jest.fn(async () => {
        throw err;
      }),
    };
    const onClose = jest.fn();
    const setError = jest.fn();

    const handler = makeDoSave({
      fullPath: "/repo/x.prompt.yaml",
      misc,
      onClose,
      setError,
    });
    await handler();

    expect(setError).toHaveBeenCalledWith("disk full");
    expect(onClose).not.toHaveBeenCalled();
  });

  test("failure: an error with no message falls back to the default text", async () => {
    const misc = {
      saveFileToPath: jest.fn(async () => {
        throw new Error();
      }),
    };
    const setError = jest.fn();

    const handler = makeDoSave({
      fullPath: "/repo/x.prompt.yaml",
      misc,
      setError,
    });
    await handler();

    expect(setError).toHaveBeenCalledWith("Failed to save file");
  });
});

// =============================================================================
// handleSave
// =============================================================================

describe("handleSave (misc.checkFileExists migration)", () => {
  test("validation: blank name short-circuits before any SDK call", async () => {
    const misc = { checkFileExists: jest.fn() };
    const doSave = jest.fn();
    const setError = jest.fn();

    const handler = makeHandleSave({
      name: "   ",
      fullPath: "/repo/x.prompt.yaml",
      misc,
      doSave,
      setError,
    });
    await handler();

    expect(setError).toHaveBeenCalledWith("Name is required");
    expect(misc.checkFileExists).not.toHaveBeenCalled();
    expect(doSave).not.toHaveBeenCalled();
  });

  test("validation: empty fullPath short-circuits before any SDK call", async () => {
    const misc = { checkFileExists: jest.fn() };
    const doSave = jest.fn();
    const setError = jest.fn();

    const handler = makeHandleSave({
      name: "My Prompt",
      fullPath: "",
      misc,
      doSave,
      setError,
    });
    await handler();

    expect(setError).toHaveBeenCalledWith("Invalid file path");
    expect(misc.checkFileExists).not.toHaveBeenCalled();
  });

  test("file does not exist: saves directly without prompting for overwrite", async () => {
    const misc = { checkFileExists: jest.fn(async () => ({ exists: false })) };
    const doSave = jest.fn(async () => {});
    const setShowOverwriteConfirm = jest.fn();

    const handler = makeHandleSave({
      name: "My Prompt",
      fullPath: "/repo/x.prompt.yaml",
      misc,
      doSave,
      setShowOverwriteConfirm,
    });
    await handler();

    expect(misc.checkFileExists).toHaveBeenCalledWith("/repo/x.prompt.yaml");
    expect(doSave).toHaveBeenCalled();
    expect(setShowOverwriteConfirm).not.toHaveBeenCalled();
  });

  test("file exists: shows the overwrite confirmation instead of saving", async () => {
    const misc = { checkFileExists: jest.fn(async () => ({ exists: true })) };
    const doSave = jest.fn(async () => {});
    const setShowOverwriteConfirm = jest.fn();

    const handler = makeHandleSave({
      name: "My Prompt",
      fullPath: "/repo/x.prompt.yaml",
      misc,
      doSave,
      setShowOverwriteConfirm,
    });
    await handler();

    expect(setShowOverwriteConfirm).toHaveBeenCalledWith(true);
    expect(doSave).not.toHaveBeenCalled();
  });

  test("a rejected checkFileExists (MittoApiError/MittoNetworkError) falls back to saving anyway", async () => {
    const misc = {
      checkFileExists: jest.fn(async () => {
        throw new Error("network down");
      }),
    };
    const doSave = jest.fn(async () => {});
    const setShowOverwriteConfirm = jest.fn();

    const handler = makeHandleSave({
      name: "My Prompt",
      fullPath: "/repo/x.prompt.yaml",
      misc,
      doSave,
      setShowOverwriteConfirm,
    });
    await handler();

    expect(doSave).toHaveBeenCalled();
    expect(setShowOverwriteConfirm).not.toHaveBeenCalled();
  });
});
