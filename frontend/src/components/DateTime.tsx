import { useSyncExternalStore } from "react";

import {
  formatDateValue,
  type DateInput,
  type DateKind,
} from "@/lib/format";
import { usePreferencesStore } from "@/stores/preferences-store";

/**
 * How often a relative time is re-read. Each DateTime re-renders only when its
 * own text changes ("4m ago" → "5m ago"), so a page of them does not redraw on
 * every tick, and nothing else on the page re-renders at all.
 */
const TICK_MS = 15_000;

const tickListeners = new Set<() => void>();
let tickTimer: ReturnType<typeof setInterval> | null = null;

function subscribe(onChange: () => void): () => void {
  tickListeners.add(onChange);
  tickTimer ??= setInterval(() => {
    if (usePreferencesStore.getState().preferences.dateFormat !== "relative") {
      return;
    }
    for (const listener of tickListeners) listener();
  }, TICK_MS);
  // A change of preference redraws at once, without a reload.
  const unsubscribe = usePreferencesStore.subscribe(onChange);
  return () => {
    unsubscribe();
    tickListeners.delete(onChange);
    if (tickListeners.size === 0 && tickTimer !== null) {
      clearInterval(tickTimer);
      tickTimer = null;
    }
  };
}

/**
 * A date or time as the Date format preference writes it (lib/format.ts),
 * kept current: under relative it moves on by itself and the exact time is
 * its hover title. Use it wherever a date is rendered as text of its own; a
 * date inside a string (an aria-label, a sentence passed as a prop) calls
 * formatDateTime and friends instead.
 */
export function DateTime({
  value,
  kind = "datetime",
  fallback = "—",
  exact = false,
  locale,
}: {
  value: DateInput;
  kind?: DateKind;
  fallback?: string;
  exact?: boolean;
  locale?: Intl.DateTimeFormatOptions;
}) {
  const read = () => formatDateValue(value, { kind, fallback, exact, locale });
  // Switching to or from relative always changes the text, so the title,
  // read here, never lags behind it.
  const text = useSyncExternalStore(subscribe, read);
  const relative =
    usePreferencesStore.getState().preferences.dateFormat === "relative" &&
    !exact &&
    kind !== "time";
  const title = relative
    ? formatDateValue(value, { kind, fallback: "", exact: true, locale })
    : "";
  return title === "" || title === text ? (
    <>{text}</>
  ) : (
    <span title={title}>{text}</span>
  );
}
