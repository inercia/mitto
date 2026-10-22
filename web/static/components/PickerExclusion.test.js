import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

if (process.env.MITTO_PICKER_EXCLUSION_TEST_CHILD === "1") {
  test("opening one composer picker closes the other", async () => {
    const preact = await import("../vendor/preact.js");
    const hooks = await import("../vendor/preact-hooks.js");
    const { default: htm } = await import("../vendor/htm.js");
    const previous = window.preact;
    window.preact = {
      ...preact,
      ...hooks,
      html: htm.bind(preact.h),
      memo: (component) => component,
    };

    const { ChatInput } = await import("./ChatInput.js");
    const { ConfigOptionSelect } = await import("./ConfigOptionSelect.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    const waitFor = async (predicate, description) => {
      const deadline = Date.now() + 3000;
      while (!predicate()) {
        if (Date.now() > deadline) {
          throw new Error(`Timed out waiting for ${description}`);
        }
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
    };

    try {
      preact.render(
        preact.h(
          preact.Fragment,
          null,
          preact.h(ChatInput, {
            sessionId: "picker-exclusion-test",
            predefinedPrompts: [{ name: "Prompt", prompt: "Text" }],
            onSend: () => {},
            onLoopPrompt: () => {},
          }),
          preact.h(ConfigOptionSelect, {
            configOption: {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "a",
              options: [
                { value: "a", name: "Alpha" },
                { value: "b", name: "Beta" },
              ],
            },
            variant: "toolbar",
            placement: "top",
            onSetConfigOption: () => {},
          }),
        ),
        container,
      );

      const promptButton = () =>
        container.querySelector('[aria-label="Insert predefined prompt"]');
      const modelSummary = () =>
        container.querySelector('summary[aria-label="Model"]');
      const modelDetails = () =>
        container.querySelector(".picker-model-toolbar");
      const promptPanel = () =>
        container.querySelector(".chat-input-prompts-dropup");
      const promptFilter = () =>
        promptPanel()?.querySelector('input[type="text"]');
      const modelFilter = () =>
        modelDetails()?.querySelector('input[type="text"]');

      promptButton().click();
      await waitFor(
        () => promptFilter() && document.activeElement === promptFilter(),
        "the prompts filter to open and take focus",
      );

      modelSummary().click();
      await waitFor(
        () =>
          modelDetails()?.open === true &&
          promptPanel() === null &&
          document.activeElement === modelFilter(),
        "the model menu to open, close prompts, and take focus",
      );

      promptButton().click();
      await waitFor(
        () =>
          modelDetails()?.open === false &&
          promptPanel() !== null &&
          document.activeElement === promptFilter(),
        "prompts to reopen, close the model menu, and take focus",
      );

      modelSummary().click();
      await waitFor(
        () =>
          modelDetails()?.open === true &&
          promptPanel() === null &&
          document.activeElement === modelFilter(),
        "the model menu to reopen and close prompts",
      );

      // A real pointer press on the prompts trigger must not close Model on
      // mousedown (that reflows the toolbar and retargets the click).
      promptButton().dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true }),
      );
      await new Promise((resolve) => setTimeout(resolve, 30));
      if (modelDetails()?.open !== true) {
        throw new Error("model menu closed on prompts trigger mousedown");
      }
      promptButton().click();
      await waitFor(
        () => modelDetails()?.open === false && promptPanel() !== null,
        "prompts to open from a model-open press+click",
      );
    } finally {
      preact.render(null, container);
      container.remove();
      window.preact = previous;
    }
  });
} else {
  test("picker exclusion in isolated happy-dom process", () => {
    const result = spawnSync(
      process.execPath,
      ["test", fileURLToPath(import.meta.url)],
      {
        encoding: "utf8",
        env: { ...process.env, MITTO_PICKER_EXCLUSION_TEST_CHILD: "1" },
        timeout: 30_000,
      },
    );
    if (result.status !== 0) {
      throw new Error(
        `Isolated picker exclusion tests failed:\n${result.stdout}\n${result.stderr}`,
      );
    }
  });
}
