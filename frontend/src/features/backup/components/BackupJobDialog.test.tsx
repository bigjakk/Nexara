import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { BackupJobDialog } from "./BackupJobDialog";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);

// Everything the form sends that isn't the guest selection under test.
const commonFields = {
  enabled: 1,
  schedule: "02:00",
  storage: "pbs-store",
  node: "",
  mode: "snapshot",
  compress: "zstd",
  comment: "",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockedGet.mockImplementation((path: string) => {
    if (path.endsWith("/storage")) {
      return Promise.resolve([
        {
          id: "s1",
          storage: "pbs-store",
          type: "pbs",
          content: "backup",
          enabled: true,
          avail: 1024,
        },
        // Not a backup target: must not be offered, and must not defeat the
        // single-candidate preselect.
        {
          id: "s2",
          storage: "local-lvm",
          type: "lvmthin",
          content: "images,rootdir",
          enabled: true,
          avail: 2048,
        },
      ] as never);
    }
    if (path.endsWith("/nodes")) {
      return Promise.resolve([{ id: "n1", name: "pve1" }] as never);
    }
    if (path.endsWith("/vms")) {
      return Promise.resolve([
        { id: "v1", vmid: 101, name: "web", type: "qemu", node_id: "n1" },
        { id: "v2", vmid: 102, name: "db", type: "lxc", node_id: "n1" },
      ] as never);
    }
    return Promise.resolve([] as never);
  });
  mockedPost.mockResolvedValue({});
  mockedPut.mockResolvedValue({});
});

async function openCreate() {
  const user = userEvent.setup();
  renderWithProviders(<BackupJobDialog clusterId="cluster-1" />);
  await user.click(screen.getByRole("button", { name: /Add Schedule/i }));
  // The lone backup-capable storage is filled in for us.
  await screen.findByText("pbs-store");
  return user;
}

function submitButton(name: RegExp) {
  return within(screen.getByRole("dialog")).getByRole("button", { name });
}

// The picker is disabled until the cluster's guests have loaded.
async function openGuestPicker(
  user: ReturnType<typeof userEvent.setup>,
  label: string,
) {
  const picker = await screen.findByRole("combobox", { name: label });
  await waitFor(() => {
    expect(picker).toBeEnabled();
  });
  await user.click(picker);
}

async function pickOption(
  user: ReturnType<typeof userEvent.setup>,
  selectName: string,
  optionName: string | RegExp,
) {
  await user.click(screen.getByRole("combobox", { name: selectName }));
  await user.click(await screen.findByRole("option", { name: optionName }));
}

