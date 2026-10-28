# Prompt menu accessibility

`web/static/components/PromptsMenu.js` renders rows that hold **two independent
actions**: activating the prompt, and toggling whether a prompt runs as a
recurring (loop) conversation. Because of that, the list is deliberately **not**
a `listbox`/`option` widget — options cannot contain their own interactive
controls.

## Implemented semantics

| Concern              | Mechanism                                                                                                                                                               |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Prompt row           | `<li>` flex wrapper containing a `<button class="prompt-item">` action plus, when `loopToggle` is set, a sibling `<input type="checkbox" class="prompt-loop-checkbox">` |
| Chosen prompt        | `aria-current="true"` on the action button (absent otherwise) and the word "selected" in its `aria-label`                                                               |
| Keyboard highlight   | Visual only (background tint + inset accent bar), announced through a polite status region                                                                              |
| Announcement         | `<div class="sr-only" role="status" aria-live="polite" aria-atomic="true">` with `"<name>, <n> of <total>"`, cleared when nothing is highlighted                        |
| Optional loop toggle | `aria-label="Loop: <name>"`, real checkbox, Space/click toggles without running the prompt                                                                              |
| Mandatory loop       | `aria-label="Always loops: <name>"`, `checked disabled` (checked state is announced in browse mode, never a focus trap)                                                 |

Navigation itself lives in `web/static/hooks/useSearchableMenu.js` (Arrow keys
move the highlight, Enter accepts the highlighted prompt). `LoopPromptSelector`
does not pass a highlight, so its status region stays silent and the menu keeps
its non-navigable behaviour there.

## Manual verification checklist

Automated tests cover the DOM structure, names and announcements
(`web/static/components/PromptsMenu.test.js`). The announcements themselves can
only be confirmed with a real screen reader — run the steps below and record
what you hear in the **Observed** column.

| Browser | AT        | Step                                                                     | Expected                                                                                        | Observed                     |
| ------- | --------- | ------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- | ---------------------------- |
| Safari  | VoiceOver | Open the chat prompt menu, press <kbd>↓</kbd> once                       | Announces the first prompt name and position ("…, 1 of N"); does not say "selected"             | _pending human verification_ |
| Safari  | VoiceOver | Press <kbd>↓</kbd> repeatedly to the last prompt                         | Each new highlight is announced once, with its position; no duplicate announcements             | _pending human verification_ |
| Safari  | VoiceOver | <kbd>Tab</kbd> from the filter input through an optional-loop row        | Reaches the action button, then the loop checkbox; both names announced; no keyboard trap       | _pending human verification_ |
| Safari  | VoiceOver | Press <kbd>Space</kbd> on the focused loop checkbox                      | Checkbox toggles, its new state is announced, the prompt does **not** run                       | _pending human verification_ |
| Safari  | VoiceOver | Focus a mandatory-loop row's checkbox                                    | "Always loops: <name>", checked and dimmed; not reachable by <kbd>Tab</kbd>                     | _pending human verification_ |
| Chrome  | NVDA      | Open the loop-prompt selector (Settings → conversation loop)             | Menu opens with filter focused; no highlight announcements while not navigating                 | _pending human verification_ |
| Chrome  | NVDA      | Browse the chosen prompt row                                             | Row name is followed by "selected" / current item, distinct from the highlight tint             | _pending human verification_ |
| Chrome  | NVDA      | Activate a prompt with <kbd>Enter</kbd> after toggling its loop checkbox | Prompt runs once; keyboard path still ignores the checkbox value (unchanged business behaviour) | _pending human verification_ |
