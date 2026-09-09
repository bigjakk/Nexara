import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { CreateSnapshotDialog } from "./CreateSnapshotDialog";

const capabilityState = vi.hoisted(() => ({
  current: { supported: true, blocking_volumes: [] as string[] },
}));

// Dispatch resolves immediately with a UPID; the task status for any watched
// UPID reports a failed task so the failure path can be exercised.
vi.mock("../api/vm-queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/vm-queries")>();
  return {
    ...actual,
    useSnapshotCapability: () => ({
      data: capabilityState.current,
      isLoading: false,
    }),
    useCreateSnapshot: () => ({
      mutate: (
        _vars: unknown,
        opts?: { onSuccess?: (data: { upid: string }) => void },
      ) => {
        opts?.onSuccess?.({
          upid: "UPID:pve2:0001:0002:0003:vzsnapshot:102:root@pam:",
        });
      },
      isPending: false,
      isError: false,
      reset: () => {},
    }),
    useTaskStatus: (_clusterId: string, upid: string | null) => ({
      data: upid
        ? {
            status: "stopped",
            exit_status: "snapshot feature is not available",
          }
        : undefined,
    }),
  };
});

const defaultProps = {
  open: true,
  onOpenChange: () => {},
  clusterId: "c1",
  resourceId: "vm-1",
  kind: "vm" as const,
  resourceName: "my-test-vm",
};

describe("CreateSnapshotDialog", () => {
  beforeEach(() => {
    capabilityState.current = { supported: true, blocking_volumes: [] };
  });

  it("shows the naming rules up front and disables submit", () => {
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);
    expect(screen.getByText(/must start with a letter/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /create snapshot/i }),
    ).toBeDisabled();
  });

  it("rejects names with spaces and explains why", async () => {
    const user = userEvent.setup();
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);
    await user.type(screen.getByLabelText("Name"), "my snap");
    expect(screen.getByText(/cannot contain spaces/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /create snapshot/i }),
    ).toBeDisabled();
  });

  it("flags the reserved name current", async () => {
    const user = userEvent.setup();
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);
    await user.type(screen.getByLabelText("Name"), "current");
    expect(screen.getByText(/reserved/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /create snapshot/i }),
    ).toBeDisabled();
  });

  it("enables submit for a valid name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);
    await user.type(screen.getByLabelText("Name"), "before-upgrade");
    expect(
      screen.getByRole("button", { name: /create snapshot/i }),
    ).toBeEnabled();
  });

  it("shows the RAM state checkbox only for VMs", () => {
    const { unmount } = renderWithProviders(
      <CreateSnapshotDialog {...defaultProps} />,
    );
    expect(screen.getByLabelText(/include ram state/i)).toBeInTheDocument();
    unmount();

    renderWithProviders(<CreateSnapshotDialog {...defaultProps} kind="ct" />);
    expect(screen.queryByLabelText(/include ram state/i)).toBeNull();
  });

  it("warns when the guest cannot snapshot but does not block submit", async () => {
    capabilityState.current = {
      supported: false,
      blocking_volumes: ["tpmstate0 on store01"],
    };
    const user = userEvent.setup();
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);

    expect(
      screen.getByText(/cannot take snapshots in its current configuration/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/tpmstate0 on store01/i)).toBeInTheDocument();

    // Warning, not a block: a valid name still enables Create.
    await user.type(screen.getByLabelText("Name"), "before-upgrade");
    expect(
      screen.getByRole("button", { name: /create snapshot/i }),
    ).toBeEnabled();
  });

  it("shows no warning when the guest supports snapshots", () => {
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);
    expect(screen.queryByText(/cannot take snapshots/i)).toBeNull();
  });

  it("keeps the dialog open with the error when the task fails", async () => {
    const user = userEvent.setup();
    renderWithProviders(<CreateSnapshotDialog {...defaultProps} />);

    await user.type(screen.getByLabelText("Name"), "before-upgrade");
    await user.click(screen.getByRole("button", { name: /create snapshot/i }));

    expect(
      screen.getByText(/task failed: snapshot feature is not available/i),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /try again/i })).toBeEnabled();
    // The dialog's built-in X button is also accessibly named "Close", so
    // target the footer button by its visible text node.
    expect(screen.getByText("Close", { selector: "button" })).toBeEnabled();

    // Try Again returns to the form with the previous inputs intact.
    await user.click(screen.getByRole("button", { name: /try again/i }));
    expect(screen.getByLabelText("Name")).toHaveValue("before-upgrade");
  });
});
