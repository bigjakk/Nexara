import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { TimeRangeSelector } from "./TimeRangeSelector";

describe("TimeRangeSelector", () => {
  it("renders all range options", () => {
    renderWithProviders(
      <TimeRangeSelector value="live" onChange={() => {}} />,
    );
    expect(screen.getByTestId("range-live")).toBeInTheDocument();
    expect(screen.getByTestId("range-1h")).toBeInTheDocument();
    expect(screen.getByTestId("range-6h")).toBeInTheDocument();
    expect(screen.getByTestId("range-24h")).toBeInTheDocument();
    expect(screen.getByTestId("range-7d")).toBeInTheDocument();
  });

  it("all buttons are enabled", () => {
    renderWithProviders(
      <TimeRangeSelector value="live" onChange={() => {}} />,
    );
    expect(screen.getByTestId("range-live")).not.toBeDisabled();
    expect(screen.getByTestId("range-1h")).not.toBeDisabled();
    expect(screen.getByTestId("range-6h")).not.toBeDisabled();
    expect(screen.getByTestId("range-24h")).not.toBeDisabled();
    expect(screen.getByTestId("range-7d")).not.toBeDisabled();
  });

  it("highlights the active range with different styling", () => {
    renderWithProviders(
      <TimeRangeSelector value="6h" onChange={() => {}} />,
    );
    const btn6h = screen.getByTestId("range-6h");
    const btnLive = screen.getByTestId("range-live");
    // Active (default variant) and inactive (ghost variant) should have different classes
    expect(btn6h.className).not.toEqual(btnLive.className);
  });

  it("calls onChange when a range is clicked", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithProviders(
      <TimeRangeSelector value="live" onChange={onChange} />,
    );
    await user.click(screen.getByTestId("range-24h"));
    expect(onChange).toHaveBeenCalledWith("24h");
  });
});
