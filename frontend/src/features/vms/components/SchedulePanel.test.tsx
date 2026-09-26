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

// The field holds a PREFIX: every run names its snapshot
// <prefix>-YYYYMMDD-HHMMSS ("auto" when empty), because a guest holds each
// snapshot name once and a name reused verbatim failed on every run after the
// first. What Proxmox judges is that whole name, so a reserved word is a legal
// prefix and the budget is 24 characters, not 40.
describe("SchedulePanel snapshot name", () => {
  beforeEach(() => {
    createCalls.current = [];
  });

  it("treats an empty name as the auto prefix", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    await openDialog(user);

    expect(
      screen.getByText(/new snapshot named auto-YYYYMMDD-HHMMSS/),
    ).toBeInTheDocument();
    expect(createButton()).toBeEnabled();

    // Empty must not merely be allowed through — it must reach the server as
    // an absent snap_name, which is what makes the scheduler use the auto
    // prefix.
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

  it("shows the name each run takes from the typed prefix", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "nightly");
    expect(
      screen.getByText(/new snapshot named nightly-YYYYMMDD-HHMMSS/),
    ).toBeInTheDocument();
    // The old copy said the name was used as-is on every run. It is not.
    expect(screen.queryByText(/as-is/i)).toBeNull();
  });

  it("says the date and time a run adds are in UTC", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    await openDialog(user);

    // The scheduler names each run in UTC (proxmox.TimestampedSnapshotName),
    // whatever the server's zone or the browser's, and this line is the only
    // place the dialog says so: the cron hint above names no zone.
    expect(
      screen.getByText(
        /auto-YYYYMMDD-HHMMSS, from the run's date and time in UTC;/,
      ),
    ).toBeInTheDocument();
  });

  it("flags a prefix Proxmox would refuse and blocks submit", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "my snap");
    expect(screen.getByText(/cannot contain spaces/i)).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
  });

  // Proxmox reserves these as WHOLE names, and the name a run sends carries a
  // date, so none of them is refused as a prefix — on either kind, whatever
  // the kind reserves as a name.
  it.each([
    ["vm", "current"],
    ["ct", "current"],
    ["vm", "pending"],
    ["vm", "PENDING"],
    ["ct", "vzdump"],
  ] as const)(
    "accepts the reserved word %s/%s as a prefix",
    async (kind, word) => {
      const user = userEvent.setup();
      renderWithProviders(<SchedulePanel {...defaultProps} kind={kind} />);
      const input = await openDialog(user);

      await user.type(input, word);
      expect(screen.queryByText(/reserved/i)).toBeNull();
      expect(createButton()).toBeEnabled();
      await user.click(createButton());
      expect(createCalls.current[0]?.body.params).toStrictEqual({
        snap_name: word,
      });
    },
  );

  it("accepts a one-letter prefix — the two-character minimum is on the whole name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "a");
    expect(createButton()).toBeEnabled();
  });

  it("accepts a prefix of exactly 24 characters", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "a".repeat(24));
    expect(input).toHaveValue("a".repeat(24));
    expect(createButton()).toBeEnabled();
  });

  it("rejects a prefix over 24 characters", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    // Set the value directly: maxLength stops this at the keyboard, so typing
    // could never reach the validator's own length rule. The rule is still
    // what must answer — maxLength is a convenience, not the authority.
    fireEvent.change(input, { target: { value: "a".repeat(25) } });
    expect(screen.getByText(/limited to 24 characters/i)).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
  });

  it("caps the field at 24 characters", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    expect(input).toHaveAttribute("maxLength", "24");
  });

  it("stops blocking submit when the action no longer takes a name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SchedulePanel {...defaultProps} />);
    const input = await openDialog(user);

    await user.type(input, "my snap");
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

    // The scheduler adds the date itself (internal/scheduler,
    // scheduledSnapshotName); nothing expands a YYYYMMDD the user TYPES — it
    // would stay in the prefix literally. So the pattern may appear in the
    // description of what a run does, never in the placeholder.
    expect(screen.queryByText(/template/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/YYYYMMDD/)).toBeNull();
  });
});
