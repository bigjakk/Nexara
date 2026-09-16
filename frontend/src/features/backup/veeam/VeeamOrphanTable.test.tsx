import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { VeeamOrphanTable } from "./VeeamOrphanTable";
import type { VeeamOrphanedObject } from "../types/backup";

function orphan(over: Partial<VeeamOrphanedObject> = {}): VeeamOrphanedObject {
  return {
    id: "obj-1",
    veeam_object_id: "3aad74b4-8013-4b41-b266-d21b6d88cc21",
    smbios_uuid: "316e531d-55c0-4fef-adc2-f1bb9c4e1873",
    name: "linux03",
    object_type: "VM",
    platform_name: "cluster01",
    cluster_id: "c0000000-0000-4000-8000-000000000001",
    cluster_name: "cluster02",
    restore_points_count: 12,
    restore_point_bytes: 53687091200,
    latest_restore_point: "2026-08-20T22:00:00Z",
    size_bytes: 21474836480,
    last_run_failed: false,
    last_seen_at: "2026-08-27T02:00:00Z",
    ...over,
  };
}

describe("VeeamOrphanTable", () => {
  it("says nothing is orphaned rather than rendering an empty table", () => {
    renderWithProviders(<VeeamOrphanTable serverId="srv-1" objects={[]} />);
    expect(screen.getByText(/matches a guest/i)).toBeInTheDocument();
  });

  it("shows the SMBIOS uuid that failed to match, which is the whole point", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamOrphanTable serverId="srv-1" objects={[orphan()]} />,
    );

    await user.click(screen.getByText("linux03"));

    // The identity Veeam recorded, that no guest on the mapped cluster
    // carries. Without it an operator has no way to tell an orphan from a
    // correlation bug.
    expect(
      screen.getByText("316e531d-55c0-4fef-adc2-f1bb9c4e1873"),
    ).toBeInTheDocument();
  });

  it("says so when Veeam recorded no identity at all", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamOrphanTable
        serverId="srv-1"
        objects={[orphan({ smbios_uuid: "" })]}
      />,
    );

    await user.click(screen.getByText("linux03"));
    expect(screen.getByText("none recorded")).toBeInTheDocument();
  });

  it("refuses to map until a valid VMID is entered", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamOrphanTable serverId="srv-1" objects={[orphan()]} />,
    );

    await user.click(screen.getByText("linux03"));
    const button = screen.getByRole("button", { name: /map to this guest/i });
    expect(button).toBeDisabled();

    // A manual map attributes a body of restore points to a guest and is
    // exempt from automatic correction, so a typo must not reach the API.
    await user.type(screen.getByLabelText(/map to guest vmid/i), "abc");
    expect(button).toBeDisabled();

    await user.clear(screen.getByLabelText(/map to guest vmid/i));
    await user.type(screen.getByLabelText(/map to guest vmid/i), "105");
    expect(button).toBeEnabled();
  });

  it("cannot map an object whose platform is not mapped to a cluster", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamOrphanTable
        serverId="srv-1"
        objects={[orphan({ cluster_id: null, cluster_name: "" })]}
      />,
    );

    await user.click(screen.getByText("linux03"));
    await user.type(screen.getByLabelText(/map to guest vmid/i), "105");

    // There is no cluster to map it to. The API rejects it; the button must
    // not pretend otherwise.
    expect(
      screen.getByRole("button", { name: /map to this guest/i }),
    ).toBeDisabled();
  });
});
