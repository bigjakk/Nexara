import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { NodeStepBadge } from "./NodeStepBadge";

describe("NodeStepBadge", () => {
  it("names the step", () => {
    render(<NodeStepBadge step="draining" />);
    expect(screen.getByText("Draining")).toBeInTheDocument();
  });

  /**
   * A node that owes a reboot has not finished. This badge is the only surface
   * anywhere that tells an operator a reboot is pending, so it must not also be
   * the one saying the work is done — an in-place upgrade that pulls a kernel
   * reaches step "completed" with the reboot deferred, and "Completed" is read
   * as "nothing left to do".
   */
  it("reports an owed reboot instead of Completed", () => {
    render(<NodeStepBadge step="completed" rebootRequired />);
    expect(screen.getByText("Reboot required")).toBeInTheDocument();
    expect(screen.queryByText("Completed")).not.toBeInTheDocument();
  });

  it("leaves a finished node alone when nothing is owed", () => {
    render(<NodeStepBadge step="completed" />);
    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.queryByText("Reboot required")).not.toBeInTheDocument();
  });
});
