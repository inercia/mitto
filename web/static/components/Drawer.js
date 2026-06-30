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
//   scoped        {boolean}  confine the drawer to its nearest positioned
//                            ancestor instead of the viewport (adds
//                            `drawer-scoped`; see styles.css). The caller must
//                            supply its own full-window backdrop — this variant's
//                            drawer-overlay is transparent. Used by BeadsView so
//                            the panel/fullscreen fills only the beads view area.
//   dock          {boolean}  dock the panel to the right edge of the nearest
//                            positioned ancestor, confined to the PANEL's own
//                            width with no dimming backdrop (adds `drawer-dock`;
//                            see styles.css). The content to the panel's left is
//                            never under a composited layer, which avoids the
//                            WebKit/Chromium backing-store drop that blanked the
//                            conversation on pointer-move (mitto-cdf). On phones
//                            the panel covers the whole viewport. No outside-click
//                            backdrop — close via Escape or the panel's own UI.
//   rootStyle     {string}   inline style applied to the `.drawer` root. In dock
//                            mode set the docked width via CSS vars, e.g.
//                            "--dock-w:40rem;--dock-maxw:85%" (defaults: 20rem /
//                            100%). Ignored on phones (dock covers the viewport).
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
  scoped = false,
  dock = false,
  rootStyle = "",
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
    <div
      class="drawer ${side === "end" ? "drawer-end" : ""} ${scoped
        ? "drawer-scoped"
        : ""} ${dock ? "drawer-dock" : ""} ${className}"
      style=${rootStyle}
    >
      <!-- Kept permanently checked: visibility is Preact-controlled (mount /
           unmount), the checkbox only makes daisyUI resolve the open state. -->
      <input
        type="checkbox"
        class="drawer-toggle"
        defaultChecked
        tabindex=${-1}
        aria-hidden="true"
      />
      <div class="drawer-side ${zClass}">
        <!-- Backdrop: daisyUI provides the scrim color; properties-backdrop
             adds the fade in/out timing (end panels only). Click to close.
             Scoped drawers are transparent and rely on the caller's own
             full-window backdrop, so they skip properties-backdrop to avoid a
             duplicate dimming layer.
             cursor-pointer is required for iOS Safari: it does not dispatch a
             click on a plain non-interactive <div> on tap unless the element
             carries cursor:pointer, so without it outside-taps would never
             close the drawer on iPhone. -->
        <div
          class="drawer-overlay cursor-pointer ${animate && !scoped
            ? "properties-backdrop"
            : ""} ${closing}"
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
