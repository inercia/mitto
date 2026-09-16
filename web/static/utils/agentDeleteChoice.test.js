import {
  AGENT_DELETE_UNSELECTED,
  agentDeleteSelectValue,
  hasAgentDeleteChoice,
} from "./agentDeleteChoice.js";

describe("agent deletion folder choice", () => {
  test("renders missing and null choices as the unselected placeholder", () => {
    expect(agentDeleteSelectValue(undefined)).toBe(AGENT_DELETE_UNSELECTED);
    expect(agentDeleteSelectValue({})).toBe(AGENT_DELETE_UNSELECTED);
    expect(agentDeleteSelectValue({ newServer: null })).toBe(
      AGENT_DELETE_UNSELECTED,
    );
    expect(hasAgentDeleteChoice({ newServer: null })).toBe(false);
  });

  test("preserves the empty string as an explicit delete choice", () => {
    expect(agentDeleteSelectValue({ newServer: "" })).toBe("");
    expect(hasAgentDeleteChoice({ newServer: "" })).toBe(true);
  });

  test("preserves a replacement agent as an explicit choice", () => {
    const choice = { newServer: "Auggie" };
    expect(agentDeleteSelectValue(choice)).toBe("Auggie");
    expect(hasAgentDeleteChoice(choice)).toBe(true);
  });
});
