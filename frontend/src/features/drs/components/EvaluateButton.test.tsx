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

/** One click's outcome: a successful response, or a failure with this message. */
type EvaluationStep = { ok: EvaluateResponse } | { failure: string };

/**
 * Drive the mutation hook through a sequence of click outcomes. The last step
 * repeats for any further clicks. `error` is re-read on every render, the way
 * React Query surfaces it, so the component sees the failure after onError's
 * state updates re-render it.
 */
function mockEvaluationSteps(steps: EvaluationStep[]) {
  if (steps.length === 0) {
    throw new Error("mockEvaluationSteps needs at least one step");
  }
  let clicks = 0;
  let error: Error | null = null;
  vi.mocked(useTriggerEvaluation).mockImplementation(
    () =>
      ({
        mutate: (
          _vars: undefined,
          opts?: {
            onSuccess?: (d: EvaluateResponse) => void;
            onError?: () => void;
          },
        ) => {
          const step = steps[Math.min(clicks, steps.length - 1)];
          clicks += 1;
          if (step && "ok" in step) {
            error = null;
            opts?.onSuccess?.(step.ok);
            return;
          }
          error = new Error(step ? step.failure : "evaluation failed");
          opts?.onError?.();
        },
        isPending: false,
        isError: error !== null,
        error,
      }) as unknown as ReturnType<typeof useTriggerEvaluation>,
  );
}

function mockEvaluation(response: EvaluateResponse) {
  mockEvaluationSteps([{ ok: response }]);
}

describe("EvaluateButton native-CRS blocked state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows a blocked notice when evaluation is suppressed by native CRS", async () => {
    mockEvaluation({
      blocked: true,
      block_reason:
        "Proxmox native CRS auto-rebalance is enabled on this cluster.",
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
    // Pin the copy the failure tests below assert the ABSENCE of, so a
    // reworded card breaks a test loudly instead of making them vacuous.
    expect(screen.getByText(/No migrations needed/i)).toBeInTheDocument();
    expect(screen.getByText(/Node Load Scores/i)).toBeInTheDocument();
    expect(screen.getByText(/Variance:/i)).toBeInTheDocument();
  });
});

describe("EvaluateButton failed evaluation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the error alone, with no balance verdict beside it", async () => {
    mockEvaluationSteps([{ failure: "get nodes: 500 internal server error" }]);
    const user = userEvent.setup();
    renderWithProviders(<EvaluateButton clusterId="c1" />);
    await user.click(screen.getByRole("button", { name: /Run Evaluation/i }));

    expect(screen.getByText(/get nodes: 500/i)).toBeInTheDocument();
    // A failed read is not a verdict: nothing may claim the cluster's balance.
    expect(screen.queryByText(/Cluster Balanced/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Imbalanced/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/No migrations needed/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Node Load Scores/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Variance:/i)).not.toBeInTheDocument();
  });

  it("still shows an error line when the failure carries no message", async () => {
    // A proxy 502 with an HTML body falls back to res.statusText, which is ""
    // over HTTP/2. Clearing the results must not leave the panel blank.
    mockEvaluationSteps([{ failure: "" }]);
    const user = userEvent.setup();
    renderWithProviders(<EvaluateButton clusterId="c1" />);
    await user.click(screen.getByRole("button", { name: /Run Evaluation/i }));

    expect(screen.getByText(/Evaluation failed/i)).toBeInTheDocument();
    expect(screen.queryByText(/Cluster Balanced/i)).not.toBeInTheDocument();
  });

  it("drops a prior run's numbers when a later evaluation fails", async () => {
    mockEvaluationSteps([
      {
        ok: {
          blocked: false,
          recommendations: [],
          count: 0,
          node_scores: [
            { node: "pve-01", score: 0.7, cpu_load: 0.7, mem_load: 0.7 },
            { node: "pve-02", score: 0.2, cpu_load: 0.2, mem_load: 0.2 },
          ],
          imbalance: 0.42,
          threshold: 0.2,
        },
      },
      { failure: "read HA status: 500 internal server error" },
    ]);
    const user = userEvent.setup();
    renderWithProviders(<EvaluateButton clusterId="c1" />);
    const button = screen.getByRole("button", { name: /Run Evaluation/i });

    await user.click(button);
    expect(screen.getByText(/Imbalanced/i)).toBeInTheDocument();

    await user.click(button);
    expect(screen.getByText(/read HA status: 500/i)).toBeInTheDocument();
    // The stale imbalance must not be re-asserted as a fresh finding.
    expect(screen.queryByText(/Imbalanced/i)).not.toBeInTheDocument();
    expect(
      screen.queryByText(/no single VM migration would improve the balance/i),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/Cluster Balanced/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Node Load Scores/i)).not.toBeInTheDocument();
  });
});
