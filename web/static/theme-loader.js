// Theme loader for Mitto
// 1. FOUC boot (woh): synchronously apply the saved named theme + light/dark
//    bucket to <html> before the app renders, so there is no flash.
// 2. Loads theme configuration and applies the v2-theme class if configured.

(function () {
  // --- FOUC boot (woh.4) --------------------------------------------------
  // Apply the persisted named daisyUI theme (data-theme) and the matching
  // light/dark class to <html> before first paint. This mirrors useTheme.js
  // (NAMED_THEMES + effective-bucket logic); useTheme reconciles the body
  // classes on mount. Runs synchronously because this script is render-
  // blocking in <head>, before <body> exists.
  try {
    // Inherent light/dark bucket per named theme; null = "mitto" passthrough
    // (follow the saved light/dark toggle). Keep in sync with useTheme.js.
    var THEME_BUCKETS = {
      mitto: null,
      light: "light",
      dark: "dark",
      cupcake: "light",
      nord: "light",
      dracula: "dark",
      sunset: "dark",
      dim: "dark",
    };
    var prefersDark = function () {
      return !!(
        window.matchMedia &&
        window.matchMedia("(prefers-color-scheme: dark)").matches
      );
    };
    var root = document.documentElement;
    var name = localStorage.getItem("mitto-theme-name");
    if (!name || !Object.prototype.hasOwnProperty.call(THEME_BUCKETS, name)) {
      name = "mitto";
    }
    root.setAttribute("data-theme", name);

    // Effective light/dark bucket: named themes use their inherent scheme; the
    // "mitto" passthrough follows the saved toggle, or system pref when
    // following system (the default for new users).
    var bucket = THEME_BUCKETS[name];
    if (bucket === null) {
      var followSystem = localStorage.getItem("mitto-follow-system-theme");
      var saved = localStorage.getItem("mitto-theme");
      if (followSystem === null || followSystem === "true") {
        bucket = prefersDark() ? "dark" : "light";
      } else if (saved === "light" || saved === "dark") {
        bucket = saved;
      } else {
        bucket = prefersDark() ? "dark" : "light";
      }
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

  // Get API prefix (injected by server into the page)
  var apiPrefix = window.mittoApiPrefix || "";

  fetch(apiPrefix + "/api/config", { credentials: "same-origin" })
    .then(function (res) {
      return res.json();
    })
    .then(function (config) {
      if (config && config.web && config.web.theme === "v2") {
        // Add v2-theme class to html element immediately
        document.documentElement.classList.add("v2-theme");
        // Add v2-theme class to body when it's ready
        if (document.body) {
          document.body.classList.add("v2-theme");
        } else {
          document.addEventListener("DOMContentLoaded", function () {
            document.body.classList.add("v2-theme");
          });
        }
        // Store theme for app.js to use
        window.mittoTheme = "v2";
      }
    })
    .catch(function (err) {
      console.error("Failed to load theme config:", err);
    });
})();
