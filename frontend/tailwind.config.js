/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        "bg-app": "rgb(var(--bg-app) / <alpha-value>)",
        "bg-panel": "rgb(var(--bg-panel) / <alpha-value>)",
        "bg-card": "rgb(var(--bg-card) / <alpha-value>)",
        "bg-inset": "rgb(var(--bg-inset) / <alpha-value>)",
        "terminal-bg": "rgb(var(--terminal-bg) / <alpha-value>)",
        border: "rgb(var(--border) / 0.08)",
        "border-strong": "rgb(var(--border-strong) / 0.18)",
        "text-primary": "rgb(var(--text-primary) / <alpha-value>)",
        "text-secondary": "rgb(var(--text-secondary) / 0.68)",
        "text-muted": "rgb(var(--text-muted) / 0.44)",
        accent: "rgb(var(--accent) / <alpha-value>)",
        ok: "rgb(var(--ok) / <alpha-value>)",
        warn: "rgb(var(--warn) / <alpha-value>)",
        error: "rgb(var(--error) / <alpha-value>)",
        info: "rgb(var(--info) / <alpha-value>)",
        neutral: "rgb(var(--neutral) / <alpha-value>)",
      },
      borderRadius: {
        card: "12px",
        control: "8px",
      },
      fontFamily: {
        mono: [
          '"JetBrains Mono"',
          "ui-monospace",
          "SFMono-Regular",
          "Consolas",
          "monospace",
        ],
      },
    },
  },
  plugins: [],
};
