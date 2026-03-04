import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { TimeRangeSelector } from "./TimeRangeSelector";

describe("TimeRangeSelector", () => {
  it("renders all range options", () => {
    renderWithProviders(<TimeRangeSelector />);
    expect(screen.getByTestId("range-live")).toBeInTheDocument();
    expect(screen.getByTestId("range-1h")).toBeInTheDocument();
    expect(screen.getByTestId("range-6h")).toBeInTheDocument();
    expect(screen.getByTestId("range-24h")).toBeInTheDocument();
    expect(screen.getByTestId("range-7d")).toBeInTheDocument();
  });

  it("live button is enabled", () => {
    renderWithProviders(<TimeRangeSelector />);
    expect(screen.getByTestId("range-live")).not.toBeDisabled();
  });

  it("historical ranges are disabled", () => {
    renderWithProviders(<TimeRangeSelector />);
    expect(screen.getByTestId("range-1h")).toBeDisabled();
    expect(screen.getByTestId("range-6h")).toBeDisabled();
    expect(screen.getByTestId("range-24h")).toBeDisabled();
    expect(screen.getByTestId("range-7d")).toBeDisabled();
  });

  it("disabled buttons have 'Coming soon' title", () => {
    renderWithProviders(<TimeRangeSelector />);
    expect(screen.getByTestId("range-1h")).toHaveAttribute("title", "Coming soon");
    expect(screen.getByTestId("range-7d")).toHaveAttribute("title", "Coming soon");
  });
});
