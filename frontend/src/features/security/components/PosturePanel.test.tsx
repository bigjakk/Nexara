import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { posture } from "../security.fixtures";
import { PosturePanel } from "./PosturePanel";

describe("PosturePanel", () => {
  // Before the first scan the API answers a score of 100 with no findings.
  // Drawn as a score, that reads as a clean bill of health.
  it("shows a sentence and no score for a cluster never scanned", () => {
    renderWithProviders(
      <PosturePanel
        posture={posture({
          status: "no_scans",
          total_nodes: 0,
          scanned_nodes: 0,
        })}
      />,
    );

    expect(screen.getByText(/^No scans have been run yet/)).toBeInTheDocument();
    expect(screen.queryByText("Posture score")).not.toBeInTheDocument();
    expect(screen.queryByText("100")).not.toBeInTheDocument();
  });
});
