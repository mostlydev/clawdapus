module.exports = {
  content: [
    "./templates/*.html",
    "./static/app.js"
  ],
  theme: {
    extend: {
      colors: {
        ink: "#d4dce8",
        muted: "#5e7085",
        chrome: "#0c1017",
        panel: "#131a24",
        "panel-2": "#19222f",
        line: "#1f2d3d",
        coral: "#22d3ee",
        ember: "#f0a500",
        cyan: "#22d3ee",
        green: "#34d399",
        amber: "#f0a500",
        red: "#ef4444",
        violet: "#a78bfa"
      },
      boxShadow: {
        dash: "0 0 0 rgba(0, 0, 0, 0)"
      },
      borderRadius: {
        "4xl": "2rem"
      },
      fontFamily: {
        sans: ["Outfit", "sans-serif"],
        mono: ["Geist Mono", "monospace"]
      }
    }
  },
  plugins: []
};
