import { useSyncExternalStore } from "react";

// Must stay in sync with Tailwind's `md` breakpoint (768px): below it the
// app shell renders the mobile layout (drawer nav, card lists), at or above
// it the desktop layout (fixed sidebar, tables).
const MOBILE_QUERY = "(max-width: 767px)";

function subscribe(onStoreChange: () => void): () => void {
  const mql = window.matchMedia(MOBILE_QUERY);
  mql.addEventListener("change", onStoreChange);
  return () => {
    mql.removeEventListener("change", onStoreChange);
  };
}

function getSnapshot(): boolean {
  return window.matchMedia(MOBILE_QUERY).matches;
}

export function useIsMobile(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot);
}
