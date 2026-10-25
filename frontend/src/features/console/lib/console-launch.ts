import type { ConsoleTarget } from "../types/console";

/**
 * A launch: Nexara's word, left for one new console tab, that the console in
 * it is the one a launcher opened for this user. The tab dials it without
 * asking (ConsoleSessionPage), once. Nothing but Nexara's own script can write
 * one — not another site, not a link a guest prints in a terminal, not the
 * login page bringing a console link back for whoever signs in next (the
 * launch rides in the hash, which returnTo does not carry).
 */
const LAUNCH_PREFIX = "nexara-console-launch:";
// Long enough for a tab opened behind this one to be looked at: it judges its
// launch only once its link has been checked, and a check that failed waits
// for the tab to be shown before it tries again.
const LAUNCH_TTL_MS = 5 * 60_000;

interface Launch {
  user: string;
  console: string;
  until: number;
}

/** A console's identity in a tab: not its node, which follows a migration. */
export function consoleIdentity(target: ConsoleTarget): string {
  return [
    target.clusterID,
    target.type,
    target.type === "node_shell" ? target.node : String(target.vmid),
  ].join("|");
}

function readLaunch(key: string): Partial<Launch> | null {
  try {
    return JSON.parse(localStorage.getItem(key) ?? "null") as Partial<Launch>;
  } catch {
    return null;
  }
}

/**
 * Leaves a launch of `target` for `user` and returns its id for the new tab's
 * hash, or null when storage refuses it — the tab then asks before it dials.
 * Launches nothing took up (a blocked tab, one closed before it signed in) are
 * dropped on the way.
 */
export function leaveLaunch(
  target: ConsoleTarget,
  user: string,
): string | null {
  // getRandomValues, not randomUUID: plain-HTTP installs have no secure context.
  const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
  const now = Date.now();
  try {
    for (const key of Object.keys(localStorage)) {
      if (!key.startsWith(LAUNCH_PREFIX)) continue;
      const until = readLaunch(key)?.until;
      if (typeof until !== "number" || until < now)
        localStorage.removeItem(key);
    }
    const launch: Launch = {
      user,
      console: consoleIdentity(target),
      until: now + LAUNCH_TTL_MS,
    };
    localStorage.setItem(LAUNCH_PREFIX + id, JSON.stringify(launch));
    return id;
  } catch {
    return null;
  }
}

/** Launches taken out of storage by this page load: see takeLaunch. */
const taken = new Map<string, Partial<Launch> | null>();

/**
 * Whether the launch a URL hash names was left for `user` and the console
 * `identity`, and is fresh. It is taken out of storage — once only — and kept
 * for this page load, so that a render React repeats (StrictMode, a discarded
 * render) finds it again; who it was for is asked every time.
 */
export function takeLaunch(
  hash: string,
  user: string,
  identity: string,
): boolean {
  const id = new URLSearchParams(hash.replace(/^#/, "")).get("launch");
  if (id === null || !/^[0-9a-f]{32}$/.test(id)) return false;
  let launch = taken.get(id);
  if (launch === undefined) {
    launch = readLaunch(LAUNCH_PREFIX + id);
    taken.set(id, launch);
    try {
      localStorage.removeItem(LAUNCH_PREFIX + id);
    } catch {
      // It expires on its own.
    }
  }
  return (
    launch?.user === user &&
    launch.console === identity &&
    typeof launch.until === "number" &&
    Date.now() < launch.until
  );
}
