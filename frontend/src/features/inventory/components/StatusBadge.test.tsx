import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatusBadge } from "./StatusBadge";

describe("StatusBadge", () => {
  it("renders a known status with its configured label", () => {
    render(<StatusBadge status="running" />);
    expect(screen.getByText("Running")).toBeInTheDocument();
  });

  it("renders PVE transient states it does not enumerate without crashing", () => {
    // Regression: statusConfig was typed exhaustive over a closed union, so
    // a guest reporting "prelaunch" crashed every page using the badge with
    // "Cannot read properties of undefined (reading 'className')".
    render(<StatusBadge status="prelaunch" />);
    expect(screen.getByText("Prelaunch")).toBeInTheDocument();
  });

  it("renders arbitrary unknown statuses with the raw value as label", () => {
    render(<StatusBadge status="io-error" />);
    expect(screen.getByText("Io-error")).toBeInTheDocument();
  });
});
