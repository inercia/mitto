// Pure helpers mapping a session.PromptProvenance-shaped object (mitto-rg79)
// to concise frontend labels/details for the named-prompt pill footer, its
// tooltip, and the Properties panel's "Last loop delivery" section.
//
// Input shape mirrors the JSON produced by internal/session.PromptProvenance —
// field names are kept as their raw backend JSON keys (snake_case), the same
// convention already used for message.meta/message.arguments:
//   {
//     loop_trigger: "schedule"|"onCompletion"|"onTasks"|"onChild"|"onSlack"|"",
//     is_loop_forced: boolean,
//     is_loop_run_on_start: boolean,
//     slack: { installation_id, channel_id, event_count } | undefined,
//   }
//
// No `window.preact`/Icons.js import here (kept a pure, framework-free
// module like utils/loopSettings.js) — callers map the returned `iconKey`
// to a concrete icon component via PROVENANCE_ICON_COMPONENT_KEYS below.

export const PROVENANCE_ICON_SCHEDULE = "clock";
export const PROVENANCE_ICON_COMPLETION = "check";
export const PROVENANCE_ICON_TASKS = "beads";
export const PROVENANCE_ICON_CHILD = "person";
export const PROVENANCE_ICON_SLACK = "chat-bubble";
export const PROVENANCE_ICON_MANUAL = "play";
export const PROVENANCE_ICON_STARTUP = "lightning";
export const PROVENANCE_ICON_UNKNOWN = "loop";

/**
 * Builds a compact "installation · channel · N events" fragment for onSlack
 * detail lines. These are IDs, not display names, so do not prefix the channel
 * with "#" (which would incorrectly imply a resolved Slack channel name).
 * Returns "" when no Slack detail is available.
 * @param {{channel_id?:string, event_count?:number}|null|undefined} slack
 * @returns {string}
 */
function slackDetailFragment(slack) {
  if (!slack) return "";
  const parts = [];
  if (slack.installation_id) {
    parts.push(`installation ${slack.installation_id}`);
  }
  if (slack.channel_id) parts.push(`channel ${slack.channel_id}`);
  if (slack.event_count > 0) {
    parts.push(
      `${slack.event_count} event${slack.event_count === 1 ? "" : "s"}`,
    );
  }
  return parts.join(" · ");
}

/**
 * Maps a provenance object to { label, detail, iconKey }, or null when
 * provenance is absent/falsy (ordinary human-typed/ad-hoc prompts).
 *
 * Startup is preferred over forced when both flags happen to be true (the
 * backend records both raw; startup is the rarer, more specific signal — see
 * internal/conversation/bgsession_prompt.go deriveUserPromptProvenance).
 *
 * @param {object|null|undefined} provenance
 * @returns {{label:string, detail:string, iconKey:string}|null}
 */
export function describeProvenance(provenance) {
  if (!provenance) return null;

  if (provenance.is_loop_run_on_start) {
    return {
      label: "Startup",
      detail: "Sent automatically shortly after Mitto started",
      iconKey: PROVENANCE_ICON_STARTUP,
    };
  }
  if (provenance.is_loop_forced) {
    return {
      label: "Manual run",
      detail: "Triggered manually via Run now",
      iconKey: PROVENANCE_ICON_MANUAL,
    };
  }

  switch (provenance.loop_trigger) {
    case "schedule":
      return {
        label: "Schedule",
        detail: "Delivered by the scheduled loop trigger",
        iconKey: PROVENANCE_ICON_SCHEDULE,
      };
    case "onCompletion":
      return {
        label: "On completion",
        detail: "Fired after the agent finished responding",
        iconKey: PROVENANCE_ICON_COMPLETION,
      };
    case "onTasks":
      return {
        label: "On tasks",
        detail: "Fired by a beads/task change",
        iconKey: PROVENANCE_ICON_TASKS,
      };
    case "onChild":
      return {
        label: "On child",
        detail: "Fired by a child-conversation lifecycle event",
        iconKey: PROVENANCE_ICON_CHILD,
      };
    case "slack": // Legacy spelling retained for historical event compatibility.
    case "onSlack": {
      const frag = slackDetailFragment(provenance.slack);
      return {
        label: "Slack",
        detail: frag ? `Fired by a Slack event (${frag})` : "Fired by a Slack event",
        iconKey: PROVENANCE_ICON_SLACK,
      };
    }
    default:
      // Unknown/future trigger name: still render something informative
      // rather than silently dropping the indicator.
      if (provenance.loop_trigger) {
        return {
          label: provenance.loop_trigger,
          detail: `Fired by the "${provenance.loop_trigger}" trigger`,
          iconKey: PROVENANCE_ICON_UNKNOWN,
        };
      }
      return null;
  }
}

/**
 * Convenience wrapper returning just the tooltip-ready detail string ("" when
 * provenance is absent), for callers that only need one line of text.
 * @param {object|null|undefined} provenance
 * @returns {string}
 */
export function provenanceTooltip(provenance) {
  return describeProvenance(provenance)?.detail || "";
}
