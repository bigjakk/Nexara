import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { setupUser } from "@/test/user";
import { makeRollingUpdateJob } from "@/test/rolling-update.fixtures";
import type { RollingUpdateJob } from "@/types/api";
import { RollingUpdateJobsTable } from "./RollingUpdateJobsTable";
import { RollingUpdateProgress } from "./RollingUpdateProgress";

const JOB = makeRollingUpdateJob();

afterEach(() => {
  vi.unstubAllGlobals();
});

const JOB_PATH = "/api/v1/clusters/c1/rolling-updates/j1";

function serve(job: RollingUpdateJob) {
  return stubApi({
    [JOB_PATH]: job,
    [`${JOB_PATH}/nodes`]: listOf(
      ["pve-01", "pve-02", "pve-03"].map((node_name, i) => ({
        id: `n${String(i)}`,
        job_id: "j1",
        node_name,
        node_order: i,
        step: "pending",
        failure_reason: "",
      })),
    ),
  });
}

function renderTable(job: RollingUpdateJob) {
  serve(job);
  const onStart = vi.fn();
  const onCancel = vi.fn();
  renderWithProviders(
    <RollingUpdateJobsTable
      jobs={[job]}
      canManage
      onSelect={vi.fn()}
      onStart={onStart}
      onCancel={onCancel}
    />,
  );
  return { onStart, onCancel, user: setupUser() };
}

describe("RollingUpdateJobsTable — Start and Cancel ask first", () => {
  it("names the node count and the steps before it starts the job", async () => {
    const { onStart, user } = renderTable(JOB);
    // View stays first, where a habitual click lands.
    const [, row] = screen.getAllByRole("row");
    if (row === undefined) throw new Error("no job row");
    expect(within(row).getAllByRole("button")[0]).toHaveTextContent("View");

    await user.click(screen.getByRole("button", { name: "Start" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      await within(dialog).findByRole("heading", {
        name: "Start the rolling update on 3 nodes?",
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Each node is drained first");
    expect(dialog).toHaveTextContent("Then the node is rebooted.");
    expect(dialog).toHaveTextContent("HA rules that name one of the node's");
    expect(onStart).not.toHaveBeenCalled();
    // Red means danger only: starting is not drawn as one, cancelling is.
    const start = within(dialog).getByRole("button", { name: "Start" });
    expect(start.className).not.toContain("bg-destructive");

    await user.click(start);
    expect(onStart).toHaveBeenCalledWith("j1");
  });

  // Only the SSH path reads /var/run/reboot-required; a confirmed manual
  // upgrade never marks a node Reboot required.
  it("promises no Reboot required mark for a manual in-place job", async () => {
    const { user } = renderTable({
      ...JOB,
      auto_upgrade: false,
      drain_guests: false,
      reboot_after_update: false,
    });

    await user.click(screen.getByRole("button", { name: "Start" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("The node is not rebooted.");
    expect(dialog).not.toHaveTextContent("Reboot required");
  });

  it("says what a cancel leaves behind before it cancels a running job", async () => {
    const { onCancel, user } = renderTable({ ...JOB, status: "running" });

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(
      "an upgrade already running finishes, but the node is not rebooted",
    );
    expect(onCancel).not.toHaveBeenCalled();
    const cancel = within(dialog).getByRole("button", { name: "Cancel job" });
    expect(cancel.className).toContain("bg-destructive");

    await user.click(cancel);
    expect(onCancel).toHaveBeenCalledWith("j1");
  });
});

describe("RollingUpdateProgress — Cancel asks first", () => {
  it("sends the cancel only from the confirmation", async () => {
    const api = serve({ ...JOB, status: "paused" });
    const user = setupUser();
    renderWithProviders(
      <RollingUpdateProgress clusterId="c1" jobId="j1" canManage />,
    );

    await user.click(await screen.findByRole("button", { name: "Cancel" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(api.writes()).toEqual([]);
    await user.click(
      within(dialog).getByRole("button", { name: "Cancel job" }),
    );
    await waitFor(() => {
      expect(api.writes()).toEqual([`POST ${JOB_PATH}/cancel`]);
    });
  });
});

describe("RollingUpdateProgress — held-back packages", () => {
  // A package the job holds back is listed with the node's updates; it is
  // marked, and not counted among those to be installed.
  it("marks the job's package excludes and leaves them out of the count", async () => {
    const pkg = (Package: string) => ({
      Package,
      OldVersion: "1",
      Version: "2",
    });
    stubApi({
      [JOB_PATH]: { ...JOB, package_excludes: ["proxmox-default-kernel"] },
      [`${JOB_PATH}/nodes`]: listOf([
        {
          id: "n0",
          job_id: "j1",
          node_name: "pve-01",
          node_order: 0,
          step: "pending",
          failure_reason: "",
          packages_json: [pkg("proxmox-default-kernel"), pkg("pve-manager")],
        },
      ]),
    });
    const user = setupUser();
    renderWithProviders(
      <RollingUpdateProgress clusterId="c1" jobId="j1" canManage />,
    );

    await user.click(await screen.findByRole("button", { name: "pve-01" }));
    expect(screen.getByText("Packages (1)")).toBeInTheDocument();
    const held = screen.getByRole("row", { name: /proxmox-default-kernel/ });
    expect(held).toHaveTextContent("Held back");
    expect(
      screen.getByRole("row", { name: /pve-manager/ }),
    ).not.toHaveTextContent("Held back");
  });
});

describe("RollingUpdateProgress — waiting for Ceph", () => {
  // A node held by the Ceph gate says so, and why, on the job and the node.
  it("names the node that waits for Ceph and the reason", async () => {
    const reason = "28 of 128 placement groups are not active+clean";
    stubApi({
      [JOB_PATH]: {
        ...JOB,
        status: "running",
        ceph_present: true,
        ceph_noout_state: "held",
      },
      [`${JOB_PATH}/nodes`]: listOf([
        {
          id: "n0",
          job_id: "j1",
          node_name: "pve-01",
          node_order: 0,
          step: "health_check",
          failure_reason: "",
          ceph_wait_reason: reason,
        },
      ]),
    });
    renderWithProviders(
      <RollingUpdateProgress clusterId="c1" jobId="j1" canManage />,
    );

    expect(
      await screen.findByText(`pve-01 is waiting for Ceph: ${reason}`),
    ).toBeInTheDocument();
    expect(screen.getByText("Waiting for Ceph")).toBeInTheDocument();
    expect(screen.getByText("waits, noout held")).toBeInTheDocument();
  });
});
