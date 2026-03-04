import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { HealthScore } from "./HealthScore";

describe("HealthScore", () => {
  it("displays the score value", () => {
    renderWithProviders(<HealthScore score={85} />);
    expect(screen.getByTestId("health-score-value")).toHaveTextContent("85");
  });

  it("renders green color for score >= 80", () => {
    renderWithProviders(<HealthScore score={80} />);
    const progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#22c55e");
  });

  it("renders yellow color for score 60-79", () => {
    renderWithProviders(<HealthScore score={60} />);
    const progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#eab308");
  });

  it("renders orange color for score 40-59", () => {
    renderWithProviders(<HealthScore score={40} />);
    const progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#f97316");
  });

  it("renders red color for score < 40", () => {
    renderWithProviders(<HealthScore score={39} />);
    const progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#ef4444");
  });

  it("shows boundary: 79 is yellow, 80 is green", () => {
    const { unmount } = renderWithProviders(<HealthScore score={79} />);
    let progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#eab308");
    unmount();

    renderWithProviders(<HealthScore score={80} />);
    progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#22c55e");
  });

  it("shows boundary: 59 is orange, 60 is yellow", () => {
    const { unmount } = renderWithProviders(<HealthScore score={59} />);
    let progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#f97316");
    unmount();

    renderWithProviders(<HealthScore score={60} />);
    progressCircle = screen.getByTestId("health-score").querySelectorAll("circle")[1];
    expect(progressCircle?.getAttribute("stroke")).toBe("#eab308");
  });
});
