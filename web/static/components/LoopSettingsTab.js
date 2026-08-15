// Mitto Web Interface - staged loop settings tab.

const { html, useCallback, useEffect, useMemo, useState, Fragment } =
  window.preact;

import { ConfirmDialog } from "./ConfirmDialog.js";
import { LoopPromptSelector } from "./LoopPromptSelector.js";
import {
  CONDITION_PRESETS,
  extractPresetParam,
  presetConditionFor,
  resolveConditionPresetId,
} from "../lib.js";
import { promptDialogParameters } from "../utils/prompts.js";
import { getSdkClient } from "../utils/sdkClient.js";
import {
  errorMessage as sdkErrorMessage,
  errorStatus,
} from "../utils/sdkErrors.js";
import {
  KNOWN_CHILD_EVENTS,
  KNOWN_LOOP_TRIGGERS,
  buildLoopPatch,
  canonicalizeChildEvents,
  canonicalizeLoopTriggers,
  isDangerousUnboundedLoop,
  normalizeLoopConfig,
  validateLoopDraft,
} from "../utils/loopSettings.js";

const CAP_STOP_REASONS = new Set([
  "maxDuration",
  "maxIterations",
  "iterationSafeguard",
]);

function ToggleRow({
  label,
  description,
  checked,
  onChange,
  disabled = false,
}) {
  return html`
    <label class="label cursor-pointer gap-4">
      <span>
        <span class="text-mitto-text-strong">${label}</span>
        ${description &&
        html`<span class="block text-xs text-mitto-text-muted"
          >${description}</span
        >`}
      </span>
      <input
        type="checkbox"
        class="toggle toggle-sm"
        checked=${checked}
        disabled=${disabled}
        onChange=${(event) => onChange(event.target.checked)}
      />
    </label>
  `;
}

function TriggerSection({
  trigger,
  title,
  description,
  armed,
  disabled = false,
  onToggle,
  children,
}) {
  return html`
    <div
      class="collapse border border-mitto-border bg-mitto-surface-2 ${armed
        ? "collapse-open"
        : "collapse-close"}"
      data-testid="loop-settings-${trigger}"
    >
      <div class="collapse-title">
        <label class="label cursor-pointer gap-3 justify-start">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked=${armed}
            disabled=${disabled}
            onChange=${(event) => onToggle(event.target.checked)}
            data-testid="loop-settings-trigger-${trigger}"
          />
          <span>
            <span class="font-medium text-mitto-text-strong">${title}</span>
            <span class="block text-xs text-mitto-text-muted"
              >${description}</span
            >
          </span>
        </label>
      </div>
      <div class="collapse-content">
        <fieldset disabled=${!armed} class="fieldset">${children}</fieldset>
      </div>
    </div>
  `;
}

function NumberField({ label, value, onInput, help, min = 0, testId }) {
  return html`
    <label class="fieldset">
      <span class="fieldset-legend">${label}</span>
      <input
        type="number"
        min=${min}
        value=${value}
        onInput=${(event) => onInput(event.target.value)}
        class="input input-sm w-full"
        data-testid=${testId}
      />
      ${help && html`<span class="label">${help}</span>`}
    </label>
  `;
}

/**
 * Full staged loop editor intended for later SessionPanel wiring.
 */
