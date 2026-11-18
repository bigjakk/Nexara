import { useSyncExternalStore } from "react";

// The line the app shell switches layouts on: below it the mobile layout
// (drawer nav, card lists), at or above it the desktop one (fixed sidebar,
// tables). Tailwind's `md:` is nominally the same 768px, but only nominally —
// v4 defines it as `48rem`, so it moves with the root font size while this
// does not. Anything that must agree with THIS line asks this hook.
const MOBILE_QUERY = "(max-width: 767px)";

// jsdom ships no matchMedia unless a test polyfills it. Assume desktop and
// never subscribe, rather than throwing during render: a table that cannot
// read the viewport should still draw a wide one. (A server render never gets
// here at all — React calls getServerSnapshot below and nothing else.)
function hasMatchMedia(): boolean {
  return typeof window.matchMedia === "function";
}

interface MediaSource {
  subscribe: (onStoreChange: () => void) => () => void;
  getSnapshot: () => boolean;
}

// One subscribe/getSnapshot pair per query, made once: useSyncExternalStore
// resubscribes whenever it is handed a new subscribe function.
const sources = new Map<string, MediaSource>();

function sourceFor(query: string): MediaSource {
  let source = sources.get(query);
  if (source === undefined) {
    source = {
      subscribe: (onStoreChange) => {
        if (!hasMatchMedia()) return () => undefined;
        const mql = window.matchMedia(query);
        mql.addEventListener("change", onStoreChange);
        return () => {
          mql.removeEventListener("change", onStoreChange);
        };
      },
      getSnapshot: () =>
        hasMatchMedia() ? window.matchMedia(query).matches : false,
    };
    sources.set(query, source);
  }
  return source;
}

/** Whether `query` matches the viewport now; false where it cannot be read. */
export function useMediaQuery(query: string): boolean {
  const { subscribe, getSnapshot } = sourceFor(query);
  return useSyncExternalStore(subscribe, getSnapshot, () => false);
}

export function useIsMobile(): boolean {
  return useMediaQuery(MOBILE_QUERY);
}

// Desktop widths where the inventory tree beside a page leaves the page too
// little room (at 800 px, 484 px): there the tree starts hidden.
const NARROW_DESKTOP_QUERY = "(min-width: 768px) and (max-width: 1023px)";

export function useIsNarrowDesktop(): boolean {
  return useMediaQuery(NARROW_DESKTOP_QUERY);
}
