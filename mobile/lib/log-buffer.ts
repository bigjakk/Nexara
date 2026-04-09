/**
 * In-memory ring buffer for app logs. Hooks into console.log/warn/error so any
 * existing logging is captured. Accessible via the DiagnosticsOverlay which
 * can be opened from any screen by long-pressing the title (or by calling
 * `useDiagnostics().open()`).
 *
 * This exists because release APKs may strip console output and we don't
 * always have adb access during on-device testing.
 */

export interface LogEntry {
  id: number;
  timestamp: number;
  level: "log" | "warn" | "error" | "info";
  message: string;
}

const MAX_ENTRIES = 200;

let nextId = 1;
const entries: LogEntry[] = [];
const listeners = new Set<() => void>();

function notify(): void {
  for (const l of listeners) l();
}

function safeStringify(value: unknown): string {
  if (typeof value === "string") return value;
  if (value instanceof Error) {
    return `${value.name}: ${value.message}\n${value.stack ?? ""}`;
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/**
 * Redact anything that looks like a credential before it lands in the
 * ring buffer. The diagnostics overlay is accessible on-device via a
 * long-press, so any string that flows through `push()` is reachable by
 * an attacker with physical access to the phone. Application code is
 * already careful not to log tokens directly — this is belt-and-braces
 * against future code paths, errors whose stack traces capture a URL,
 * or WebView-originated messages that reference `location.href`.
 *
 * Security review R2-H3.
 */
const CREDENTIAL_REDACTIONS: readonly { pattern: RegExp; replacement: string }[] = [
  // token= / access_token= / refresh_token= in URL query strings or JSON.
  {
    pattern: /((?:access[_-]?|refresh[_-]?)?token["']?\s*[:=]\s*["']?)([A-Za-z0-9._~+/=-]{12,})/gi,
    replacement: "$1***",
  },
  // Bearer <jwt>
  { pattern: /(Bearer\s+)([A-Za-z0-9._~+/=-]{12,})/gi, replacement: "$1***" },
  // Authorization header values in stringified objects.
  {
    pattern: /("[Aa]uthorization"\s*:\s*")[^"]+(")/g,
    replacement: "$1***$2",
  },
  // Raw 3-segment JWT shapes (header.payload.signature) that leaked without a key.
  {
    pattern: /\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b/g,
    replacement: "***.***.***",
  },
];

function redactCredentials(message: string): string {
  let out = message;
  for (const { pattern, replacement } of CREDENTIAL_REDACTIONS) {
    out = out.replace(pattern, replacement);
  }
  return out;
}

function push(level: LogEntry["level"], args: unknown[]): void {
  const message = redactCredentials(args.map(safeStringify).join(" "));
  entries.push({
    id: nextId++,
    timestamp: Date.now(),
    level,
    message,
  });
  while (entries.length > MAX_ENTRIES) {
    entries.shift();
  }
  notify();
}

let installed = false;

/**
 * Replace global console methods so anything logged anywhere lands in the
 * buffer. The original methods are still called so logcat (and Metro in dev)
 * keep working.
 */
export function installConsoleHooks(): void {
  if (installed) return;
  installed = true;

  const originals = {
    log: console.log.bind(console),
    info: console.info.bind(console),
    warn: console.warn.bind(console),
    error: console.error.bind(console),
  };

  console.log = (...args: unknown[]) => {
    push("log", args);
    originals.log(...args);
  };
  console.info = (...args: unknown[]) => {
    push("info", args);
    originals.info(...args);
  };
  console.warn = (...args: unknown[]) => {
    push("warn", args);
    originals.warn(...args);
  };
  console.error = (...args: unknown[]) => {
    push("error", args);
    originals.error(...args);
  };

  // Catch any unhandled promise rejections too — these are the most common
  // "nothing happens" failures.
  if (typeof globalThis !== "undefined" && "addEventListener" in globalThis) {
    try {
      (globalThis as unknown as { addEventListener?: (e: string, h: (ev: unknown) => void) => void })
        .addEventListener?.("unhandledrejection", (event: unknown) => {
          const reason = (event as { reason?: unknown }).reason;
          push("error", ["[unhandledrejection]", reason]);
        });
    } catch {
      // ignore
    }
  }
}

export const logBuffer = {
  getAll: (): readonly LogEntry[] => entries.slice(),
  clear: (): void => {
    entries.length = 0;
    notify();
  },
  subscribe: (listener: () => void): (() => void) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
  /** Programmatic logging that bypasses console (useful from low-level libs). */
  push: (level: LogEntry["level"], message: string): void => {
    push(level, [message]);
  },
};
