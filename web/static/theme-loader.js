// Theme loader for Mitto
// FOUC boot (woh): synchronously apply the saved named theme + light/dark
// bucket to <html> before the app renders, so there is no flash.

(function () {
  // --- FOUC boot (l6a) ----------------------------------------------------
  // Apply the persisted named daisyUI theme (data-theme) and the matching
  // light/dark class to <html> before first paint. This mirrors useTheme.js
  // two-slot model (l6a); useTheme reconciles the body classes on mount.
  // Runs synchronously because this script is render-blocking in <head>,
  // before <body> exists.
  try {
    // Inherent light/dark bucket per named theme; null = "mitto" passthrough
    // (follow the saved light/dark toggle). Keep in sync with useTheme.js
    // NAMED_THEMES. Derived from color-scheme in tailwind.css.
    var THEME_BUCKETS = {
      mitto: null,
      // Light themes
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
      // Dark themes
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
    var prefersDark = function () {
      return !!(
        window.matchMedia &&
        window.matchMedia("(prefers-color-scheme: dark)").matches
      );
    };
    var root = document.documentElement;

    // Determine effective light/dark bucket for FOUC (mirrors useTheme.js logic).
    var followSystem = localStorage.getItem("mitto-follow-system-theme");
    var savedTheme = localStorage.getItem("mitto-theme");
    var effectiveBucket;
    if (followSystem === null || followSystem === "true") {
      effectiveBucket = prefersDark() ? "dark" : "light";
    } else if (savedTheme === "light" || savedTheme === "dark") {
      effectiveBucket = savedTheme;
    } else {
      effectiveBucket = prefersDark() ? "dark" : "light";
    }

    // Two-slot: pick the theme name for the active bucket.
    // One-pass migration: fall back to old mitto-theme-name if new keys absent.
    var legacy = localStorage.getItem("mitto-theme-name");
    var slotKey =
      effectiveBucket === "light" ? "mitto-theme-light" : "mitto-theme-dark";
    var name = localStorage.getItem(slotKey);
    if (!name || !Object.prototype.hasOwnProperty.call(THEME_BUCKETS, name)) {
      // Migration: use legacy key if it matches the active bucket
      if (
        legacy &&
        Object.prototype.hasOwnProperty.call(THEME_BUCKETS, legacy)
      ) {
        var legacyBucket = THEME_BUCKETS[legacy];
        if (legacyBucket === effectiveBucket || legacyBucket === null) {
          name = legacy;
        }
      }
      if (!name) name = "mitto";
    }
    root.setAttribute("data-theme", name);

    // Effective light/dark bucket: named themes use their inherent scheme; the
    // "mitto" passthrough follows the effective slot bucket.
    var bucket = THEME_BUCKETS[name];
    if (bucket === null) {
      bucket = effectiveBucket;
    }
    if (bucket === "light") {
      root.classList.add("light");
      root.classList.remove("dark");
    } else {
      root.classList.add("dark");
      root.classList.remove("light");
    }
  } catch (e) {
    // localStorage/matchMedia may be unavailable — fall back to default styling.
  }
})();
