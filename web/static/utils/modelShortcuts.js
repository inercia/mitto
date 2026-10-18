// Pure runtime mapping, physical-key matching and safe confirmation feedback.
// Model resolution belongs exclusively to the canonical backend tag operation.
export function resolveModelShortcut(digit, modelShortcuts) {
  if (!Number.isInteger(digit) || digit < 1 || digit > 5 || !Array.isArray(modelShortcuts)) {
    return { status: "unassigned" };
  }
  const entry = modelShortcuts.find((row) => row?.number === digit &&
    typeof row.model_tag === "string" && row.model_tag.trim());
  return entry ? { status: "apply", modelTag: entry.model_tag.trim() } : { status: "unassigned" };
}

export function modelShortcutDigit(event) {
  if (!(event.metaKey || event.ctrlKey) || !event.shiftKey) return null;
  const match = /^Digit([1-5])$/.exec(event.code);
  return match ? Number(match[1]) : null;
}

export function modelShortcutToast(outcome, modelTag, digit) {
  const title = "Model shortcut";
  switch (outcome?.status) {
    case "applied":
      return { style: "success", title, message: `Model selection accepted for tag “${modelTag}”; it may take effect before the next prompt.` };
    case "already_active":
      return { style: "info", title, message: `The model for tag “${modelTag}” is already active.` };
    case "unassigned":
      return { style: "info", title, message: `No tag is assigned to shortcut ${digit}. Configure Settings > Keyboard.` };
    case "no_match":
      return { style: "warning", title, message: `No matching model for tag “${modelTag}”.` };
    case "catalog_unavailable":
      return { style: "warning", title, message: "The model catalog is unavailable. Try again when it is available." };
    case "invalid_tag":
      return { style: "error", title, message: "Invalid model tag. Check Settings > Keyboard." };
    case "invalid_request":
      return { style: "error", title, message: "Invalid model selection request. Refresh and try again." };
    case "missing_session":
    case "invalid_target":
      return { style: "error", title, message: "The conversation is unavailable for model selection." };
    case "send_failed":
      return { style: "error", title, message: "The model selection request could not be sent or confirmed. Check the connection and current model before retrying." };
    case "timeout":
      return { style: "warning", title, message: "Model selection confirmation was not received; the selection may still apply. Check the current model before retrying." };
    case "cancelled":
      return null;
    case "internal_error":
      return { style: "error", title, message: "Model selection encountered an application error. Check the current model before retrying." };
    case "protocol_error":
    default:
      return { style: "error", title, message: "Model selection could not be confirmed. Refresh and check the current model before retrying." };
  }
}
