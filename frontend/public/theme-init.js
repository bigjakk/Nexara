// Pre-paint theme bootstrap, loaded as a classic blocking script from
// index.html. It must stay an external file: the backend CSP is
// script-src 'self', so an inline version is silently blocked (and the
// dark-mode anti-flash never runs). Keep the localStorage contract in
// sync with src/stores/theme-store.ts (key "nexara-theme", raw mode string).
(function () {
  var t = localStorage.getItem("nexara-theme");
  if (t === "dark" || (t !== "light" && matchMedia("(prefers-color-scheme:dark)").matches))
    document.documentElement.classList.add("dark");
})();
