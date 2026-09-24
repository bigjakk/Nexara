import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { HAResourceForm } from "./HAResourceForm";
import type { HAResource } from "@/features/ha/api/ha-queries";
import type { VMResponse } from "@/types/api";

// The real hooks run against a mocked transport, so what these tests read is
// the body the edit form actually hands to PUT .../ha/resources/:sid.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      put: vi.fn(),
      post: vi.fn(),
      list: vi.fn(),
      get: vi.fn(),
      delete: vi.fn(),
    },
  };
});

const mockedPut = vi.mocked(apiClient.put);
const mockedPost = vi.mocked(apiClient.post);
const mockedList = vi.mocked(apiClient.list);

const CLUSTER = "cccccccc-0000-0000-0000-000000000001";
const PUT_PATH = `/api/v1/clusters/${CLUSTER}/ha/resources/vm%3A101`;

// A resource has a failback of its own only from PVE 9 (haResourceHasFailback),
// so the version decides what the form may offer and send.
const PVE9 = "9.0.6";
const PVE8 = "8.4.1";

/** A resource as the API returns it when every setting is Proxmox's default. */
const ON_DEFAULTS: HAResource = {
  sid: "vm:101",
  type: "vm",
  state: "started",
  group: "",
  status: "",
};

/** The one PUT the form made: its path and its body, or a loud failure. */
function onlyPut(): [string, unknown] {
  expect(mockedPut).toHaveBeenCalledTimes(1);
  const call = mockedPut.mock.calls[0];
  if (call === undefined) throw new Error("expected a PUT to have been made");
  return [call[0], call[1]];
}

/**
 * Puts text into a number input the way a browser holds it. user.type is no
 * use for exponent form: it leaves "1e1" as "10" (its change events run "",
 * "1", "", "10"), so a test typing it would never hand the form the spelling
 * that parseInt misread. The precondition makes that impossible to miss.
 */
function setNumberInput(input: HTMLElement, text: string) {
  fireEvent.change(input, { target: { value: text } });
  expect((input as HTMLInputElement).value).toBe(text);
}

function renderEdit(
  resource: HAResource,
  onSuccess = vi.fn(),
  pveVersion = PVE9,
) {
  const user = userEvent.setup();
  const view = renderWithProviders(
    <HAResourceForm
      mode="edit"
      clusterId={CLUSTER}
      pveVersion={pveVersion}
      resource={resource}
      onSuccess={onSuccess}
    />,
  );
  return { user, onSuccess, ...view };
}

