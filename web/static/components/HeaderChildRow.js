// mitto-7vpp — HeaderChildRow
//
// A single row of the "children dropdown" in the conversation header
// (see app.js `header-children-dropdown` item). Extracted into its own
// component so each row can call the `useLinkedBeadPhase` hook (which
// resolves a bead's phase pill) — hooks cannot be called inside a `.map`
// callback of another component.
//
// The row mirrors the sidebar's visual language (see SessionItem.js): loading
// ring while the child agent is streaming, origin marker (auto / mcp / human),
// linked-bead phase pill (the purple / accent / success pill also shown in the
// tree), and the waiting-for-children / waiting-for-user-input pulses.

const { html } = window.preact;
import { useLinkedBeadPhase } from "../hooks/index.js";
import {
  LightningIcon,
  RobotIcon,
  PersonIcon,
  HourglassIcon,
  QuestionMarkIcon,
  LoopFilledIcon,
  SearchIcon,
  RefreshIcon,
  WrenchIcon,
  ListIcon,
  CodeBlockIcon,
  BeakerIcon,
  EyeIcon,
  CheckIcon,
} from "./Icons.js";

// Kept in sync with SessionItem.js's PHASE_ICON_COMPONENTS: maps the
// `currentIconName` field from derivePhaseState() to a concrete icon component.
const PHASE_ICON_COMPONENTS = {
  search: SearchIcon,
  refresh: RefreshIcon,
  wrench: WrenchIcon,
  list: ListIcon,
  "code-block": CodeBlockIcon,
  beaker: BeakerIcon,
  eye: EyeIcon,
  check: CheckIcon,
};

export function HeaderChildRow({ child, onSelect }) {
  const beadPhase = useLinkedBeadPhase(child.beads_issue, child.working_dir);

  const isStreaming = !child.archived && !!child.isStreaming;

  const originIcon =
    child.child_origin === "auto"
      ? html`<span
          class="shrink-0 text-amber-400"
          data-tip="Auto-created child"
          aria-label="Auto-created child"
        >
          <${LightningIcon} className="w-3.5 h-3.5" />
        </span>`
      : child.child_origin === "mcp"
        ? html`<span
            class="shrink-0 text-mitto-accent"
            data-tip="Created by agent"
            aria-label="Created by agent"
          >
            <${RobotIcon} className="w-3.5 h-3.5" />
          </span>`
        : child.child_origin === "human"
          ? html`<span
              class="shrink-0 text-mitto-success"
              data-tip="Manually created child"
              aria-label="Manually created child"
            >
              <${PersonIcon} className="w-3.5 h-3.5" />
            </span>`
          : null;

  // Linked-bead phase pill — same colours / icon as the sidebar tree so the
  // menu carries the exact same at-a-glance status the user sees in the tree.
  let phasePill = null;
  if (beadPhase) {
    const PhaseIcon = PHASE_ICON_COMPONENTS[beadPhase.currentIconName];
    const tip = `${beadPhase.kindLabel} phase: ${beadPhase.currentDisplayName}`;
    const pillColour = beadPhase.isTerminal
      ? "bg-mitto-success/20 border-mitto-success/40 text-mitto-success"
      : beadPhase.currentTier === "reasoning"
        ? "bg-purple-500/20 border-purple-500/40 text-purple-300"
        : "bg-mitto-accent/20 border-mitto-accent/40 text-mitto-accent";
    phasePill = html`<span
      class="badge badge-xs shrink-0 inline-flex items-center justify-center border ${pillColour}"
      data-tip=${tip}
      aria-label=${tip}
    >
      ${PhaseIcon ? html`<${PhaseIcon} className="w-3 h-3" />` : null}
    </span>`;
  }

  return html`
    <li class="w-full min-w-0">
      <button
        type="button"
        class="flex items-center gap-2 text-left w-full min-w-0 flex-nowrap"
        data-testid=${`header-children-item-${child.session_id}`}
        onClick=${() => onSelect(child.session_id)}
      >
        ${isStreaming
          ? html`<span
              class="loading loading-ring loading-xs shrink-0 text-mitto-accent"
              data-tip="Receiving response..."
              aria-label="Receiving response..."
            ></span>`
          : null}
        ${child.loop_configured
          ? html`<${LoopFilledIcon}
              className="w-3 h-3 opacity-70 shrink-0"
            />`
          : null}
        ${originIcon}
        <span class="truncate flex-1 min-w-0">
          ${child.name || child.description || "Untitled"}
        </span>
        ${phasePill}
        ${child.isWaitingForChildren
          ? html`<span
              class="shrink-0 text-mitto-warning animate-pulse"
              data-tip="Waiting for child conversations"
              aria-label="Waiting for child conversations"
            >
              <${HourglassIcon} className="w-3.5 h-3.5" />
            </span>`
          : null}
        ${child.isWaitingForUserInput
          ? html`<span
              class="shrink-0 text-purple-400 animate-pulse"
              data-tip="Waiting for user input"
              aria-label="Waiting for user input"
            >
              <${QuestionMarkIcon} className="w-3.5 h-3.5" />
            </span>`
          : null}
      </button>
    </li>
  `;
}
