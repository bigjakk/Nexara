import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { SchedulePanel } from "./SchedulePanel";

/** The slice of a create-schedule call these tests assert on. */
interface CapturedCreate {
  body: { action: string; params: Record<string, unknown> };
}

const createCalls = vi.hoisted(() => ({ current: [] as CapturedCreate[] }));

vi.mock("../api/vm-queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/vm-queries")>();
  return {
    ...actual,
    useScheduledTasks: () => ({ data: [], isLoading: false }),
    useCreateSchedule: () => ({
      mutate: (vars: CapturedCreate) => {
        createCalls.current.push(vars);
      },
      isPending: false,
    }),
    useDeleteSchedule: () => ({ mutate: () => {} }),
  };
});

const defaultProps = {
  clusterId: "c1",
  kind: "vm" as const,
  vmid: 101,
  node: "pve-01",
};

/** Opens the create dialog and returns the snapshot-name field. */
async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /add schedule/i }));
  return screen.getByLabelText(/snapshot name/i);
}

function createButton() {
  return screen.getByRole("button", { name: "Create" });
}

describe("SchedulePanel snapshot name", () => {
  beforeEach(() => {
    createCalls.current = [];
  });

  it("treats an empty name as the auto-generate path", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    await openDialog(user);

    expect(
      screen.getByText(/auto-generate a timestamped name/i),
    ).toBeInTheDocument();
    expect(createButton()).toBeEnabled();

    // Empty must not merely be allowed through — it must reach the server as
    // an absent snap_name, which is what makes the scheduler mint one.
    // toStrictEqual, not toMatchObject and not toEqual: toMatchObject reads a
    // nested {} as "any object" and would pass against a params carrying a
    // snap_name, and toEqual still tolerates {snap_name: undefined} — benign
    // on the wire, since JSON.stringify drops it, but it is not the absence
    // this case is named for.
    await user.click(createButton());
    expect(createCalls.current).toHaveLength(1);
    expect(createCalls.current[0]?.body.action).toBe("snapshot");
    expect(createCalls.current[0]?.body.params).toStrictEqual({});
  });

  it("sends a valid name through as snap_name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    // The counterpart to the empty case above: without this, a panel that
    // never sends snap_name at all satisfies the whole file.
    await user.type(input, "nightly-snap");
    await user.click(createButton());
    expect(createCalls.current).toHaveLength(1);
    expect(createCalls.current[0]?.body.params).toStrictEqual({
      snap_name: "nightly-snap",
    });
  });

  it("accepts an ordinary name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "nightly-snap");
    expect(screen.queryByText(/reserved/i)).toBeNull();
    expect(createButton()).toBeEnabled();
  });

  it("flags the reserved name current and blocks submit", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "current");
    expect(screen.getByText(/reserved/i)).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
  });

  // The reserved set is asymmetric by guest kind, so these two prove the
  // `kind` prop actually reaches the validator — a hardcoded kind at the call
  // site keeps every other case green.
  it("accepts vzdump on a VM — it is reserved for containers only", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "vzdump");
    expect(screen.queryByText(/reserved/i)).toBeNull();
    expect(createButton()).toBeEnabled();
  });

  it("accepts pending on a container — it is reserved for VMs only", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} kind="ct" />);
    const input = await openDialog(user);

    await user.type(input, "pending");
    expect(screen.queryByText(/reserved/i)).toBeNull();
    expect(createButton()).toBeEnabled();
  });

  it("accepts Current — only lowercase current is reserved", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "Current");
    expect(screen.queryByText(/reserved/i)).toBeNull();
    expect(createButton()).toBeEnabled();
  });

  it("rejects a name over 40 characters", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    // Set the value directly: maxLength stops this at the keyboard, so typing
    // could never reach the validator's own length rule. The rule is still
    // what must answer — maxLength is a convenience, not the authority.
    fireEvent.change(input, { target: { value: "a".repeat(41) } });
    expect(screen.getByText(/limited to 40 characters/i)).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
  });

  it("caps the field at 40 characters", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    expect(input).toHaveAttribute("maxLength", "40");
  });

  it("stops blocking submit when the action no longer takes a name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "current");
    expect(createButton()).toBeDisabled();

    // The field goes away with the action, so the block it caused has to go
    // with it — otherwise Create is dead with nothing on screen saying why.
    await user.selectOptions(screen.getByRole("combobox"), "reboot");
    expect(screen.queryByLabelText(/snapshot name/i)).toBeNull();
    expect(createButton()).toBeEnabled();
  });

  it("does not promise template substitution", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    await openDialog(user);

    // The scheduler uses the stored value verbatim (internal/scheduler,
    // executeSnapshot); nothing anywhere expands a YYYYMMDD placeholder.
    expect(screen.queryByText(/template/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/YYYYMMDD/)).toBeNull();
  });
});
