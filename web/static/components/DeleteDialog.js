// Mitto Web Interface - Delete Dialog Component
const { html, useState, useEffect } = window.preact;

import { Modal } from "./Modal.js";

// =============================================================================
// Delete Confirmation Dialog (with worktree merge-back flow)
// =============================================================================

// mergeFailureMessage returns a human-readable explanation for a failed
// merge-back attempt, keyed by the backend reason.
function mergeFailureMessage(reason, detail, target) {
  switch (reason) {
    case "conflict":
      return `Rebasing onto "${target}" stopped on conflicts and was aborted. The conversation was NOT deleted.`;
    case "dirty_worktree":
      return "This conversation's worktree has uncommitted changes, so it can't be merged automatically.";
    case "target_checked_out_dirty":
      return `The target branch "${target}" is checked out with uncommitted changes. Commit or stash them first.`;
    case "session_busy":
      return "The agent is currently responding. Wait for it to finish, or let the agent perform the merge.";
    default:
      return detail || "The merge could not be completed.";
  }
}

// renderMergeBody renders the warning + target selector + failure notice shown
// when a conversation has unmerged work.
function renderMergeBody(p) {
  return html`
    <div class="space-y-3 text-sm">
      <p class="text-mitto-text-muted">
        "${p.sessionName}" has work that is not yet merged.
        ${p.ahead > 0 &&
        html`<br /><span class="text-mitto-warning"
            >${p.ahead} commit${p.ahead === 1 ? "" : "s"} ahead of
            "${p.baseBranch || "its base branch"}".</span
          >`}
        ${p.dirty &&
        html`<br /><span class="text-orange-400"
            >⚠️ Uncommitted changes in the worktree.</span
          >`}
      </p>

      ${p.mergeResult &&
      html`<div class="alert alert-warning text-xs py-2">
        ${mergeFailureMessage(
          p.mergeResult.reason,
          p.mergeResult.detail,
          p.resolvedTarget,
        )}
      </div>`}

      <div class="space-y-2">
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            type="radio"
            name="merge-target-mode"
            class="radio radio-sm radio-primary"
            checked=${p.targetMode === "existing"}
            onChange=${() => p.setTargetMode("existing")}
          />
          <span>Merge (${p.strategy}) into an existing branch</span>
        </label>
        ${p.targetMode === "existing" &&
        html`<select
          value=${p.selectedTarget}
          onChange=${(e) => p.setSelectedTarget(e.target.value)}
          class="select select-sm w-full"
        >
          ${p.branches.map(
            (b) => html`<option value=${b} key=${b}>${b}</option>`,
          )}
        </select>`}

        <label class="flex items-center gap-2 cursor-pointer">
          <input
            type="radio"
            name="merge-target-mode"
            class="radio radio-sm radio-primary"
            checked=${p.targetMode === "new"}
            onChange=${() => p.setTargetMode("new")}
          />
          <span>Create a new branch (from the default branch)</span>
        </label>
        ${p.targetMode === "new" &&
        html`<input
          type="text"
          value=${p.newBranchName}
          onInput=${(e) => p.setNewBranchName(e.target.value)}
          class="input input-sm w-full font-mono"
          placeholder="feature-branch"
        />`}
      </div>
    </div>
  `;
}

export function DeleteDialog({
  isOpen,
  sessionName,
  isActive,
  isStreaming,
  worktreeStatus,
  branchesData,
  mergeResult,
  isMerging,
  onDelete,
  onMergeAndDelete,
  onSendAgentPrompt,
  onCancel,
}) {
  const hasUnmerged = !!worktreeStatus?.has_unmerged_work;
  const dirty = !!worktreeStatus?.dirty;
  const ahead = worktreeStatus?.ahead || 0;
  const strategy = worktreeStatus?.merge_strategy || "rebase";
  const baseBranch = worktreeStatus?.base_branch || "";
  const branches = branchesData?.branches || [];
  const defaultTarget =
    worktreeStatus?.default_merge_target ||
    branchesData?.default_merge_target ||
    baseBranch ||
    branchesData?.default_branch ||
    "";

  const [targetMode, setTargetMode] = useState("existing");
  const [selectedTarget, setSelectedTarget] = useState(defaultTarget);
  const [newBranchName, setNewBranchName] = useState("");

  // Reset the selection whenever the dialog (re)opens for a new conversation.
  useEffect(() => {
    if (isOpen) {
      setTargetMode("existing");
      setSelectedTarget(defaultTarget);
      setNewBranchName("");
    }
  }, [isOpen, defaultTarget]);

  const resolvedTarget =
    targetMode === "existing" ? selectedTarget : newBranchName.trim();
  const canMerge = resolvedTarget !== "";
  const doMerge = () =>
    targetMode === "existing"
      ? onMergeAndDelete(selectedTarget, "")
      : onMergeAndDelete("", newBranchName.trim());
  const doAgentPrompt = () =>
    targetMode === "existing"
      ? onSendAgentPrompt(selectedTarget, "")
      : onSendAgentPrompt("", newBranchName.trim());

  // Offer the agent-resolve path for conflicts, dirty trees and busy sessions.
  const offerAgent =
    mergeResult &&
    [
      "conflict",
      "dirty_worktree",
      "target_checked_out_dirty",
      "session_busy",
    ].includes(mergeResult.reason);

  // --- Simple path: no worktree or nothing unmerged -> plain confirmation. ---
  if (!hasUnmerged) {
    const footer = html`
      <button type="button" onClick=${onCancel} class="btn btn-ghost btn-sm">
        Cancel
      </button>
      <button type="button" onClick=${onDelete} class="btn btn-error btn-sm">
        Delete
      </button>
    `;
    return html`
      <${Modal} isOpen=${isOpen} onClose=${onCancel} title="Delete Conversation" footer=${footer}>
        <p class="text-mitto-text-muted text-sm">
          Are you sure you want to delete "${sessionName}"?
          ${isStreaming &&
          html`<br /><span class="text-orange-400"
              >⚠️ This conversation is still receiving a response.</span
            >`}
          ${isActive &&
          !isStreaming &&
          html`<br /><span class="text-mitto-warning"
              >This is the active conversation.</span
            >`}
        </p>
      </${Modal}>
    `;
  }

  // --- Merge-back path: the conversation has unmerged work. ---
  const footer = html`
    <button type="button" onClick=${onCancel} class="btn btn-ghost btn-sm">
      Cancel
    </button>
    ${offerAgent &&
    html`<button
      type="button"
      onClick=${doAgentPrompt}
      class="btn btn-warning btn-sm"
    >
      Let the agent resolve it
    </button>`}
    <button type="button" onClick=${onDelete} class="btn btn-ghost btn-sm text-error">
      Delete without merging
    </button>
    <button
      type="button"
      onClick=${doMerge}
      disabled=${!canMerge || isMerging}
      class="btn btn-primary btn-sm"
    >
      ${isMerging ? "Merging…" : "Merge and delete"}
    </button>
  `;

  return html`
    <${Modal} isOpen=${isOpen} onClose=${onCancel} title="Delete Conversation" footer=${footer}>
      ${renderMergeBody({
        sessionName,
        dirty,
        ahead,
        strategy,
        baseBranch,
        branches,
        targetMode,
        setTargetMode,
        selectedTarget,
        setSelectedTarget,
        newBranchName,
        setNewBranchName,
        mergeResult,
        resolvedTarget,
      })}
    </${Modal}>
  `;
}
