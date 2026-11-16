import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";

import { stubApi } from "@/test/fetch-stub";
import { renderWithProviders } from "@/test/test-utils";
import { NodeMetricsPanel } from "./NodeMetricsPanel";

// IO wait the node did not report (null) is left out; a reported 0 is shown,
// which the panel used to hide along with the unreported ones.
describe("NodeMetricsPanel IO wait", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each([
    [0, "Load 0.10 · IO wait 0.0%"],
    [0.023, "Load 0.10 · IO wait 2.3%"],
    [null, "Load 0.10"],
  ])("io_wait %s reads %s", async (ioWait, text) => {
    stubApi({});
    renderWithProviders(
      <NodeMetricsPanel
        clusterId="c1"
        nodeId="n1"
        liveMetric={undefined}
        cpuCount={4}
        loadAvg="0.10"
        ioWait={ioWait}
      />,
    );
    expect(await screen.findByText(text)).toBeInTheDocument();
  });
});
