import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { Wizard } from "./Wizard";

const STEPS = [
  { id: "general", label: "General" },
  { id: "disks", label: "Disks" },
  { id: "review", label: "Review" },
] as const;

type StepId = (typeof STEPS)[number]["id"];

/** A wizard that goes where it is told, as a dialog holding the step would. */
function Harness({ onStepChange }: { onStepChange: (id: StepId) => void }) {
  const [current, setCurrent] = useState<StepId>("disks");
  return (
    <Wizard
      steps={STEPS}
      current={current}
      onStepChange={(id) => {
        onStepChange(id);
        setCurrent(id);
      }}
      canGoTo={(id) => id !== "review"}
      footer={<button type="button">Next</button>}
    >
      <p>the step&apos;s fields</p>
    </Wizard>
  );
}

describe("Wizard", () => {
  it("marks the current step, jumps to one that is allowed and holds one that is not", async () => {
    const onStepChange = vi.fn<(id: StepId) => void>();
    renderWithProviders(<Harness onStepChange={onStepChange} />);

    expect(screen.getByRole("button", { name: "Disks" })).toHaveAttribute(
      "aria-current",
      "step",
    );
    // The fields carry the step's name and its place, for when the list is folded away.
    const heading = screen.getByRole("heading", {
      name: /^Step 2 of 3:\s*Disks$/,
    });
    // Opening the wizard leaves focus where the dialog around it puts it.
    expect(heading).not.toHaveFocus();
    expect(screen.getByText("the step's fields")).toBeInTheDocument();

    expect(screen.getByRole("button", { name: "Review" })).toBeDisabled();
    // The picker that stands in for the list on a phone is held to the same rule.
    expect(screen.getByRole("option", { name: "Review" })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: /General/ }));
    expect(onStepChange).toHaveBeenCalledExactlyOnceWith("general");
    // The new step takes focus on its heading: what was pressed may be gone.
    expect(
      screen.getByRole("heading", { name: /^Step 1 of 3:\s*General$/ }),
    ).toHaveFocus();

    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Go to step" }),
      "disks",
    );
    expect(onStepChange).toHaveBeenLastCalledWith("disks");
  });
});
