// Theme loader for Mitto
// Loads theme configuration and applies v2-theme class if configured

(function () {
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
