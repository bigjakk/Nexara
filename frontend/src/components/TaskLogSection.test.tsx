import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { TaskLogSection } from "./TaskLogSection";

describe("TaskLogSection", () => {
  // Proxmox answers a rotated-away log with one line of its own, not an
  // error; showing that line read as if it were the task's output.
  it("says a log Proxmox no longer has is unavailable", () => {
    render(
      <TaskLogSection
        lines={[{ n: 1, t: "unable to open file - No such file or directory" }]}
      />,
    );
    expect(
      screen.getByText(/Proxmox did not return this task's log/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/unable to open file/)).not.toBeInTheDocument();
  });
});
