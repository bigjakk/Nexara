import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { queryClient } from "./lib/query-client";
import { useAuthStore } from "./stores/auth-store";
import "./lib/i18n"; // Initialize i18next before render
import "./index.css";

// After a redeploy, an already-open tab may lazy-load route chunks whose
// hashed filenames no longer exist; Vite surfaces that as vite:preloadError.
// Reload once to pick up the new shell instead of dying on a blank route.
// The timestamp guard prevents a reload loop if the server keeps failing.
window.addEventListener("vite:preloadError", (event) => {
  const key = "nexara-chunk-reload-at";
  const last = Number(sessionStorage.getItem(key) ?? "0");
  if (Date.now() - last < 30_000) return;
  sessionStorage.setItem(key, String(Date.now()));
  event.preventDefault();
  window.location.reload();
});

// Initialize auth state — calls /auth/refresh against the HttpOnly cookie
// (or, on mobile, the body refresh_token) to validate the session before
// rendering anything that depends on auth.
void useAuthStore.getState().initialize();

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("Root element not found");

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </React.StrictMode>,
);