describe("HAResourceForm — edit sends only what changed", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedPut.mockResolvedValue(undefined);
    mockedList.mockImplementation((path: string) =>
      Promise.resolve(
        path.endsWith("/ha/groups")
          ? [{ group: "ha-group01", nodes: "pve-01", restricted: 0 }]
          : [],
      ),
    );
  });

  it("sends only the comment for an edit of only the comment", async () => {
    const { user, onSuccess } = renderEdit(ON_DEFAULTS);

    await user.type(screen.getByLabelText("Comment"), "maintenance window");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalled();
    });
    expect(onlyPut()).toEqual([PUT_PATH, { comment: "maintenance window" }]);
  });

  it("sends only the state for a failback-0 resource edited on state alone", async () => {
    // failback 0 is what Proxmox stores when failback is OFF (its default is
    // on), and the precondition below is that the form shows it off — so a
    // save that re-sent the switch would be re-sending a real policy.
    const { user } = renderEdit({ ...ON_DEFAULTS, failback: 0 });
    expect(screen.getByRole("switch", { name: "Failback" })).not.toBeChecked();

    const [stateTrigger] = screen.getAllByRole("combobox");
    if (!stateTrigger) throw new Error("state select not rendered");
    await user.click(stateTrigger);
    await user.click(screen.getByRole("option", { name: "stopped" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { state: "stopped" }]);
  });

  it("shows an explicit max_restart of 0 as 0 and leaves it out of an unrelated edit", async () => {
    const { user } = renderEdit({ ...ON_DEFAULTS, max_restart: 0 });
    expect(screen.getByLabelText("Max Restart")).toHaveValue(0);

    await user.type(screen.getByLabelText("Comment"), "note");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { comment: "note" }]);
  });

  it("sends only max_relocate when that is all that changed", async () => {
    const { user } = renderEdit(ON_DEFAULTS);
    const relocate = screen.getByLabelText("Max Relocate");
    // Unset reads as Proxmox's default of 1, not as 0 or as nothing.
    expect(relocate).toHaveValue(1);

    await user.clear(relocate);
    await user.type(relocate, "3");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { max_relocate: 3 }]);
  });

  it("sends an emptied comment as the empty string, which clears it", async () => {
    const { user } = renderEdit({ ...ON_DEFAULTS, comment: "old note" });

    await user.clear(screen.getByLabelText("Comment"));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { comment: "" }]);
  });

  it("sends the empty group when the operator picks no group", async () => {
    const { user } = renderEdit({ ...ON_DEFAULTS, group: "ha-group01" });

    const groupTrigger = screen.getAllByRole("combobox")[1];
    if (!groupTrigger) throw new Error("group select not rendered");
    await user.click(groupTrigger);
    await user.click(screen.getByRole("option", { name: "— None —" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { group: "" }]);
  });

  it("writes nothing for a save with nothing changed, including a change undone", async () => {
    const { user, onSuccess } = renderEdit(ON_DEFAULTS);
    const restart = screen.getByLabelText("Max Restart");
    await user.clear(restart);
    await user.type(restart, "5");
    await user.clear(restart);
    await user.type(restart, "1");

    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("keeps an unsaved change through a parent re-render with the same resource", async () => {
    const onSuccess = vi.fn();
    const { user, rerender } = renderEdit(ON_DEFAULTS, onSuccess);
    await user.type(screen.getByLabelText("Comment"), "kept");

    // What ClusterHATab does whenever a polled query returns new data: the
    // same resource, in a new props object.
    rerender(
      <HAResourceForm
        mode="edit"
        clusterId={CLUSTER}
        pveVersion={PVE9}
        resource={ON_DEFAULTS}
        onSuccess={onSuccess}
      />,
    );
    expect(screen.getByLabelText("Comment")).toHaveValue("kept");

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onlyPut()).toEqual([PUT_PATH, { comment: "kept" }]);
  });

  it("opens a resource on the defaults at Proxmox's defaults, and says so", () => {
    const { unmount } = renderEdit(ON_DEFAULTS);
    expect(screen.getByLabelText("Max Restart")).toHaveValue(1);
    expect(screen.getByLabelText("Max Relocate")).toHaveValue(1);
    expect(screen.getByRole("switch", { name: "Failback" })).toBeChecked();
    expect(
      screen.getAllByText(/Not set on this resource, so Proxmox's default/),
    ).toHaveLength(3);
    unmount();

    // The positive control: a resource that sets all three gets no hint.
    renderEdit({
      ...ON_DEFAULTS,
      max_restart: 1,
      max_relocate: 1,
      failback: 1,
    });
    expect(
      screen.queryByText(/Not set on this resource, so Proxmox's default/),
    ).not.toBeInTheDocument();
  });
});

const CREATE_PATH = `/api/v1/clusters/${CLUSTER}/ha/resources`;

const UNMANAGED_VM = {
  id: "vm-row-105",
  cluster_id: CLUSTER,
  vmid: 105,
  name: "linux05",
  type: "qemu",
  template: false,
} as unknown as VMResponse;

/** Opens the create form, picks the one guest, and presses Create. */
async function createOn(pveVersion: string) {
  const user = userEvent.setup();
  renderWithProviders(
    <HAResourceForm
      mode="create"
      clusterId={CLUSTER}
      pveVersion={pveVersion}
      availableVMs={[UNMANAGED_VM]}
      onSuccess={() => undefined}
    />,
  );
  const [guestTrigger] = screen.getAllByRole("combobox");
  if (!guestTrigger) throw new Error("guest select not rendered");
  await user.click(guestTrigger);
  await user.click(screen.getByRole("option", { name: "vm:105 — linux05" }));
  await user.click(screen.getByRole("button", { name: "Create" }));
  expect(mockedPost).toHaveBeenCalledTimes(1);
  return mockedPost.mock.calls[0];
}

describe("HAResourceForm — per-version fields, unset state, re-seeding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedPut.mockResolvedValue(undefined);
    mockedPost.mockResolvedValue(undefined);
    mockedList.mockResolvedValue([]);
  });

  it("offers no failback on PVE 8, and an edit there never sends it", async () => {
    const { user } = renderEdit(ON_DEFAULTS, vi.fn(), PVE8);
    expect(
      screen.queryByRole("switch", { name: "Failback" }),
    ).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Comment"), "note");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { comment: "note" }]);
  });

  it("creates without failback on PVE 8, where Proxmox refuses the key", async () => {
    expect(await createOn(PVE8)).toEqual([
      CREATE_PATH,
      { sid: "vm:105", state: "started", max_restart: 1, max_relocate: 1 },
    ]);
  });

  it("creates with failback on PVE 9", async () => {
    expect(await createOn(PVE9)).toEqual([
      CREATE_PATH,
      {
        sid: "vm:105",
        state: "started",
        max_restart: 1,
        max_relocate: 1,
        failback: 1,
      },
    ]);
  });

  it("opens a resource with no stored state on started, and leaves state unsent", async () => {
    // The API passes an unset state through as "", which Proxmox runs as
    // "started" (HA_RESOURCE_DEFAULTS).
    const { user } = renderEdit({ ...ON_DEFAULTS, state: "" });
    const [stateTrigger] = screen.getAllByRole("combobox");
    expect(stateTrigger).toHaveTextContent("started");

    await user.type(screen.getByLabelText("Comment"), "note");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { comment: "note" }]);
  });

  it("re-seeds from a different resource handed to the same form", async () => {
    const onSuccess = vi.fn();
    const { user, rerender } = renderEdit(ON_DEFAULTS, onSuccess);
    expect(screen.getByLabelText("Max Restart")).toHaveValue(1);

    const other: HAResource = {
      sid: "vm:102",
      type: "vm",
      state: "stopped",
      group: "",
      status: "",
      max_restart: 4,
      max_relocate: 2,
      comment: "second",
    };
    rerender(
      <HAResourceForm
        mode="edit"
        clusterId={CLUSTER}
        pveVersion={PVE9}
        resource={other}
        onSuccess={onSuccess}
      />,
    );
    expect(screen.getByLabelText("Max Restart")).toHaveValue(4);
    expect(screen.getByLabelText("Max Relocate")).toHaveValue(2);
    expect(screen.getByLabelText("Comment")).toHaveValue("second");

    // Measured against the resource now shown, not the first one: only the
    // comment changed.
    await user.type(screen.getByLabelText("Comment"), " pair");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onlyPut()).toEqual([
      `/api/v1/clusters/${CLUSTER}/ha/resources/vm%3A102`,
      { comment: "second pair" },
    ]);
  });

  it("does not let a stored count above the input's usual 10 block an unrelated edit", async () => {
    // Proxmox has no maximum; ha-manager or pvesh can store more than the
    // 10 this editor offers.
    const { user } = renderEdit({
      ...ON_DEFAULTS,
      max_restart: 15,
      max_relocate: 12,
    });
    expect(screen.getByLabelText("Max Restart")).toBeValid();
    expect(screen.getByLabelText("Max Relocate")).toBeValid();

    await user.type(screen.getByLabelText("Comment"), "note");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { comment: "note" }]);
  });

  it("sends an edited count that is above 10 but within the stored one's ceiling", async () => {
    // The input's max is the stored 15, so 12 is valid and must be sent —
    // measured against a fixed 10 it would be dropped, and the dialog would
    // close as if it had saved.
    const { user, onSuccess } = renderEdit({ ...ON_DEFAULTS, max_restart: 15 });
    const restart = screen.getByLabelText("Max Restart");
    await user.clear(restart);
    await user.type(restart, "12");
    expect(restart).toHaveValue(12);
    expect(restart).toBeValid();
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalled();
    });
    expect(onlyPut()).toEqual([PUT_PATH, { max_restart: 12 }]);
  });

  it("sends a count typed in exponent form as the number it is", async () => {
    // A number input takes "1e1" as a valid 10 — within max, a whole number —
    // and hands the form the text "1e1", which parseInt read as 1.
    const { user } = renderEdit(ON_DEFAULTS);
    const relocate = screen.getByLabelText("Max Relocate");
    setNumberInput(relocate, "1e1");
    expect(relocate).toBeValid();
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { max_relocate: 10 }]);
  });

  it("creates with a count typed in exponent form as the number it is", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <HAResourceForm
        mode="create"
        clusterId={CLUSTER}
        pveVersion={PVE8}
        availableVMs={[UNMANAGED_VM]}
        onSuccess={() => undefined}
      />,
    );
    const [guestTrigger] = screen.getAllByRole("combobox");
    if (!guestTrigger) throw new Error("guest select not rendered");
    await user.click(guestTrigger);
    await user.click(screen.getByRole("option", { name: "vm:105 — linux05" }));
    setNumberInput(screen.getByLabelText("Max Restart"), "1e1");
    await user.click(screen.getByRole("button", { name: "Create" }));

    expect(mockedPost).toHaveBeenCalledTimes(1);
    expect(mockedPost.mock.calls[0]).toEqual([
      CREATE_PATH,
      { sid: "vm:105", state: "started", max_restart: 10, max_relocate: 1 },
    ]);
  });

  it("leaves a cleared count out rather than sending it as 0", async () => {
    // Number("") is 0, and 0 would switch restarts off.
    const { user } = renderEdit({ ...ON_DEFAULTS, max_restart: 3 });
    await user.clear(screen.getByLabelText("Max Restart"));
    await user.type(screen.getByLabelText("Comment"), "note");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onlyPut()).toEqual([PUT_PATH, { comment: "note" }]);
  });
});
