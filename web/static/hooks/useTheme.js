// web/static/hooks/useTheme.js
// Theme, font-size, and reduced-motion preference manager for Mitto Web Interface.
// Owns the light/dark theme, font size, follow-system-theme, and reduce-animations
// clusters: their localStorage persistence, OS-preference syncing, document class
// application, and the SettingsDialog window-event bridges. Returns only the values
// the App render consumes; the follow-system and reduced-motion state stays internal.
import {
  getFontSize,
  setFontSize as persistFontSize,
  getTheme,
  setTheme as persistTheme,
  getThemeLight,
  setThemeLight as persistThemeLight,
  getThemeDark,
  setThemeDark as persistThemeDark,
  getFollowSystemTheme,
  setFollowSystemTheme as persistFollowSystemTheme,
  getFollowSystemReducedMotion,
  setFollowSystemReducedMotion as persistFollowSystemReducedMotion,
  getReduceAnimations,
  setReduceAnimations as persistReduceAnimations,
  onUIPreferencesLoaded,
} from "../utils/storage.js";
const { useState, useEffect, useCallback } = window.preact;

// Derive the initial "effective" light/dark theme when nothing is persisted:
// respect the OS preference when available so the first paint doesn't flip.
function osPrefersDark() {
  if (typeof window !== "undefined" && window.matchMedia) {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  }
  return false;
}

// Auto-enable reduced motion on mobile/tablet where backdrop-filter blur
// causes sustained GPU compositing (iPad reports as Macintosh with touch).
function isMobileLikeDevice() {
  if (typeof navigator === "undefined") return false;
  const ua = navigator.userAgent || "";
  return (
    /iPad|iPhone|iPod|Android/i.test(ua) ||
    (navigator.maxTouchPoints > 1 && /Macintosh/i.test(ua))
  );
}

// Named daisyUI themes offered by the theme picker (l6a). "mitto" is the
// default passthrough — the legacy --mitto-* light/dark system stays in
// control. The rest set a data-theme on <html> that drives the --mitto-*
// bridge in tailwind.css. The value is the theme's inherent light/dark
// "bucket" (derived from color-scheme in the generated tailwind.css), used
// to (a) pick the Mermaid diagram theme and (b) keep residual hardcoded
// dark:* / *-slate utilities coherent with the bridge. "mitto" (null)
// follows the live light/dark toggle.
// Keep in sync with theme-loader.js (THEME_BUCKETS).
export const NAMED_THEMES = {
  mitto: null,
  // Light themes (color-scheme: light)
  light: "light",
  cupcake: "light",
  bumblebee: "light",
  emerald: "light",
  corporate: "light",
  retro: "light",
  cyberpunk: "light",
  valentine: "light",
  garden: "light",
  lofi: "light",
  pastel: "light",
  fantasy: "light",
  wireframe: "light",
  cmyk: "light",
  autumn: "light",
  acid: "light",
  lemonade: "light",
  winter: "light",
  nord: "light",
  caramellatte: "light",
  silk: "light",
  // Dark themes (color-scheme: dark)
  dark: "dark",
  synthwave: "dark",
  halloween: "dark",
  forest: "dark",
  aqua: "dark",
  black: "dark",
  luxury: "dark",
  dracula: "dark",
  business: "dark",
  night: "dark",
  coffee: "dark",
  dim: "dark",
  sunset: "dark",
  abyss: "dark",
};

/**
 * Theme / font-size / reduced-motion preferences hook.
 * Returns { theme, toggleTheme, fontSize, toggleFontSize, lightThemeName, darkThemeName }.
 *
 * Two-slot model (l6a): lightThemeName is the daisyUI theme used when the
 * effective mode is light; darkThemeName when dark. The active data-theme on
 * <html> is whichever slot matches the current effectiveBucket.
 */
