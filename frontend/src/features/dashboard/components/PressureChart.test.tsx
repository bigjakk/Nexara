import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { PressureChart } from "./PressureChart";
import type { PressurePoint } from "@/types/api";

const point = (
  timestamp: number,
  ioSome: number | null,
  cpuFull: number | null = null,
): PressurePoint => ({
  timestamp,
  cpuSome: 0.5,
  cpuFull,
  ioSome,
  ioFull: null,
  memSome: 0,
  memFull: 0,
});

describe("PressureChart", () => {
  it("draws CPU, memory and IO with each one's newest reading", () => {
    renderWithProviders(
      <PressureChart
        data={[point(0, 12.5), point(60_000, 30, 2), point(120_000, null)]}
        timeRange="1h"
        isLoading={false}
        isError={false}
      />,
    );
    for (const id of ["pressure-cpu", "pressure-memory", "pressure-io"]) {
      expect(screen.getByTestId(id)).toBeInTheDocument();
    }
    // IO's newest reading is the one before the trailing gap; CPU has a full.
    expect(screen.getByTestId("pressure-io")).toHaveTextContent("30.0%");
    expect(screen.getByTestId("pressure-cpu")).toHaveTextContent("full 2.0%");
    expect(screen.getByTestId("pressure-io")).not.toHaveTextContent("full");
  });

  it("says what reports pressure when nothing was recorded", () => {
    renderWithProviders(
      <PressureChart
        data={[
          {
            timestamp: 0,
            cpuSome: null,
            cpuFull: null,
            ioSome: null,
            ioFull: null,
            memSome: null,
            memFull: null,
          },
        ]}
        timeRange="1h"
        isLoading={false}
        isError={false}
      />,
    );
    expect(
      screen.getByText(/No pressure recorded in this range/),
    ).toBeInTheDocument();
  });
});
