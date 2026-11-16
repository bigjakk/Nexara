import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";

import { usePreferencesStore } from "@/stores/preferences-store";
import { DateTime } from "./DateTime";

afterEach(() => {
  vi.useRealTimers();
  usePreferencesStore.getState().setPreferences({ dateFormat: "relative" });
});

// What is on screen follows the preference at once and, under relative,
// moves on by itself, with the exact time as its title.
describe("DateTime", () => {
  it("re-reads on a tick and on a change of preference", () => {
    const now = new Date("2026-09-03T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);
    const at = new Date(now.getTime() - 5 * 60_000);
    usePreferencesStore.getState().setPreferences({ dateFormat: "relative" });
    render(<DateTime value={at.toISOString()} />);

    expect(screen.getByText("5m ago")).toHaveAttribute(
      "title",
      at.toLocaleString(),
    );
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(screen.getByText("6m ago")).toBeInTheDocument();

    act(() => {
      usePreferencesStore.getState().setPreferences({ dateFormat: "locale" });
    });
    expect(screen.getByText(at.toLocaleString())).not.toHaveAttribute("title");
  });
});
