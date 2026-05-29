import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { EvaluateButton } from "./EvaluateButton";
import { useTriggerEvaluation } from "../api/drs-queries";
import type { EvaluateResponse } from "../types/drs";

vi.mock("../api/drs-queries", () => ({
  useTriggerEvaluation: vi.fn(),
}));

function mockEvaluation(response: EvaluateResponse) {
  vi.mocked(useTriggerEvaluation).mockReturnValue({
    mutate: (
      _vars: undefined,
      opts?: { onSuccess?: (d: EvaluateResponse) => void; onError?: () => void },
    ) => {
      opts?.onSuccess?.(response);
    },
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useTriggerEvaluation>);
}

describe("EvaluateButton native-CRS blocked state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows a blocked notice when evaluation is suppressed by native CRS", async () => {
    mockEvaluation({
      blocked: true,
      block_reason: "Proxmox native CRS auto-rebalance is enabled on this cluster.",
      recommendations: [],
      count: 0,
      node_scores: [],
      imbalance: 0,
      threshold: 0,
    });
    const user = userEvent.setup();
    renderWithProviders(<EvaluateButton clusterId="c1" />);
    await user.click(screen.getByRole("button", { name: /Run Evaluation/i }));

    expect(screen.getByText(/Evaluation skipped/i)).toBeInTheDocument();
    expect(
      screen.getByText(/native CRS auto-rebalance is enabled/i),
    ).toBeInTheDocument();
    // Must not fall through to the "balanced" success state.
    expect(screen.queryByText(/Cluster Balanced/i)).not.toBeInTheDocument();
  });

  it("shows balanced (not blocked) for a normal evaluation", async () => {
    mockEvaluation({
      blocked: false,
      recommendations: [],
      count: 0,
      node_scores: [{ node: "pve1", score: 0.4, cpu_load: 0.4, mem_load: 0.4 }],
      imbalance: 0.05,
      threshold: 0.25,
    });
    const user = userEvent.setup();
    renderWithProviders(<EvaluateButton clusterId="c1" />);
    await user.click(screen.getByRole("button", { name: /Run Evaluation/i }));

    expect(screen.getByText(/Cluster Balanced/i)).toBeInTheDocument();
    expect(screen.queryByText(/Evaluation skipped/i)).not.toBeInTheDocument();
  });
});
