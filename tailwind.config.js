/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./web/static/**/*.html",
    "./web/static/**/*.js",
  ],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        // Main app colors (use CSS variables for theme switching)
        "mitto-bg": "var(--mitto-bg)",
        "mitto-sidebar": "var(--mitto-sidebar)",
        "mitto-chat": "var(--mitto-chat)",
        "mitto-input": "var(--mitto-input)",
        "mitto-input-box": "var(--mitto-input-box)",
        "mitto-user": "var(--mitto-user)",
        "mitto-user-text": "var(--mitto-user-text)",
        "mitto-user-border": "var(--mitto-user-border)",
        "mitto-agent": "var(--mitto-agent)",
        "mitto-border": "var(--mitto-border)",
      },
    },
  },
  plugins: [],
};

