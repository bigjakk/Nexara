import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { TaskProgressCell } from "./TaskProgressCell";

/** The drawn track, or null when the cell rendered text instead of a bar. */
function track(el: HTMLElement) {
  return el.querySelector<HTMLElement>("[class*='rounded-full'] > div");
}

describe("TaskProgressCell", () => {
  it("draws a full bar and 100% for a finished task by default", () => {
    // What the Tasks tables get: they carry their own Status column, so the
    // bar stays a bar and the word "Completed" is said once, next door.
    const { container } = render(<TaskProgressCell display="ok" value={1} />);
    expect(container.textContent).toBe("100%");
    expect(track(container)).not.toBeNull();
  });

  it("says Completed instead of 100% when asked", () => {
    // A finished task is always 100%, so the filled track is the same picture
    // a task one tick from done paints — and "100%" beside it reads as
    // "finishing up". The Activity panel's status column is only an icon, so
    // this is the row's one word for its outcome.
    const { container } = render(
      <TaskProgressCell display="ok" value={1} completedLabel />,
    );
    expect(container.textContent).toBe("Completed");
    expect(track(container)).toBeNull();
  });

  it("keeps the bar for a failed task, at the fraction it reached", () => {
    // How far a failed migration got is the useful part of the row; only the
    // finished case is replaced by a word.
    const { container } = render(
      <TaskProgressCell display="failed" value={0.42} completedLabel />,
    );
    expect(container.textContent).toBe("42%");
    expect(track(container)?.style.width).toBe("42%");
  });

  it("keeps the indeterminate bar for a running task", () => {
    const { container } = render(
      <TaskProgressCell display="running" value={null} completedLabel />,
    );
    expect(track(container)?.className).toContain("animate-task-indeterminate");
  });

  it("shows an em dash for a row that is not a task at all", () => {
    const { container } = render(
      <TaskProgressCell display={null} value={null} completedLabel />,
    );
    expect(container.textContent).toBe("—");
  });
});
