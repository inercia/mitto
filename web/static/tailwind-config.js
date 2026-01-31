// Tailwind CSS configuration for Mitto
// This file configures Tailwind's Play CDN with custom theme colors

tailwind.config = {
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        "mitto-bg": "var(--mitto-bg)",
        "mitto-sidebar": "var(--mitto-sidebar)",
        "mitto-chat": "var(--mitto-chat)",
        "mitto-input": "var(--mitto-input)",
        "mitto-input-box": "var(--mitto-input-box)",
        "mitto-user": "var(--mitto-user)",
        "mitto-user-text": "var(--mitto-user-text)",
        "mitto-user-border": "var(--mitto-user-border)",
        "mitto-agent": "var(--mitto-agent)",
      },
    },
  },
};
