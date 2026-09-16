export const AGENT_DELETE_UNSELECTED = "__none__";

// The backend uses the empty string as the explicit "delete this folder"
// choice, so the unselected UI state must use a distinct value. Otherwise the
// select displays deletion before the user has chosen it and selecting that
// already-displayed option emits no change event.
export function agentDeleteSelectValue(choice) {
  return typeof choice?.newServer === "string"
    ? choice.newServer
    : AGENT_DELETE_UNSELECTED;
}

export function hasAgentDeleteChoice(choice) {
  return typeof choice?.newServer === "string";
}
