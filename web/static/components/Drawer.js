// Mitto Web Interface - Drawer Component
// Generic Preact-controlled side drawer built on daisyUI `drawer` classes.
//
// Unlike daisyUI's CSS-only checkbox visibility, this drawer's presence is
// controlled by Preact: the parent conditionally mounts it (and typically keeps
// it mounted through an exit animation via `isClosing`). The internal
// `drawer-toggle` checkbox is kept permanently checked so daisyUI resolves the
// `:checked ~ .drawer-side` visible state (visibility/opacity/pointer-events +
// `translate: 0`). The slide-in/out motion and backdrop fade are driven by the
// existing `properties-panel` / `properties-backdrop` keyframes via the
// `isClosing` prop (right-side / `end` panels only).
//
// Props:
//   onClose       {Function} backdrop click + Escape handler
//   side          {"start"|"end"} edge the panel docks to (default "end")
//   isClosing     {boolean}  adds `.closing` to play the slide-out keyframes
//   animate       {boolean}  apply properties-panel/properties-backdrop classes
//                            (default true; set false for left/mobile drawers
//                            whose keyframes would slide from the wrong edge)
//   widthClass    {string}   width utilities for the panel (e.g. "w-80")
//   panelClass    {string}   remaining panel classes (surface, border, layout)
//   zClass        {string}   z-index utility for `.drawer-side` (default "z-50";
//                            overrides daisyUI's sublayered z-10)
//   className     {string}   extra classes for the `.drawer` root (e.g. md:hidden)
//   children      {any}      panel content
//   testid        {string}   data-testid applied to the panel element
//   overlayTestid {string}   data-testid applied to the drawer-overlay backdrop

const { html, useEffect } = window.preact;

export function Drawer({
  onClose,
  side = "end",
  isClosing = false,
  animate = true,
  widthClass = "w-80",
  panelClass = "bg-mitto-sidebar border-l border-mitto-border-1",
  zClass = "z-50",
  className = "",
  children,
  testid,
  overlayTestid,
}) {
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === "Escape") onClose?.();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const closing = isClosing ? "closing" : "";

  return html`
    <div class="drawer ${side === "end" ? "drawer-end" : ""} ${className}">
      <!-- Kept permanently checked: visibility is Preact-controlled (mount /
           unmount), the checkbox only makes daisyUI resolve the open state. -->
      <input
        type="checkbox"
        class="drawer-toggle"
        defaultChecked
        tabIndex=${-1}
        aria-hidden="true"
      />
      <div class="drawer-side ${zClass}">
        <!-- Backdrop: daisyUI provides the scrim color; properties-backdrop
             adds the fade in/out timing (end panels only). Click to close. -->
        <div
          class="drawer-overlay ${animate ? "properties-backdrop" : ""} ${closing}"
          onClick=${onClose}
          data-testid=${overlayTestid}
        ></div>
        <!-- Panel: width + caller layout classes; properties-panel adds the
             slide in/out timing (end panels only). -->
        <div
          class="${widthClass} ${panelClass} shadow-2xl ${animate
            ? "properties-panel"
            : ""} ${closing}"
          data-testid=${testid}
        >
          ${children}
        </div>
      </div>
    </div>
  `;
}
