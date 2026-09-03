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

function subscribe(onStoreChange: () => void): () => void {
  if (!hasMatchMedia()) return () => undefined;
  const mql = window.matchMedia(MOBILE_QUERY);
  mql.addEventListener("change", onStoreChange);
  return () => {
    mql.removeEventListener("change", onStoreChange);
  };
}

function getSnapshot(): boolean {
  return hasMatchMedia() ? window.matchMedia(MOBILE_QUERY).matches : false;
}

export function useIsMobile(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, () => false);
}
