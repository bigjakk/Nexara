import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";

import { usePreferencesStore } from "@/stores/preferences-store";
import { DateTime } from "./DateTime";

afterEach(() => {
  vi.useRealTimers();
  usePreferencesStore.getState().setPreferences({ dateDisplay: "auto" });
});

const now = new Date("2026-09-03T12:00:00Z");

// What is on screen follows the preference at once and, under relative,
// moves on by itself, with the exact time as its title and its spoken text.
describe("DateTime", () => {
  it("re-reads on a tick and on a change of preference", () => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    const at = new Date(now.getTime() - 5 * 60_000);
    usePreferencesStore.getState().setPreferences({ dateDisplay: "relative" });
    render(<DateTime value={at.toISOString()} />);

    const time = screen.getByText("5m ago");
    expect(time).toHaveAttribute("title", at.toLocaleString());
    expect(time).toHaveAttribute("dateTime", at.toISOString());
    expect(time).toHaveTextContent(`5m ago (${at.toLocaleString()})`);
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(screen.getByText("6m ago")).toBeInTheDocument();

    act(() => {
      usePreferencesStore.getState().setPreferences({ dateDisplay: "locale" });
    });
    expect(screen.getByText(at.toLocaleString())).not.toHaveAttribute("title");
  });

  // A narrow cell keeps the exact time one hover away in an absolute format.
  it("writes a compact cell as the clock today, with the exact time", () => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    usePreferencesStore.getState().setPreferences({ dateDisplay: "iso" });
    render(<DateTime value={now} compact />);

    const pad = (n: number) => String(n).padStart(2, "0");
    const time = screen.getByText(`${pad(now.getHours())}:${pad(now.getMinutes())}`);
    expect(time.getAttribute("title")).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  });
});
