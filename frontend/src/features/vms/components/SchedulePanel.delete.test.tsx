import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import type { ScheduledTask } from "../api/vm-queries";
import { SchedulePanel } from "./SchedulePanel";

// A file of its own: SchedulePanel.test.tsx mocks the vm-queries hooks, and
// these assert on the request that actually leaves.

const CLUSTER = "c1";

function schedule(id: string, action: string, cron: string): ScheduledTask {
  return {
    id,
    cluster_id: CLUSTER,
    resource_type: "vm",
    resource_id: "101",
    node: "pve-01",
    action,
    schedule: cron,
    params: {},
    enabled: true,
    last_run_at: null,
    next_run_at: null,
    last_status: null,
    last_error: null,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
  };
}

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  api = stubApi({
    [`/api/v1/clusters/${CLUSTER}/schedules`]: listOf([
      schedule("sch-1", "snapshot", "0 2 * * *"),
      schedule("sch-2", "snapshot", "0 4 * * 0"),
    ]),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPanel() {
  renderWithProviders(
    <SchedulePanel clusterId={CLUSTER} kind="vm" vmid={101} node="pve-01" />,
  );
}

async function clickDelete(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    await screen.findByRole("button", {
      name: "Delete snapshot schedule 0 4 * * 0",
    }),
  );
}

describe("SchedulePanel — delete", () => {
  it("asks first, naming the schedule, and sends nothing yet", async () => {
    const user = userEvent.setup();
    renderPanel();
    await clickDelete(user);

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Delete the snapshot schedule 0 4 * * 0?");
    expect(dialog).toHaveTextContent(
      "No further snapshot runs are started for this VM.",
    );
    expect(dialog).toHaveTextContent("snapshots earlier runs took are kept");
    expect(api.writes()).toEqual([]);
  });

  it("calls a container a container", async () => {
    vi.unstubAllGlobals();
    api = stubApi({
      [`/api/v1/clusters/${CLUSTER}/schedules`]: listOf([
        { ...schedule("sch-3", "reboot", "0 3 * * *"), resource_type: "ct" },
      ]),
    });
    const user = userEvent.setup();
    renderWithProviders(
      <SchedulePanel clusterId={CLUSTER} kind="ct" vmid={101} node="pve-01" />,
    );
    await user.click(
      await screen.findByRole("button", {
        name: "Delete reboot schedule 0 3 * * *",
      }),
    );

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent(
      "No further reboot runs are started for this container.",
    );
    // Snapshots are mentioned only for a snapshot schedule.
    expect(dialog).not.toHaveTextContent("snapshots");
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    renderPanel();
    await clickDelete(user);

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming deletes exactly the schedule clicked, once", async () => {
    const user = userEvent.setup();
    renderPanel();
    await clickDelete(user);

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([
        `DELETE /api/v1/clusters/${CLUSTER}/schedules/sch-2`,
      ]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual([
      `DELETE /api/v1/clusters/${CLUSTER}/schedules/sch-2`,
    ]);
  });
});
