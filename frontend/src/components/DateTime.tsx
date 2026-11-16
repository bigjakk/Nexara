import { useSyncExternalStore } from "react";

import {
  describeDate,
  formatDateValue,
  type DateInput,
  type DateOptions,
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
    const format = usePreferencesStore.getState().preferences.dateDisplay;
    if (format !== "relative" && format !== "auto") return;
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
 * The text a DateTime would show, for a date that has to sit inside a string
 * (a translated sentence) in a component that stays on screen, such as the
 * shell's: it follows a change of preference at once and moves on by itself.
 */
// eslint-disable-next-line react-refresh/only-export-components -- the hook belongs beside the component it mirrors
export function useDateText(value: DateInput, options?: DateOptions): string {
  return useSyncExternalStore(subscribe, () => formatDateValue(value, options));
}

/**
 * A date or time as the Date format preference writes it (lib/format.ts),
 * kept current: a relative time moves on by itself. Whenever the text is not
 * the exact time (relative, or a compact cell) the exact time is its hover
 * title and is read out after it. Use it wherever a date is rendered as text
 * of its own; a date inside a string (an aria-label, a sentence passed as a
 * prop) calls formatDateTime and friends instead.
 */
export function DateTime({
  value,
  ...options
}: { value: DateInput } & DateOptions) {
  // The text is the snapshot, so a tick re-renders only a changed one; a
  // change of format always changes it, so the rest, read here, never lags.
  const text = useSyncExternalStore(subscribe, () =>
    formatDateValue(value, options),
  );
  const { exact, iso } = describeDate(value, options);
  if (iso === "") return <>{text}</>;
  return (
    <time dateTime={iso} title={exact === "" ? undefined : exact}>
      {text}
      {exact !== "" && <span className="sr-only"> ({exact})</span>}
    </time>
  );
}
