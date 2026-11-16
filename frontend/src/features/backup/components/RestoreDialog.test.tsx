import { describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { fill, setupUser } from "@/test/user";
import { useAuthStore } from "@/stores/auth-store";
import { RestoreDialog } from "./RestoreDialog";
import type { PBSSnapshot } from "../types/backup";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const SNAPSHOT: PBSSnapshot = {
  id: "s1",
  pbs_server_id: "p1",
  datastore: "datastore01",
  backup_type: "vm",
  backup_id: "101",
  backup_time: 1700000000,
  size: 1024,
  verified: true,
  protected: false,
  comment: "",
  owner: "",
  last_seen_at: "",
  created_at: "",
  updated_at: "",
};

// Unique gives the guest new MAC addresses: off for the original guest coming
// back under its own VMID, on for a copy under a new one, and the user's own
// choice once they make one.
describe("RestoreDialog — Unique", () => {
  it("follows the VMID until the user sets it", async () => {
    vi.mocked(apiClient.list).mockResolvedValue([]);
    vi.mocked(apiClient.get).mockResolvedValue([]);
    const user = setupUser();
    renderWithProviders(<RestoreDialog snapshot={SNAPSHOT} pbsId="p1" />);
    await user.click(screen.getByRole("button", { name: "Restore" }));

    const unique = await screen.findByRole("checkbox", { name: /Unique/ });
    const vmid = screen.getByLabelText("VMID");
    expect(unique).not.toBeChecked();
    fill(vmid, "200");
    expect(unique).toBeChecked();
    await user.click(unique);
    fill(vmid, "101");
    fill(vmid, "201");
    expect(unique).not.toBeChecked();
  });
});

// The server refuses a start after restore to a caller without execute on the
// guest's kind, so the dialog does not offer the tick to one.
describe("RestoreDialog — Start after restore", () => {
  it.each([
    [["execute:vm"], true],
    [["execute:container"], false],
  ])("with %j the tick is enabled: %s", async (permissions, enabled) => {
    vi.mocked(apiClient.list).mockResolvedValue([]);
    vi.mocked(apiClient.get).mockResolvedValue([]);
    useAuthStore.setState({ permissions });
    try {
      const user = setupUser();
      renderWithProviders(<RestoreDialog snapshot={SNAPSHOT} pbsId="p1" />);
      await user.click(screen.getByRole("button", { name: "Restore" }));
      const start = await screen.findByRole("checkbox", {
        name: "Start after restore",
      });
      expect(start.hasAttribute("disabled")).toBe(!enabled);
      expect(
        screen.queryByText(/needs the execute:vm permission/) === null,
      ).toBe(enabled);
    } finally {
      useAuthStore.setState({ permissions: [] });
    }
  });
});

// The server refuses a force restore to a caller without delete on the
// archive's kind, so the dialog disables the tick and never sends force.
describe("RestoreDialog — Overwrite existing VM", () => {
  it("is disabled without delete:vm and the request carries no force", async () => {
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters"))
        return Promise.resolve([{ id: "c1", name: "cluster01" }]);
      if (p.endsWith("/nodes"))
        return Promise.resolve([
          { id: "n1", name: "pve-01", status: "online" },
        ]);
      if (p.endsWith("/storage"))
        return Promise.resolve([
          {
            storage: "store01",
            type: "pbs",
            content: "backup",
            active: true,
            enabled: true,
          },
        ]);
      return Promise.resolve([]);
    });
    vi.mocked(apiClient.post).mockResolvedValue({
      upid: "UPID:pve-01:1",
      status: "restoring",
    });
    useAuthStore.setState({
      permissions: ["manage:backup", "delete:container"],
    });
    try {
      const user = setupUser();
      renderWithProviders(<RestoreDialog snapshot={SNAPSHOT} pbsId="p1" />);
      await user.click(screen.getByRole("button", { name: "Restore" }));
      const overwrite = await screen.findByRole("checkbox", {
        name: "Overwrite existing VM",
      });
      expect(overwrite).toBeDisabled();
      expect(
        screen.getByText(/needs the delete:vm permission/),
      ).toBeInTheDocument();
      await user.click(overwrite);
      const restore = within(screen.getByRole("dialog")).getByRole("button", {
        name: "Restore",
      });
      await vi.waitFor(() => {
        expect(restore).toBeEnabled();
      });
      await user.click(restore);
      await vi.waitFor(() => {
        expect(apiClient.post).toHaveBeenCalled();
      });
      expect(vi.mocked(apiClient.post).mock.calls[0]?.[1]).toMatchObject({
        force: false,
      });
    } finally {
      useAuthStore.setState({ permissions: [] });
    }
  });
});
