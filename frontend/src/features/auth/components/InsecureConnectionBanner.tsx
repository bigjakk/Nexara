import { useState } from "react";
import { AlertTriangle, X } from "lucide-react";

const DISMISS_KEY = "nexara_http_warning_dismissed";

// Treat these as "secure context" hosts where Set-Cookie with Secure flag
// works over plain HTTP — matches the browser-spec exception so we don't
// nag users on local dev setups.
const SECURE_LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]", "::1"]);

function shouldShowBanner(): boolean {
  if (typeof window === "undefined") return false;
  if (window.location.protocol === "https:") return false;
  if (SECURE_LOCAL_HOSTS.has(window.location.hostname)) return false;
  return window.localStorage.getItem(DISMISS_KEY) !== "1";
}

export function InsecureConnectionBanner() {
  const [visible, setVisible] = useState(shouldShowBanner);

  if (!visible) return null;

  const dismiss = () => {
    window.localStorage.setItem(DISMISS_KEY, "1");
    setVisible(false);
  };

  return (
    <div
      role="alert"
      className="mx-auto mb-4 flex w-full max-w-md items-start gap-3 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-sm text-amber-900 dark:text-amber-200"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      <div className="flex-1 leading-snug">
        <p className="font-medium">Insecure connection</p>
        <p className="mt-1 text-amber-800/90 dark:text-amber-200/80">
          Nexara is being served over plain HTTP. Credentials and session data
          travel unencrypted on your network. For production deployments, put a
          TLS reverse proxy (Caddy, nginx, Traefik) in front of Nexara.
        </p>
      </div>
      <button
        type="button"
        onClick={dismiss}
        aria-label="Dismiss"
        className="rounded p-1 text-amber-800/70 transition-colors hover:bg-amber-500/20 hover:text-amber-900 dark:text-amber-200/70 dark:hover:text-amber-100"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}
