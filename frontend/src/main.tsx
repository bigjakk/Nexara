import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { useAuthStore } from "./stores/auth-store";
import "./lib/i18n"; // Initialize i18next before render
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60_000, // 5 minutes — inventory data doesn't change rapidly
      gcTime: 10 * 60_000,   // 10 minutes — keep cache longer to avoid refetches
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

// Initialize auth state from localStorage before render
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
