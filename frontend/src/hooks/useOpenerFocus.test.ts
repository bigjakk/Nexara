import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useOpenerFocus } from "./useOpenerFocus";

// FocusScope dispatches its close event cancelable, as this one is.
function closeEvent() {
  return new Event("focusScope.autoFocusOnUnmount", { cancelable: true });
}

function button(name: string) {
  const el = document.createElement("button");
  el.textContent = name;
  document.body.append(el);
  return el;
}

function region() {
  const el = document.createElement("div");
  el.tabIndex = -1;
  document.body.append(el);
  return el;
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("useOpenerFocus", () => {
  it("sends focus back to what had it when the dialog opened", () => {
    const opener = button("Edit");
    const inside = button("Save");
    opener.focus();
    const { result } = renderHook(() => useOpenerFocus(true));
    inside.focus();

    const event = closeEvent();
    result.current(event);

    expect(document.activeElement).toBe(opener);
    // Or Radix would go on to focus its (empty) trigger.
    expect(event.defaultPrevented).toBe(true);
  });

  it("records the opener when the dialog opens, not when the hook mounts closed", () => {
    const early = button("Somewhere");
    const opener = button("Edit");
    early.focus();
    const { result, rerender } = renderHook(
      ({ open }: { open: boolean }) => useOpenerFocus(open),
      { initialProps: { open: false } },
    );
    opener.focus();
    rerender({ open: true });
    button("Save").focus();

    result.current(closeEvent());

    expect(document.activeElement).toBe(opener);
  });

  it("keeps the opener while the dialog closes, when focus is inside it", () => {
    const opener = button("Edit");
    const inside = button("Save");
    opener.focus();
    const { result, rerender } = renderHook(
      ({ open }: { open: boolean }) => useOpenerFocus(open),
      { initialProps: { open: true } },
    );
    inside.focus();
    // Radix asks where focus goes after the open state has turned false.
    rerender({ open: false });

    result.current(closeEvent());

    expect(document.activeElement).toBe(opener);
  });

  it("records a new opener for each opening", () => {
    const first = button("Edit A");
    const second = button("Edit B");
    first.focus();
    const { result, rerender } = renderHook(
      ({ open }: { open: boolean }) => useOpenerFocus(open),
      { initialProps: { open: true } },
    );
    rerender({ open: false });
    second.focus();
    rerender({ open: true });
    button("Save").focus();

    result.current(closeEvent());

    expect(document.activeElement).toBe(second);
  });

  it("stays on the opener when it takes focus, without the fallback", () => {
    const opener = button("Edit");
    opener.focus();
    const fallback = vi.fn(region);
    const { result } = renderHook(() => useOpenerFocus(true, fallback));
    button("Cancel").focus();

    result.current(closeEvent());

    expect(document.activeElement).toBe(opener);
    expect(fallback).not.toHaveBeenCalled();
  });

  it("falls back when the opener is disabled by then", () => {
    const opener = button("Edit");
    const card = region();
    opener.focus();
    const { result } = renderHook(() => useOpenerFocus(true, () => card));
    button("Save").focus();
    opener.disabled = true;

    result.current(closeEvent());

    expect(document.activeElement).toBe(card);
  });

  it("falls back when the opener is gone by then", () => {
    const opener = button("Replace device");
    const card = region();
    opener.focus();
    const { result } = renderHook(() => useOpenerFocus(true, () => card));
    button("Cancel").focus();
    opener.remove();

    result.current(closeEvent());

    expect(document.activeElement).toBe(card);
  });

  it("falls back when nothing had focus when the dialog opened", () => {
    const card = region();
    const bodyFocus = vi.spyOn(document.body, "focus");
    const { result } = renderHook(() => useOpenerFocus(true, () => card));
    button("Cancel").focus();

    result.current(closeEvent());

    expect(document.activeElement).toBe(card);
    expect(bodyFocus).not.toHaveBeenCalled();
  });
});