export function useTheme() {
  // Follow-system-theme state. Prefer the server-synced value (via
  // getFollowSystemTheme); default to true (follow system) when unset.
  const [followSystemTheme, setFollowSystemTheme] = useState(() => {
    const saved = getFollowSystemTheme();
    return saved === null ? true : saved;
  });

  // Explicit light/dark theme. When following system, honour the OS
  // preference; otherwise adopt the persisted value.
  const [theme, setTheme] = useState(() => {
    const savedFollow = getFollowSystemTheme();
    if (savedFollow === null || savedFollow === true) {
      return osPrefersDark() ? "dark" : "light";
    }
    const saved = getTheme();
    if (saved) return saved;
    return osPrefersDark() ? "dark" : "light";
  });

  // Two-slot theme state (l6a): one daisyUI theme per light/dark slot.
  // Persisted to mitto-theme-light / mitto-theme-dark.
  // One-pass migration: if the old mitto-theme-name key exists, seed the
  // matching slot from it and ignore the old key going forward.
  const [lightThemeName, setLightThemeName] = useState(() => {
    const saved = getThemeLight();
    if (saved && Object.prototype.hasOwnProperty.call(NAMED_THEMES, saved)) {
      return saved;
    }
    if (typeof localStorage !== "undefined") {
      // Migration: seed from old single-slot key if it was a light-bucket theme
      const legacy = localStorage.getItem("mitto-theme-name");
      if (
        legacy &&
        Object.prototype.hasOwnProperty.call(NAMED_THEMES, legacy) &&
        (NAMED_THEMES[legacy] === "light" || legacy === "mitto")
      ) {
        return legacy;
      }
    }
    return "mitto";
  });

  const [darkThemeName, setDarkThemeName] = useState(() => {
    const saved = getThemeDark();
    if (saved && Object.prototype.hasOwnProperty.call(NAMED_THEMES, saved)) {
      return saved;
    }
    if (typeof localStorage !== "undefined") {
      // Migration: seed from old single-slot key if it was a dark-bucket theme
      const legacy = localStorage.getItem("mitto-theme-name");
      if (
        legacy &&
        Object.prototype.hasOwnProperty.call(NAMED_THEMES, legacy) &&
        NAMED_THEMES[legacy] === "dark"
      ) {
        return legacy;
      }
    }
    return "mitto";
  });

  // Listen for OS theme changes when followSystemTheme is enabled
  useEffect(() => {
    if (
      !followSystemTheme ||
      typeof window === "undefined" ||
      !window.matchMedia
    ) {
      return;
    }

    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    const handleChange = (e) => {
      setTheme(e.matches ? "dark" : "light");
    };

    // Add listener for theme changes
    mediaQuery.addEventListener("change", handleChange);
    return () => mediaQuery.removeEventListener("change", handleChange);
  }, [followSystemTheme]);

  // Persist followSystemTheme to localStorage + server (see FONT_SIZE_KEY
  // comment in storage.js for why the server-side copy is required).
  useEffect(() => {
    persistFollowSystemTheme(followSystemTheme);
  }, [followSystemTheme]);

  // Apply theme class to document and active data-theme (two-slot model, l6a).
  // The effective slot is determined by the current light/dark mode; the active
  // theme name from that slot drives data-theme on <html>. The slot theme's
  // own bucket (or "mitto" → follow the explicit toggle) sets the light/dark
  // body class so residual dark:* / *-slate utilities stay coherent.
  useEffect(() => {
    // Pick the theme name from the appropriate slot.
    const activeTheme = theme === "light" ? lightThemeName : darkThemeName;
    const scheme = NAMED_THEMES[activeTheme];
    // Effective bucket: named themes use their inherent scheme; "mitto"
    // passthrough follows the explicit light/dark toggle.
    const effective = scheme == null ? theme : scheme;
    const root = document.documentElement;
    if (effective === "light") {
      root.classList.add("light");
      root.classList.remove("dark");
      // Also apply to body so .light/.dark component selectors match.
      document.body.classList.add("light");
      document.body.classList.remove("dark");
    } else {
      root.classList.add("dark");
      root.classList.remove("light");
      // Also apply to body so .light/.dark component selectors match.
      document.body.classList.add("dark");
      document.body.classList.remove("light");
    }
    // Apply the active slot's theme as data-theme on <html>.
    root.setAttribute("data-theme", activeTheme);
    // Persist the explicit light/dark choice (not the effective bucket) so it
    // is restored when switching back to the "mitto" passthrough theme.
    persistTheme(theme);
    // Update Mermaid.js theme for new diagrams to match the effective bucket.
    if (typeof window.updateMermaidTheme === "function") {
      window.updateMermaidTheme(effective);
    }
  }, [theme, lightThemeName, darkThemeName]);

  // Persist slot theme names to localStorage + server.
  useEffect(() => {
    persistThemeLight(lightThemeName);
  }, [lightThemeName]);

  useEffect(() => {
    persistThemeDark(darkThemeName);
  }, [darkThemeName]);

  // Listen for per-slot theme changes dispatched by SettingsDialog (l6a).
  useEffect(() => {
    const handleLightThemeChanged = (e) => {
      const name = e.detail && e.detail.name;
      if (name && Object.prototype.hasOwnProperty.call(NAMED_THEMES, name)) {
        setLightThemeName(name);
      }
    };
    const handleDarkThemeChanged = (e) => {
      const name = e.detail && e.detail.name;
      if (name && Object.prototype.hasOwnProperty.call(NAMED_THEMES, name)) {
        setDarkThemeName(name);
      }
    };
    window.addEventListener(
      "mitto-theme-light-changed",
      handleLightThemeChanged,
    );
    window.addEventListener("mitto-theme-dark-changed", handleDarkThemeChanged);
    return () => {
      window.removeEventListener(
        "mitto-theme-light-changed",
        handleLightThemeChanged,
      );
      window.removeEventListener(
        "mitto-theme-dark-changed",
        handleDarkThemeChanged,
      );
    };
  }, []);

  const toggleTheme = useCallback(() => {
    // When user manually toggles theme, disable follow system theme
    setFollowSystemTheme(false);
    setTheme((prev) => (prev === "dark" ? "light" : "dark"));
  }, []);

  const handleSetFollowSystemTheme = useCallback((value) => {
    setFollowSystemTheme(value);
    // When enabling follow system theme, immediately sync with OS preference
    if (value && typeof window !== "undefined" && window.matchMedia) {
      const prefersDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      setTheme(prefersDark ? "dark" : "light");
    }
  }, []);

  // Listen for follow system theme changes from SettingsDialog
  useEffect(() => {
    const handleFollowSystemThemeChanged = (e) => {
      handleSetFollowSystemTheme(e.detail.enabled);
    };
    window.addEventListener(
      "mitto-follow-system-theme-changed",
      handleFollowSystemThemeChanged,
    );
    return () =>
      window.removeEventListener(
        "mitto-follow-system-theme-changed",
        handleFollowSystemThemeChanged,
      );
  }, [handleSetFollowSystemTheme]);

  // Follow-system-reduced-motion state. Default true when unset.
  const [followSystemReducedMotion, setFollowSystemReducedMotion] = useState(
    () => {
      const saved = getFollowSystemReducedMotion();
      return saved === null ? true : saved;
    },
  );

  // Reduce-animations state. When following system, honour OS preference
  // (falling back to mobile-device auto-enable so battery-sensitive iPads
  // get reduced animation by default); otherwise adopt the persisted value.
  const [reduceAnimations, setReduceAnimations] = useState(() => {
    const savedFollow = getFollowSystemReducedMotion();
    if (savedFollow === null || savedFollow === true) {
      if (
        typeof window !== "undefined" &&
        window.matchMedia &&
        window.matchMedia("(prefers-reduced-motion: reduce)").matches
      ) {
        return true;
      }
      if (isMobileLikeDevice()) return true;
    }
    const saved = getReduceAnimations();
    if (saved !== null) return saved;
    // Fallback: no persisted value at all — check OS + mobile heuristics.
    if (
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches
    ) {
      return true;
    }
    return isMobileLikeDevice();
  });

  // Listen for OS reduced motion changes when followSystemReducedMotion is enabled
  useEffect(() => {
    if (
      !followSystemReducedMotion ||
      typeof window === "undefined" ||
      !window.matchMedia
    ) {
      return;
    }

    const mediaQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const handleChange = (e) => {
      setReduceAnimations(e.matches);
    };

    mediaQuery.addEventListener("change", handleChange);
    return () => mediaQuery.removeEventListener("change", handleChange);
  }, [followSystemReducedMotion]);

  // Persist followSystemReducedMotion to localStorage + server.
  useEffect(() => {
    persistFollowSystemReducedMotion(followSystemReducedMotion);
  }, [followSystemReducedMotion]);

  // Apply reduce-animations class to document and persist to localStorage +
  // server (see FONT_SIZE_KEY comment in storage.js for the rationale).
  useEffect(() => {
    const root = document.documentElement;
    if (reduceAnimations) {
      root.classList.add("reduce-animations");
    } else {
      root.classList.remove("reduce-animations");
    }
    persistReduceAnimations(reduceAnimations);
  }, [reduceAnimations]);

  const handleSetFollowSystemReducedMotion = useCallback((value) => {
    setFollowSystemReducedMotion(value);
    // When enabling follow system, immediately sync with OS preference
    if (value && typeof window !== "undefined" && window.matchMedia) {
      const prefersReduced = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      setReduceAnimations(prefersReduced);
    }
  }, []);

  // Listen for reduce animations changes from SettingsDialog
  useEffect(() => {
    const handleReduceAnimationsChanged = (e) => {
      if (e.detail.followSystem !== undefined) {
        handleSetFollowSystemReducedMotion(e.detail.followSystem);
      }
      if (e.detail.reduceAnimations !== undefined) {
        setReduceAnimations(e.detail.reduceAnimations);
      }
    };
    window.addEventListener(
      "mitto-reduce-animations-changed",
      handleReduceAnimationsChanged,
    );
    return () =>
      window.removeEventListener(
        "mitto-reduce-animations-changed",
        handleReduceAnimationsChanged,
      );
  }, [handleSetFollowSystemReducedMotion]);

  // Font size state — persisted to both localStorage (fast path) and the
  // server via /api/ui-preferences. Server-side persistence is required
  // for the macOS app: it binds a fresh random localhost port on every
  // launch, and localStorage is per-origin (scheme+host+port), so without
  // the server copy the previously-chosen size would reset each restart.
  const [fontSize, setFontSize] = useState(getFontSize);

  // Apply font size class to document and mirror the choice to storage
  // (localStorage + debounced server sync via persistFontSize).
  useEffect(() => {
    const root = document.documentElement;
    if (fontSize === "large") {
      root.classList.add("font-large");
      root.classList.remove("font-small");
    } else {
      root.classList.add("font-small");
      root.classList.remove("font-large");
    }
    persistFontSize(fontSize);
  }, [fontSize]);

  // When the server-side UI preferences finish loading, adopt the persisted
  // values if they differ from what we booted with. localStorage was empty
  // on this launch because of the port-scoped origin (see comment above),
  // so React state was seeded from defaults + OS preferences; this effect
  // brings it in line with what the user actually chose on a prior launch.
  useEffect(() => {
    const unsubscribe = onUIPreferencesLoaded(() => {
      const savedFontSize = getFontSize();
      setFontSize((prev) => (prev === savedFontSize ? prev : savedFontSize));

      const savedFollowTheme = getFollowSystemTheme();
      if (savedFollowTheme !== null) {
        setFollowSystemTheme((prev) =>
          prev === savedFollowTheme ? prev : savedFollowTheme,
        );
      }
      // Only adopt an explicit theme when we're NOT following the system,
      // otherwise the OS listener remains the source of truth.
      const effectiveFollow =
        savedFollowTheme === null ? followSystemTheme : savedFollowTheme;
      if (!effectiveFollow) {
        const savedTheme = getTheme();
        if (savedTheme) {
          setTheme((prev) => (prev === savedTheme ? prev : savedTheme));
        }
      }

      const savedLight = getThemeLight();
      if (
        savedLight &&
        Object.prototype.hasOwnProperty.call(NAMED_THEMES, savedLight)
      ) {
        setLightThemeName((prev) => (prev === savedLight ? prev : savedLight));
      }
      const savedDark = getThemeDark();
      if (
        savedDark &&
        Object.prototype.hasOwnProperty.call(NAMED_THEMES, savedDark)
      ) {
        setDarkThemeName((prev) => (prev === savedDark ? prev : savedDark));
      }

      const savedFollowRM = getFollowSystemReducedMotion();
      if (savedFollowRM !== null) {
        setFollowSystemReducedMotion((prev) =>
          prev === savedFollowRM ? prev : savedFollowRM,
        );
      }
      const effectiveFollowRM =
        savedFollowRM === null ? followSystemReducedMotion : savedFollowRM;
      if (!effectiveFollowRM) {
        const savedReduce = getReduceAnimations();
        if (savedReduce !== null) {
          setReduceAnimations((prev) =>
            prev === savedReduce ? prev : savedReduce,
          );
        }
      }
    });
    return unsubscribe;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const toggleFontSize = useCallback(() => {
    setFontSize((prev) => (prev === "small" ? "large" : "small"));
  }, []);

  return {
    theme,
    toggleTheme,
    fontSize,
    toggleFontSize,
    lightThemeName,
    darkThemeName,
  };
}
