import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { CephStatusPanel } from "./CephStatusPanel";
import type { CephPGStateCount, CephStatus } from "../types/ceph";

function status(pgs: CephPGStateCount[]): CephStatus {
  return {
    health: { status: "HEALTH_OK", checks: [] },
    pgmap: {
      bytes_used: 0,
      bytes_avail: 0,
      bytes_total: 0,
      read_bytes_sec: 0,
      write_bytes_sec: 0,
      read_op_per_sec: 0,
      write_op_per_sec: 0,
      num_pgs: pgs.reduce((n, s) => n + s.count, 0),
      pgs_by_state: pgs,
    },
    osdmap: {
      num_osds: 3,
      num_up_osds: 3,
      num_in_osds: 3,
      full: false,
      nearfull: false,
    },
    monmap: { num_mons: 3 },
  };
}

// D9: whether every PG is active+clean is read off the panel, and when not,
// each state with its count, worst first.
describe("the placement groups stat", () => {
  it.each([
    {
      name: "all clean, scrubbing included",
      pgs: [
        { state_name: "active+clean", count: 95 },
        { state_name: "active+clean+scrubbing+deep", count: 2 },
      ],
      want: "All active+clean",
    },
    {
      name: "mixed",
      pgs: [
        { state_name: "active+clean", count: 19 },
        { state_name: "active+undersized+degraded", count: 76 },
        { state_name: "peering", count: 2 },
      ],
      want: "2 peering, 76 active+undersized+degraded, 19 active+clean",
    },
  ])("$name", ({ pgs, want }) => {
    render(<CephStatusPanel status={status(pgs)} />);
    expect(screen.getByText("97")).toBeInTheDocument();
    expect(screen.getByText(want)).toBeInTheDocument();
  });
});
