import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { makeStorage } from "@/test/storage.fixtures";
import type { StorageResponse } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
import { CreateCTDialog } from "./CreateCTDialog";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const NODE = {
  id: "node-01",
  cluster_id: "cluster01",
  name: "pve-01",
  status: "online",
  cpu_count: 8,
  mem_total: 8589934592,
};

// A store of pve-01's own (node-01) that holds container disks.
const ROOTDIR = makeStorage({
  id: "st-01",
  cluster_id: "cluster01",
  node_id: "node-01",
  storage: "store01",
  type: "lvmthin",
  content: "rootdir,images",
});

/** Answers the dialog's reads: `nodes`, and `storage` for the stores. */
function serve(storage: StorageResponse[] = [ROOTDIR], nodes = [NODE]) {
  const answer = (path: unknown) => {
    const p = String(path);
    return Promise.resolve(
      p.endsWith("/nodes") ? nodes : p.endsWith("/storage") ? storage : [],
    );
  };
  vi.mocked(apiClient.list).mockImplementation(answer);
  vi.mocked(apiClient.get).mockImplementation(answer);
}

/** Opens the dialog with General filled in and a template typed. */
async function openWithTemplate() {
  const user = userEvent.setup();
  renderWithProviders(
    <CreateCTDialog
      open
      onOpenChange={() => undefined}
      clusterId="cluster01"
    />,
  );
  await waitFor(() => {
    expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
  });
  await user.click(screen.getByRole("button", { name: "Template" }));
  await user.type(
    screen.getByLabelText("Template path"),
    "local:vztmpl/debian-12.tar.zst",
  );
  return user;
}

describe("CreateCTDialog", () => {
  // The step list is a second way forward. It must not get past a step that
  // Next would hold: a container cannot be reviewed, or created, with no template.
  it("lets the step list go no further than Next would", async () => {
    serve();
    const user = userEvent.setup();
    renderWithProviders(
      <CreateCTDialog
        open
        onOpenChange={() => undefined}
        clusterId="cluster01"
      />,
    );

    // General fills itself in: the next free VMID and the only node.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
    });
    expect(screen.getByRole("button", { name: "Root disk" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Review" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Template" }));
    expect(screen.getByRole("button", { name: "Root disk" })).toBeDisabled();
    await user.type(
      screen.getByLabelText("Template path"),
      "local:vztmpl/debian-12.tar.zst",
    );

    // Every later step is valid as it opens, so the list now reaches the end.
    await user.click(screen.getByRole("button", { name: "Review" }));
    expect(
      screen.getByRole("button", { name: "Create container" }),
    ).toBeEnabled();
  });

  // The server refuses a create's start without execute:container (403), so
  // the tick is offered only to a holder, and says why to anyone else.
  it.each([
    [["manage:container"], false],
    [["manage:container", "execute:container"], true],
  ])(
    "with %j offers Start after creation: %s",
    async (permissions, offered) => {
      useAuthStore.setState({ permissions });
      serve();
      const user = await openWithTemplate();
      await user.click(screen.getByRole("button", { name: "DNS and options" }));

      const tick = screen.getByRole("checkbox", {
        name: "Start after creation",
      });
      if (offered) expect(tick).toBeEnabled();
      else expect(tick).toBeDisabled();
      expect(
        screen.queryByText(/needs the execute:container permission/) === null,
      ).toBe(offered);
    },
  );

  // "Default" sent neither storage nor rootfs: Proxmox fell back to `local`,
  // which normally refuses container disks. store00 is another node's own.
  it("sends a root disk left unset on the node's first store for container disks", async () => {
    serve([
      { ...ROOTDIR, id: "st-00", storage: "store00", node_id: "node-02" },
      ROOTDIR,
    ]);
    vi.mocked(apiClient.post).mockResolvedValue({ upid: "UPID:pve-01:x" });
    const user = await openWithTemplate();
    await user.click(screen.getByRole("button", { name: "Review" }));
    await user.click(screen.getByRole("button", { name: "Create container" }));

    await waitFor(() => {
      expect(apiClient.post).toHaveBeenCalled();
    });
    const body = vi.mocked(apiClient.post).mock.calls[0]?.[1] as {
      storage: string;
      rootfs: string;
    };
    expect(body.storage).toBe("store01");
    expect(body.rootfs).toMatch(/^store01:\d+$/);
  });

  // A node's own store has a row per node: keyed by row, store01 picked on
  // pve-01 became pve-02's first store after a node change.
  it("keeps the root disk store picked by name when the node changes", async () => {
    const node2 = { ...NODE, id: "node-02", name: "pve-02" };
    serve(
      [
        ROOTDIR,
        { ...ROOTDIR, id: "st-00", storage: "store00", node_id: "node-02" },
        { ...ROOTDIR, id: "st-02", node_id: "node-02" },
      ],
      [NODE, node2],
    );
    const user = await openWithTemplate();
    await user.click(screen.getByRole("button", { name: /^General/ }));
    await user.selectOptions(screen.getByLabelText("Node"), ["pve-01"]);
    await user.click(screen.getByRole("button", { name: /^Root disk/ }));
    await user.selectOptions(screen.getByLabelText("Storage"), ["store01"]);
    await user.click(screen.getByRole("button", { name: /^General/ }));
    await user.selectOptions(screen.getByLabelText("Node"), ["pve-02"]);
    await user.click(screen.getByRole("button", { name: /^Root disk/ }));

    expect(screen.getByLabelText<HTMLSelectElement>("Storage").value).toBe(
      "store01",
    );
  });

  it("holds the wizard at Root disk while no store holds container disks, and says why", async () => {
    serve([]);
    const user = await openWithTemplate();
    await user.click(screen.getByRole("button", { name: "Next" }));

    expect(
      screen.getByText(/No storage for container disks/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
  });
});
