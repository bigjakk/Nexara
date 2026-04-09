/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./app/**/*.{js,jsx,ts,tsx}", "./features/**/*.{js,jsx,ts,tsx}", "./components/**/*.{js,jsx,ts,tsx}"],
  presets: [require("nativewind/preset")],
  theme: {
    extend: {
      colors: {
        // Match Nexara web Shadcn theme (dark-first)
        background: "#0a0a0a",
        foreground: "#fafafa",
        card: "#111111",
        "card-foreground": "#fafafa",
        muted: "#1f1f1f",
        "muted-foreground": "#a1a1aa",
        border: "#262626",
        primary: "#22c55e",
        "primary-foreground": "#052e16",
        destructive: "#ef4444",
        "destructive-foreground": "#fef2f2",
      },
    },
  },
  plugins: [],
};