export function LoopSettingsTab({
  sessionId,
  loopConfig,
  prompts = [],
  allPrompts = [],
  hasBeadsWorkspace = false,
  isStreaming = false,
  minDelaySeconds = 5,
  onOpenPromptParamDialog,
  onConfigChange,
  showToast,
}) {
  const [draft, setDraft] = useState(null);
  const [serverDraft, setServerDraft] = useState(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [running, setRunning] = useState(false);
  const [validation, setValidation] = useState(null);
  const [saveError, setSaveError] = useState("");
  const [conditionError, setConditionError] = useState("");
  const [dialogError, setDialogError] = useState("");
  const [showRunDialog, setShowRunDialog] = useState(false);
  const [showDangerDialog, setShowDangerDialog] = useState(false);
  const [showRestoreDialog, setShowRestoreDialog] = useState(false);
  const [resetTimer, setResetTimer] = useState(true);
  const [resetCounters, setResetCounters] = useState(true);
  const [pendingPayload, setPendingPayload] = useState(null);
  const [conditionPresetOverride, setConditionPresetOverride] = useState(null);

  const applyConfig = useCallback(
    (config, notify = false) => {
      const normalized = normalizeLoopConfig(config);
      setDraft(normalized);
      setServerDraft(normalized);
      setValidation(null);
      setSaveError("");
      setConditionError("");
      setConditionPresetOverride(null);
      if (notify) onConfigChange?.(config);
      return normalized;
    },
    [onConfigChange],
  );

  useEffect(() => {
    let cancelled = false;
    setValidation(null);
    setSaveError("");
    setConditionError("");
    if (!sessionId) {
      setLoading(false);
      setDraft(null);
      setServerDraft(null);
      return () => {
        cancelled = true;
      };
    }
    if (loopConfig) {
      setLoading(false);
      applyConfig(loopConfig);
      return () => {
        cancelled = true;
      };
    }

    setLoading(true);
    getSdkClient()
      .sessions.loop.get(sessionId)
      .then((config) => {
        if (!cancelled) applyConfig(config);
      })
      .catch((error) => {
        if (!cancelled) {
          setDraft(null);
          setServerDraft(null);
          setSaveError(sdkErrorMessage(error, "Failed to load loop settings."));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [sessionId, loopConfig, applyConfig]);

  const stage = useCallback((updater) => {
    setDraft((current) =>
      typeof updater === "function" ? updater(current) : updater,
    );
    setValidation(null);
    setSaveError("");
  }, []);

  const armed = useCallback(
    (trigger) => !!draft?.triggers?.includes(trigger),
    [draft?.triggers],
  );

  const toggleTrigger = useCallback(
    (trigger, checked) => {
      if (trigger === "onTasks" && checked && !hasBeadsWorkspace) return;
      stage((current) => {
        const next = checked
          ? [...current.triggers, trigger]
          : current.triggers.filter((value) => value !== trigger);
        const updated = {
          ...current,
          triggers: canonicalizeLoopTriggers(next),
        };
        if (checked && trigger === "onCompletion") {
          updated.onCompletion = {
            ...current.onCompletion,
            delaySeconds: Math.max(
              minDelaySeconds,
              Number(current.onCompletion.delaySeconds) || 0,
            ),
          };
        }
        if (
          checked &&
          ["onCompletion", "onTasks", "onChild"].includes(trigger) &&
          current.iterationCount === 0
        ) {
          if (current.maxIterations <= 0) updated.maxIterations = 5;
          if (Number(current.maxDuration.value) <= 0) {
            updated.maxDuration = { value: 1, unit: "hours" };
          }
        }
        return updated;
      });
      setConditionError("");
    },
    [hasBeadsWorkspace, minDelaySeconds, stage],
  );

  const selectedPrompt = useMemo(() => {
    if (!draft?.promptName) return null;
    return (
      allPrompts.find((prompt) => prompt.name === draft.promptName) ||
      prompts.find((prompt) => prompt.name === draft.promptName) ||
      null
    );
  }, [allPrompts, prompts, draft?.promptName]);

  const selectedPromptParams = useMemo(
    () => (selectedPrompt ? promptDialogParameters(selectedPrompt) : []),
    [selectedPrompt],
  );

  const openArguments = useCallback(() => {
    if (
      !selectedPrompt ||
      selectedPromptParams.length === 0 ||
      !onOpenPromptParamDialog
    ) {
      return;
    }
    onOpenPromptParamDialog(
      selectedPrompt,
      selectedPromptParams,
      (argumentsMap) => {
        stage((current) => ({ ...current, arguments: { ...argumentsMap } }));
      },
      { initialValues: draft.arguments, hostSessionId: sessionId },
    );
  }, [
    selectedPrompt,
    selectedPromptParams,
    onOpenPromptParamDialog,
    draft?.arguments,
    sessionId,
    stage,
  ]);

  const presetId =
    conditionPresetOverride ||
    (draft
      ? resolveConditionPresetId(
          draft.onTasks.condition,
          draft.onTasks.conditionPreset,
        )
      : "any");
  const preset = CONDITION_PRESETS.find((item) => item.id === presetId);
  const presetParam = draft
    ? extractPresetParam(presetId, draft.onTasks.condition)
    : "";

  const setPreset = useCallback(
    (id) => {
      setConditionPresetOverride(id === "custom" ? "custom" : null);
      stage((current) => ({
        ...current,
        onTasks: {
          ...current.onTasks,
          conditionPreset: id === "custom" ? "" : id,
          condition:
            id === "custom"
              ? current.onTasks.condition
              : presetConditionFor(id, ""),
        },
      }));
      setConditionError("");
    },
    [stage],
  );

  const setPresetParam = useCallback(
    (value) => {
      setConditionPresetOverride(null);
      stage((current) => ({
        ...current,
        onTasks: {
          ...current.onTasks,
          conditionPreset: presetId,
          condition: presetConditionFor(presetId, value),
        },
      }));
      setConditionError("");
    },
    [presetId, stage],
  );

  const restoring =
    !!draft && !!serverDraft && !serverDraft.enabled && draft.enabled;
  const limitStopped =
    restoring && CAP_STOP_REASONS.has(serverDraft.stoppedReason);

  const performSave = useCallback(
    async (payload, restore = null) => {
      if (!sessionId || saving) return;
      setSaving(true);
      setSaveError("");
      setConditionError("");
      try {
        const response = await getSdkClient().sessions.loop.update(
          sessionId,
          payload,
        );
        applyConfig(response, true);
        showToast?.({ message: "Loop settings saved", style: "success" });
        if (restore && !(restore.limitStopped && !restore.resetCounters)) {
          try {
            await getSdkClient().sessions.loop.runNow(sessionId, true);
          } catch (_error) {
            // Restoring succeeded; a busy agent can run the enabled loop later.
          }
        }
        setPendingPayload(null);
        setShowDangerDialog(false);
        setShowRestoreDialog(false);
      } catch (error) {
        const message = sdkErrorMessage(error, "Failed to save loop settings.");
        setSaveError(message);
        if (draft?.triggers.includes("onTasks")) setConditionError(message);
      } finally {
        setSaving(false);
      }
    },
    [sessionId, saving, applyConfig, showToast, draft?.triggers],
  );

  const continueSave = useCallback(
    (payload) => {
      if (restoring) {
        setPendingPayload(payload);
        setResetCounters(true);
        setShowRestoreDialog(true);
        return;
      }
      performSave(payload);
    },
    [restoring, performSave],
  );

  const requestSave = useCallback(() => {
    if (!draft || saving) return;
    const result = validateLoopDraft(draft);
    setValidation(result);
    if (!result.valid) return;
    const payload = buildLoopPatch(draft, { minDelaySeconds });
    if (isDangerousUnboundedLoop(draft)) {
      setPendingPayload(payload);
      setShowDangerDialog(true);
      return;
    }
    continueSave(payload);
  }, [draft, saving, minDelaySeconds, continueSave]);

  const confirmDanger = useCallback(() => {
    setShowDangerDialog(false);
    if (pendingPayload) continueSave(pendingPayload);
  }, [pendingPayload, continueSave]);

  const confirmRestore = useCallback(() => {
    if (!pendingPayload) return;
    const payload =
      limitStopped && resetCounters
        ? { ...pendingPayload, reset_counters: true }
        : pendingPayload;
    performSave(payload, {
      limitStopped,
      resetCounters,
    });
  }, [pendingPayload, limitStopped, resetCounters, performSave]);

  const confirmRunNow = useCallback(async () => {
    if (!sessionId || running) return;
    setRunning(true);
    try {
      await getSdkClient().sessions.loop.runNow(sessionId, resetTimer);
      setShowRunDialog(false);
      showToast?.({ message: "Loop run started", style: "success" });
    } catch (error) {
      setShowRunDialog(false);
      setDialogError(
        errorStatus(error) === 409
          ? "Session is currently processing a prompt. Please wait and try again."
          : sdkErrorMessage(error, "Failed to run the loop now."),
      );
    } finally {
      setRunning(false);
    }
  }, [sessionId, running, resetTimer, showToast]);

  if (loading) {
    return html`<div class="flex justify-center p-6">
      <span class="loading loading-spinner loading-md"></span>
    </div>`;
  }

  if (!draft) {
    return html`
      <div class="alert alert-error" role="alert">
        <span>${saveError || "Loop settings are unavailable."}</span>
      </div>
    `;
  }

  const unknownTriggers = draft.triggers.filter(
    (trigger) => !KNOWN_LOOP_TRIGGERS.includes(trigger),
  );
  const resetLabel =
    draft.maxIterations > 0 && draft.maxDuration.value > 0
      ? "Reset elapsed time and iteration count"
      : draft.maxDuration.value > 0
        ? "Reset elapsed time"
        : "Reset iteration count";

  return html`
    <${Fragment}>
      <div class="flex flex-col gap-4" data-testid="loop-settings-tab">
        ${
          saveError &&
          html`<div class="alert alert-error" role="alert">
            <span>${saveError}</span>
          </div>`
        }
        ${
          validation &&
          !validation.valid &&
          html`<div class="alert alert-error" role="alert">
            <span>${validation.firstError}</span>
          </div>`
        }

        <fieldset class="fieldset border border-mitto-border rounded-box p-4">
          <legend class="fieldset-legend">Prompt</legend>
          <div class="flex flex-wrap gap-4">
            <label class="label cursor-pointer gap-2">
              <input
                type="radio"
                name="loop-prompt-mode"
                class="radio radio-sm"
                checked=${draft.promptMode === "named"}
                onChange=${() =>
                  stage((current) => ({ ...current, promptMode: "named" }))}
              />
              Named prompt
            </label>
            <label class="label cursor-pointer gap-2">
              <input
                type="radio"
                name="loop-prompt-mode"
                class="radio radio-sm"
                checked=${draft.promptMode === "freeText"}
                onChange=${() =>
                  stage((current) => ({ ...current, promptMode: "freeText" }))}
              />
              Free text
            </label>
          </div>
          ${
            draft.promptMode === "named"
              ? html`
                  <div class="flex flex-wrap items-center gap-2">
                    <div class="flex-1 min-w-0">
                      <${LoopPromptSelector}
                        prompts=${prompts}
                        selectedPromptName=${draft.promptName}
                        selectedPromptBody=""
                        fullWidth=${true}
                        idPrefix="loop-settings-prompt"
                        onSelect=${(promptName) =>
                          stage((current) => ({
                            ...current,
                            promptName,
                            arguments:
                              promptName === current.promptName
                                ? current.arguments
                                : {},
                          }))}
                      />
                    </div>
                    <button
                      type="button"
                      class="btn btn-sm"
                      disabled=${selectedPromptParams.length === 0 ||
                      !onOpenPromptParamDialog}
                      onClick=${openArguments}
                    >
                      Arguments
                    </button>
                  </div>
                `
              : html`
                  <textarea
                    class="textarea textarea-sm w-full"
                    rows="5"
                    value=${draft.promptBody}
                    placeholder="Enter the prompt sent on every loop run"
                    onInput=${(event) =>
                      stage((current) => ({
                        ...current,
                        promptBody: event.target.value,
                      }))}
                  ></textarea>
                `
          }
          ${
            validation?.fieldErrors.prompt &&
            html`<span class="label text-mitto-danger"
              >${validation.fieldErrors.prompt}</span
            >`
          }
        </fieldset>

        <fieldset class="fieldset border border-mitto-border rounded-box p-4">
          <legend class="fieldset-legend">General</legend>
          <${ToggleRow}
            label="Enabled"
            description="Allow configured triggers to run this loop"
            checked=${draft.enabled}
            onChange=${(enabled) =>
              stage((current) => ({ ...current, enabled }))}
          />
          <${ToggleRow}
            label="Fresh context"
            description="Start every run with a clean agent context"
            checked=${draft.freshContext}
            onChange=${(freshContext) =>
              stage((current) => ({ ...current, freshContext }))}
          />
          <${ToggleRow}
            label="Run on start"
            description="Fire once shortly after Mitto starts"
            checked=${draft.runOnStart}
            onChange=${(runOnStart) =>
              stage((current) => ({ ...current, runOnStart }))}
          />
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <${NumberField}
              label="Max runs"
              value=${draft.maxIterations}
              help="0 = unlimited"
              onInput=${(value) =>
                stage((current) => ({
                  ...current,
                  maxIterations: Number(value),
                }))}
              testId="loop-settings-max-runs"
            />
            <label class="fieldset">
              <span class="fieldset-legend">Max duration</span>
              <div class="flex gap-2">
                <input
                  type="number"
                  min="0"
                  value=${draft.maxDuration.value}
                  class="input input-sm flex-1 min-w-0"
                  onInput=${(event) =>
                    stage((current) => ({
                      ...current,
                      maxDuration: {
                        ...current.maxDuration,
                        value: Number(event.target.value),
                      },
                    }))}
                />
                <select
                  class="select select-sm"
                  value=${draft.maxDuration.unit}
                  onChange=${(event) =>
                    stage((current) => ({
                      ...current,
                      maxDuration: {
                        ...current.maxDuration,
                        unit: event.target.value,
                      },
                    }))}
                >
                  <option value="seconds">seconds</option>
                  <option value="minutes">minutes</option>
                  <option value="hours">hours</option>
                  <option value="days">days</option>
                </select>
              </div>
              <span class="label">0 = unlimited</span>
            </label>
          </div>
          <span class="label">
            ${draft.iterationCount} completed run${
              draft.iterationCount === 1 ? "" : "s"
            }${draft.stoppedReason ? ` · stopped: ${draft.stoppedReason}` : ""}
          </span>
        </fieldset>

        <fieldset class="fieldset">
          <legend class="fieldset-legend">Triggers</legend>
          ${
            validation?.fieldErrors.triggers &&
            html`<div class="alert alert-error" role="alert">
              <span>${validation.fieldErrors.triggers}</span>
            </div>`
          }
          ${
            unknownTriggers.length > 0 &&
            html`<div class="alert alert-info" role="alert">
              <span
                >Preserving additional triggers:
                ${unknownTriggers.join(", ")}</span
              >
            </div>`
          }

          <${TriggerSection}
            trigger="schedule"
            title="Schedule"
            description="Run repeatedly on a time interval"
            armed=${armed("schedule")}
            onToggle=${(checked) => toggleTrigger("schedule", checked)}
          >
            <div class="flex flex-wrap items-end gap-3">
              <${NumberField}
                label="Run every"
                min=${1}
                value=${draft.schedule.value}
                onInput=${(value) =>
                  stage((current) => ({
                    ...current,
                    schedule: { ...current.schedule, value: Number(value) },
                  }))}
              />
              <label class="fieldset">
                <span class="fieldset-legend">Unit</span>
                <select
                  class="select select-sm"
                  value=${draft.schedule.unit}
                  onChange=${(event) =>
                    stage((current) => ({
                      ...current,
                      schedule: {
                        ...current.schedule,
                        unit: event.target.value,
                        at:
                          event.target.value === "days"
                            ? current.schedule.at
                            : "",
                      },
                    }))}
                >
                  <option value="minutes">minutes</option>
                  <option value="hours">hours</option>
                  <option value="days">days</option>
                </select>
              </label>
              ${
                draft.schedule.unit === "days" &&
                html`<label class="fieldset">
                  <span class="fieldset-legend">Local time</span>
                  <input
                    type="time"
                    class="input input-sm"
                    value=${draft.schedule.at}
                    onInput=${(event) =>
                      stage((current) => ({
                        ...current,
                        schedule: {
                          ...current.schedule,
                          at: event.target.value,
                        },
                      }))}
                  />
                </label>`
              }
            </div>
            ${
              validation?.fieldErrors.schedule &&
              html`<span class="label text-mitto-danger"
                >${validation.fieldErrors.schedule}</span
              >`
            }
          </${TriggerSection}>

          <${TriggerSection}
            trigger="onCompletion"
            title="On completion"
            description="Run after the agent finishes responding"
            armed=${armed("onCompletion")}
            onToggle=${(checked) => toggleTrigger("onCompletion", checked)}
          >
            <${NumberField}
              label="Delay (seconds)"
              min=${0}
              value=${draft.onCompletion.delaySeconds}
              help="Saved with the server minimum applied"
              onInput=${(value) =>
                stage((current) => ({
                  ...current,
                  onCompletion: {
                    ...current.onCompletion,
                    delaySeconds: Number(value),
                  },
                }))}
              testId="loop-settings-completion-delay"
            />
          </${TriggerSection}>

          <${TriggerSection}
            trigger="onTasks"
            title="On tasks"
            description=${
              hasBeadsWorkspace
                ? "Run when matching beads/task changes occur"
                : "Unavailable because this workspace has no beads task store"
            }
            armed=${armed("onTasks")}
            disabled=${!hasBeadsWorkspace && !armed("onTasks")}
            onToggle=${(checked) => toggleTrigger("onTasks", checked)}
          >
            <label class="fieldset">
              <span class="fieldset-legend">Fire when</span>
              <select
                class="select select-sm w-full"
                value=${presetId}
                onChange=${(event) => setPreset(event.target.value)}
              >
                ${CONDITION_PRESETS.map(
                  (item) =>
                    html`<option value=${item.id}>${item.label}</option>`,
                )}
                <option value="custom">Custom (advanced CEL)</option>
              </select>
            </label>
            ${
              preset?.needsParam &&
              html`<label class="fieldset">
                <span class="fieldset-legend">${preset.paramLabel}</span>
                <input
                  type="text"
                  class="input input-sm w-full"
                  value=${presetParam}
                  placeholder=${preset.paramPlaceholder}
                  onInput=${(event) => setPresetParam(event.target.value)}
                />
              </label>`
            }
            <label class="fieldset">
              <span class="fieldset-legend">Condition (CEL)</span>
              <textarea
                class="textarea textarea-sm w-full font-mono"
                rows="3"
                value=${draft.onTasks.condition}
                placeholder="Empty = any task change"
                onInput=${(event) => {
                  setConditionError("");
                  setConditionPresetOverride("custom");
                  stage((current) => ({
                    ...current,
                    onTasks: {
                      ...current.onTasks,
                      condition: event.target.value,
                      conditionPreset: "",
                    },
                  }));
                }}
              ></textarea>
              ${
                conditionError &&
                html`<span class="label text-mitto-danger"
                  >${conditionError}</span
                >`
              }
            </label>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <${NumberField}
                label="Cooldown (seconds)"
                value=${draft.onTasks.cooldownSeconds}
                help="0 = global default"
                onInput=${(value) =>
                  stage((current) => ({
                    ...current,
                    onTasks: {
                      ...current.onTasks,
                      cooldownSeconds: Number(value),
                    },
                  }))}
              />
              <${NumberField}
                label="Settle window (seconds)"
                value=${draft.onTasks.settleWindowSeconds}
                help="0 = fire on first change"
                onInput=${(value) =>
                  stage((current) => ({
                    ...current,
                    onTasks: {
                      ...current.onTasks,
                      settleWindowSeconds: Number(value),
                    },
                  }))}
              />
            </div>
            <${ToggleRow}
              label="Coalesce while busy"
              description="Absorb task changes while the loop subtree is active"
              checked=${draft.onTasks.coalesceDuringBusy}
              onChange=${(coalesceDuringBusy) =>
                stage((current) => ({
                  ...current,
                  onTasks: { ...current.onTasks, coalesceDuringBusy },
                }))}
            />
          </${TriggerSection}>

          <${TriggerSection}
            trigger="onChild"
            title="On child"
            description="Run when child-conversation lifecycle events occur"
            armed=${armed("onChild")}
            onToggle=${(checked) => toggleTrigger("onChild", checked)}
          >
            ${KNOWN_CHILD_EVENTS.map((eventName) => {
              const labels = {
                anyEndResponse: "Any child finishes a response",
                anyDeleted: "Any child is deleted",
                anyLoopStopped: "Any child loop stops",
              };
              return html`<label
                class="label cursor-pointer justify-start gap-3"
              >
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm"
                  checked=${draft.onChild.events.includes(eventName)}
                  onChange=${(event) =>
                    stage((current) => {
                      const events = event.target.checked
                        ? [...current.onChild.events, eventName]
                        : current.onChild.events.filter(
                            (value) => value !== eventName,
                          );
                      return {
                        ...current,
                        onChild: {
                          events: canonicalizeChildEvents(events),
                        },
                      };
                    })}
                />
                ${labels[eventName]}
              </label>`;
            })}
          </${TriggerSection}>
        </fieldset>

        <div class="flex flex-wrap justify-end gap-2">
          <button
            type="button"
            class="btn btn-sm"
            disabled=${running || saving || isStreaming}
            onClick=${() => {
              setResetTimer(true);
              setShowRunDialog(true);
            }}
          >
            ${running && html`<span class="loading loading-spinner loading-xs"></span>`}
            Run now
          </button>
          <button
            type="button"
            class="btn btn-primary btn-sm"
            disabled=${saving}
            onClick=${requestSave}
          >
            ${saving && html`<span class="loading loading-spinner loading-xs"></span>`}
            Save
          </button>
        </div>
      </div>

      <${ConfirmDialog}
        isOpen=${showRunDialog}
        title="Run now"
        message="Do you want to send this loop prompt now?"
        confirmLabel="Send"
        isLoading=${running}
        onConfirm=${confirmRunNow}
        onCancel=${() => !running && setShowRunDialog(false)}
      >
        <label class="label cursor-pointer justify-start gap-3">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked=${resetTimer}
            onChange=${(event) => setResetTimer(event.target.checked)}
          />
          Reset countdown for the next scheduled run
        </label>
      </${ConfirmDialog}>

      <${ConfirmDialog}
        isOpen=${showDangerDialog}
        title="Save unbounded loop?"
        message="This new loop has no run or duration limit and can fire frequently. It could keep running indefinitely."
        confirmLabel="Save anyway"
        confirmVariant="danger"
        isLoading=${saving}
        onConfirm=${confirmDanger}
        onCancel=${() => {
          setShowDangerDialog(false);
          setPendingPayload(null);
        }}
      />

      <${ConfirmDialog}
        isOpen=${showRestoreDialog}
        title="Restore loop schedule"
        message=${
          limitStopped
            ? "This loop stopped after reaching a configured safety limit. Restore it to keep iterating."
            : "Restore this loop and run its prompt now?"
        }
        confirmLabel="Restore"
        isLoading=${saving}
        onConfirm=${confirmRestore}
        onCancel=${() => {
          setShowRestoreDialog(false);
          setPendingPayload(null);
        }}
      >
        ${
          saveError &&
          html`<div class="alert alert-error" role="alert">
            <span>${saveError}</span>
          </div>`
        }
        ${
          limitStopped &&
          html`<label class="label cursor-pointer justify-start gap-3">
            <input
              type="checkbox"
              class="checkbox checkbox-sm"
              checked=${resetCounters}
              onChange=${(event) => setResetCounters(event.target.checked)}
            />
            ${resetLabel}
          </label>`
        }
      </${ConfirmDialog}>

      <${ConfirmDialog}
        isOpen=${dialogError !== ""}
        title="Error"
        message=${dialogError}
        confirmLabel="OK"
        onConfirm=${() => setDialogError("")}
        onCancel=${() => setDialogError("")}
      />
    </${Fragment}>
  `;
}