describe("BackupJobDialog", () => {
  it("creates an all-guests job with the default daily schedule", async () => {
    const user = await openCreate();
    await user.click(submitButton(/^Create$/));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    // all=1 is what makes PVE treat this as an every-guest job; an empty vmid
    // is not the same thing and the job would back up nothing.
    expect(mockedPost).toHaveBeenCalledWith(
      "/api/v1/clusters/cluster-1/backup-jobs",
      { ...commonFields, all: 1 },
    );
  });

  it("sends the picked VMIDs when scoped to selected guests", async () => {
    const user = await openCreate();
    await pickOption(user, "Guests", "Selected guests");

    expect(submitButton(/^Create$/)).toBeDisabled();
    await openGuestPicker(user, "Included guests");
    await user.click(await screen.findByRole("option", { name: /102 db/ }));
    await user.click(await screen.findByRole("option", { name: /101 web/ }));
    await user.keyboard("{Escape}");

    await user.click(submitButton(/^Create$/));
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    // Sorted numerically, not by click order.
    expect(mockedPost).toHaveBeenCalledWith(
      "/api/v1/clusters/cluster-1/backup-jobs",
      { ...commonFields, vmid: "101,102" },
    );
  });

  it("pairs an exclusion list with all=1", async () => {
    const user = await openCreate();
    await pickOption(user, "Guests", "All except selected");
    await openGuestPicker(user, "Excluded guests");
    await user.click(await screen.findByRole("option", { name: /101 web/ }));
    await user.keyboard("{Escape}");

    await user.click(submitButton(/^Create$/));
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost).toHaveBeenCalledWith(
      "/api/v1/clusters/cluster-1/backup-jobs",
      { ...commonFields, all: 1, exclude: "101" },
    );
  });

  it("builds a weekly schedule from the day and time controls", async () => {
    const user = await openCreate();
    await user.click(screen.getByRole("button", { name: "Weekly" }));
    await user.click(screen.getByRole("button", { name: "Sat" }));
    // "sun" is the weekly default, so Sat+Sun are now selected.
    await pickOption(user, "Hour", "23");

    expect(screen.getByText("Every Sat and Sun at 23:00")).toBeInTheDocument();
    await user.click(submitButton(/^Create$/));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost).toHaveBeenCalledWith(
      "/api/v1/clusters/cluster-1/backup-jobs",
      { ...commonFields, schedule: "sat,sun 23:00", all: 1 },
    );
  });

  it("reopens an existing job in the mode it was saved with", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <BackupJobDialog
        clusterId="cluster-1"
        open
        onOpenChange={() => undefined}
        job={{
          id: "backup-1",
          type: "vzdump",
          schedule: "mon..fri 22:30",
          storage: "pbs-store",
          vmid: "102",
          mode: "stop",
          compress: "zstd",
          enabled: 1,
        }}
      />,
    );

    // Schedule parsed back into the builder rather than left as raw text.
    expect(
      await screen.findByText("Every Mon, Tue, Wed, Thu and Fri at 22:30"),
    ).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Guests" })).toHaveTextContent(
      "Selected guests",
    );
    expect(screen.getByText("1 guest selected")).toBeInTheDocument();

    await user.click(submitButton(/^Update$/));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut).toHaveBeenCalledWith(
      "/api/v1/clusters/cluster-1/backup-jobs/backup-1",
      {
        enabled: 1,
        schedule: "mon..fri 22:30",
        storage: "pbs-store",
        node: "",
        mode: "stop",
        compress: "zstd",
        comment: "",
        vmid: "102",
      },
    );
  });

  it("keeps a disabled job disabled", async () => {
    // PVE reports a disabled job as enabled=0; treating that as "not set" used
    // to re-enable the job on the next save.
    const user = userEvent.setup();
    renderWithProviders(
      <BackupJobDialog
        clusterId="cluster-1"
        open
        onOpenChange={() => undefined}
        job={{
          id: "backup-1",
          type: "vzdump",
          schedule: "02:00",
          storage: "pbs-store",
          all: 1,
          enabled: 0,
        }}
      />,
    );

    const enabled = await screen.findByRole("checkbox", { name: "Enabled" });
    expect(enabled).not.toBeChecked();

    await user.click(submitButton(/^Update$/));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut).toHaveBeenCalledWith(
      "/api/v1/clusters/cluster-1/backup-jobs/backup-1",
      { ...commonFields, enabled: 0, all: 1 },
    );
  });

  it("opens a job with an exclusion list in exclude mode", async () => {
    renderWithProviders(
      <BackupJobDialog
        clusterId="cluster-1"
        open
        onOpenChange={() => undefined}
        job={{
          id: "backup-1",
          type: "vzdump",
          schedule: "02:00",
          storage: "pbs-store",
          all: 1,
          exclude: "101",
          enabled: 1,
        }}
      />,
    );

    expect(
      await screen.findByRole("combobox", { name: "Guests" }),
    ).toHaveTextContent("All except selected");
    expect(screen.getByText("1 guest selected")).toBeInTheDocument();
  });

  it("surfaces the API error instead of closing silently", async () => {
    mockedPost.mockRejectedValue(new Error("schedule: invalid calendar event"));
    const user = await openCreate();
    await user.click(submitButton(/^Create$/));

    expect(
      await screen.findByText("schedule: invalid calendar event"),
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
