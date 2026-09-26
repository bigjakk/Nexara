import { describe, it, expect, afterEach, vi } from "vitest";
import { act, screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import {
  SCHEDULE_IN_FLIGHT_POLL_MS,
  type ScheduledTask,
} from "../api/vm-queries";
import { SchedulePanel } from "./SchedulePanel";

// The Status cell. A schedule's last_status used to say only whether the run
// had been SENT to Proxmox: the scheduler wrote "success" the moment Proxmox
// accepted the call, and Proxmox refuses much of what it refuses inside the
// task afterwards, so the cell read Success while every run failed. The
// scheduler now records "dispatched" until the task ends and then "success" or
// "failed" with Proxmox's exit status — and the cell must read the in-flight
// values as Running, not as the Failed it showed for anything but "success".
//
// A file of its own because SchedulePanel.test.tsx mocks the vm-queries hooks;
// this renders the rows the real hook reads off the wire.

const CLUSTER = "c1";

function schedule(
  id: string,
  cron: string,
  lastStatus: string | null,
  lastError: string | null = null,
): ScheduledTask {
  return {
    id,
    cluster_id: CLUSTER,
    resource_type: "vm",
    resource_id: "101",
    node: "pve-01",
    action: "snapshot",
    schedule: cron,
    params: {},
    enabled: true,
    last_run_at: lastStatus === null ? null : "2026-09-26T02:00:00Z",
    next_run_at: "2026-09-27T02:00:00Z",
    last_status: lastStatus,
    last_error: lastError,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-26T02:00:00Z",
  };
}

const alreadyUsed = "snapshot name 'nightly-20260926-020000' already used";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/** The Status cell of the row whose cron is `cron`. */
async function statusCell(cron: string): Promise<HTMLElement> {
  const row = (await screen.findByText(cron)).closest("tr");
  if (!row) throw new Error(`no table row holds ${cron}`);
  // Action, Schedule, Status, …
  const cell = row.querySelectorAll("td")[2];
  if (!cell) throw new Error(`the row for ${cron} has no Status cell`);
  return cell;
}

describe("SchedulePanel — status", () => {
  it("shows each run's state, reading a run under way as Running", async () => {
    stubApi({
      [`/api/v1/clusters/${CLUSTER}/schedules`]: listOf([
        schedule("sch-1", "0 1 * * *", null),
        schedule("sch-2", "0 2 * * *", "running"),
        schedule("sch-3", "0 3 * * *", "dispatched"),
        schedule("sch-4", "0 4 * * *", "success"),
        schedule("sch-5", "0 5 * * *", "failed", alreadyUsed),
      ]),
    });
    renderWithProviders(
      <SchedulePanel clusterId={CLUSTER} kind="vm" vmid={101} node="pve-01" />,
    );

    const want: [string, string][] = [
      ["0 1 * * *", "Pending"],
      // The claim: the scheduler is starting the run.
      ["0 2 * * *", "Running"],
      // Proxmox started the task and it has not ended.
      ["0 3 * * *", "Running"],
      ["0 4 * * *", "Success"],
      ["0 5 * * *", "Failed"],
    ];
    for (const [cron, label] of want) {
      const cell = await statusCell(cron);
      expect(
        within(cell).getByText(label),
        `the ${cron} row should read ${label}`,
      ).toBeInTheDocument();
      // One state per row: a Running row that also said Failed is the bug
      // this replaces.
      for (const other of ["Pending", "Running", "Success", "Failed"]) {
        if (other !== label) {
          expect(within(cell).queryByText(other)).toBeNull();
        }
      }
    }

    // A failed task's exit status is what the operator reads under Failed.
    expect(
      within(await statusCell("0 5 * * *")).getByText(alreadyUsed),
    ).toBeInTheDocument();
  });
});

// Which rows keep an open tab re-reading. The list is the whole cluster's, the
// panel shows one guest's, and before this any in-flight row anywhere in the
// cluster made every open Schedules tab poll — a stuck one, for good. Only a
// row the panel shows counts, and only an enabled schedule's: nothing
// re-claims a disabled one, so one left reading "running" never moves.
//
// setInterval and Date are faked (see vm-queries.schedules.test.ts for why
// the waiting below is done on setTimeout).
describe("SchedulePanel — polling", () => {
  const LIST = `/api/v1/clusters/${CLUSTER}/schedules`;

  async function until(what: string, cond: () => boolean) {
    for (let i = 0; i < 200; i++) {
      if (cond()) return;
      await act(async () => {
        await new Promise((r) => setTimeout(r, 5));
      });
    }
    throw new Error(`timed out waiting for ${what}`);
  }

  async function advanceAndSettle(ms: number) {
    act(() => {
      vi.advanceTimersByTime(ms);
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
  }

  it.each([
    {
      name: "this guest's run in flight",
      rows: [schedule("sch-1", "0 1 * * *", "dispatched")],
      polls: true,
    },
    {
      name: "another guest's run in flight",
      rows: [
        { ...schedule("sch-2", "0 2 * * *", "dispatched"), resource_id: "102" },
      ],
      polls: false,
    },
    {
      name: "this guest's disabled schedule stuck at running",
      rows: [{ ...schedule("sch-3", "0 3 * * *", "running"), enabled: false }],
      polls: false,
    },
  ])("$name → polls: $polls", async ({ rows, polls }) => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
    // A minute after the fixtures' run began.
    vi.setSystemTime(new Date("2026-09-26T02:01:00Z"));
    const api = stubApi({ [LIST]: listOf(rows) });
    const gets = () => api.sent.filter((r) => r === `GET ${LIST}`).length;

    renderWithProviders(
      <SchedulePanel clusterId={CLUSTER} kind="vm" vmid={101} node="pve-01" />,
    );
    await until(
      "the first read",
      () => gets() === 1 && screen.queryByText("Loading...") === null,
    );

    await advanceAndSettle(SCHEDULE_IN_FLIGHT_POLL_MS);
    expect(gets()).toBe(polls ? 2 : 1);
    if (!polls) {
      await advanceAndSettle(10 * 60_000);
      expect(gets()).toBe(1);
    }
  });
});
