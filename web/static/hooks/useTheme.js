// web/static/hooks/useTheme.js
// Theme, font-size, and reduced-motion preference manager for Mitto Web Interface.
// Owns the light/dark theme, font size, follow-system-theme, and reduce-animations
// clusters: their localStorage persistence, OS-preference syncing, document class
// application, and the SettingsDialog window-event bridges. Returns only the values
// the App render consumes; the follow-system and reduced-motion state stays internal.
const { useState, useEffect, useCallback } = window.preact;

/**
 * Theme / font-size / reduced-motion preferences hook.
 * Returns { theme, toggleTheme, fontSize, toggleFontSize }.
 */
export function useTheme() {
  // Follow system theme state - persisted to localStorage
  const [followSystemTheme, setFollowSystemTheme] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-follow-system-theme");
      // Default to true for new users (follow system theme by default)
      return saved === null ? true : saved === "true";
    }
    return true;
  });

  // Theme state - respects OS preference when followSystemTheme is enabled
  const [theme, setTheme] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const followSystem = localStorage.getItem("mitto-follow-system-theme");
      // If following system theme (default for new users)
      if (followSystem === null || followSystem === "true") {
        if (typeof window !== "undefined" && window.matchMedia) {
          const prefersDark = window.matchMedia(
            "(prefers-color-scheme: dark)",
          ).matches;
          return prefersDark ? "dark" : "light";
        }
      }
      // Otherwise use saved theme preference
      const saved = localStorage.getItem("mitto-theme");
      if (saved) return saved;
    }
    // Check OS preference for dark/light mode
    if (typeof window !== "undefined" && window.matchMedia) {
      const prefersDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      return prefersDark ? "dark" : "light";
    }
    // Fallback: If v2 theme is active (set by index.html script), default to light
    if (
      window.mittoTheme === "v2" ||
      document.documentElement.classList.contains("v2-theme")
    ) {
      return "light";
    }
    return "dark";
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

  // Persist followSystemTheme to localStorage
  useEffect(() => {
    localStorage.setItem(
      "mitto-follow-system-theme",
      String(followSystemTheme),
    );
  }, [followSystemTheme]);

  // Apply theme class to document
  useEffect(() => {
    const root = document.documentElement;
    if (theme === "light") {
      root.classList.add("light");
      root.classList.remove("dark");
      // Also apply to body for v2-theme CSS selectors (which use .v2-theme.dark)
      document.body.classList.add("light");
      document.body.classList.remove("dark");
    } else {
      root.classList.add("dark");
      root.classList.remove("light");
      // Also apply to body for v2-theme CSS selectors (which use .v2-theme.dark)
      document.body.classList.add("dark");
      document.body.classList.remove("light");
    }
    localStorage.setItem("mitto-theme", theme);
    // Update Mermaid.js theme for new diagrams
    if (typeof window.updateMermaidTheme === "function") {
      window.updateMermaidTheme(theme);
    }
  }, [theme]);

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

  // Follow system reduced motion state - persisted to localStorage
  const [followSystemReducedMotion, setFollowSystemReducedMotion] = useState(
    () => {
      if (typeof localStorage !== "undefined") {
        const saved = localStorage.getItem(
          "mitto-follow-system-reduced-motion",
        );
        // Default to true for new users (respect OS preference by default)
        return saved === null ? true : saved === "true";
      }
      return true;
    },
  );

  // Reduce animations state - respects OS preference when followSystemReducedMotion is enabled
  const [reduceAnimations, setReduceAnimations] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const followSystem = localStorage.getItem(
        "mitto-follow-system-reduced-motion",
      );
      // If following system preference (default for new users)
      if (followSystem === null || followSystem === "true") {
        if (typeof window !== "undefined" && window.matchMedia) {
          if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
            return true;
          }
        }
        // Auto-enable on mobile/tablet (iPad reports as Macintosh with touch support)
        if (typeof navigator !== "undefined") {
          const ua = navigator.userAgent || "";
          if (/iPad|iPhone|iPod|Android/i.test(ua) ||
              (navigator.maxTouchPoints > 1 && /Macintosh/i.test(ua))) {
            return true;
          }
        }
      }
      // Otherwise use saved explicit preference
      const saved = localStorage.getItem("mitto-reduce-animations");
      if (saved !== null) return saved === "true";
    }
    // Fallback: check OS preference
    if (typeof window !== "undefined" && window.matchMedia) {
      return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    }
    // Auto-enable on mobile/tablet devices to save battery —
    // backdrop-filter blur causes sustained GPU compositing work
    // even when idle, draining battery on iPad and similar devices.
    if (typeof navigator !== "undefined") {
      const ua = navigator.userAgent || "";
      if (/iPad|iPhone|iPod|Android/i.test(ua) ||
          (navigator.maxTouchPoints > 1 && /Macintosh/i.test(ua))) {
        return true;
      }
    }
    return false;
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

  // Persist followSystemReducedMotion to localStorage
  useEffect(() => {
    localStorage.setItem(
      "mitto-follow-system-reduced-motion",
      String(followSystemReducedMotion),
    );
  }, [followSystemReducedMotion]);

  // Apply reduce-animations class to document
  useEffect(() => {
    const root = document.documentElement;
    if (reduceAnimations) {
      root.classList.add("reduce-animations");
    } else {
      root.classList.remove("reduce-animations");
    }
    localStorage.setItem("mitto-reduce-animations", String(reduceAnimations));
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

  // Font size state - persisted to localStorage
  const [fontSize, setFontSize] = useState(() => {
    if (typeof localStorage !== "undefined") {
      const saved = localStorage.getItem("mitto-font-size");
      if (saved === "small" || saved === "large") return saved;
    }
    return "small"; // Default to small
  });

  // Apply font size class to document
  useEffect(() => {
    const root = document.documentElement;
    if (fontSize === "large") {
      root.classList.add("font-large");
      root.classList.remove("font-small");
    } else {
      root.classList.add("font-small");
      root.classList.remove("font-large");
    }
    localStorage.setItem("mitto-font-size", fontSize);
  }, [fontSize]);

  const toggleFontSize = useCallback(() => {
    setFontSize((prev) => (prev === "small" ? "large" : "small"));
  }, []);

  return { theme, toggleTheme, fontSize, toggleFontSize };
}
