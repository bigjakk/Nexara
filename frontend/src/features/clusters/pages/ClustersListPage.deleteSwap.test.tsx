import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { configure, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { apiClient } from "@/lib/api-client";
import { deferred } from "@/test/fake-server";
import {
  DENIED,
  PATIENCE_MS,
  denied,
  flushInAct,
  renderOnAppClient,
  signIn,
  signOutForGood,
  tabTo,
  waitForSuccess,
  waitUntil,
} from "@/test/save-outcome-kit";
import type { ClusterResponse } from "@/types/api";
import { ClustersListPage } from "./ClustersListPage";

/**
 * The Delete Cluster dialog keeps what the operator typed and ticked in state of
 * its own: the cluster's name, and "also delete the Proxmox user and API token
 * Nexara created for this cluster", which is off until they turn it on and which
 * makes the delete remove that user and token from the cluster's Proxmox.
 *
 * It is not held while its request is out, and the button that sent it is
 * disabled by the request, so focus is lost and Tab walks out of the modal to the
 * Delete buttons behind it, which a pointer cannot reach. Enter on another
 * cluster's puts its dialog where the first was, with nothing closed between the
 * two. The page keys the dialog on the cluster, so the second is a dialog of its
 * own, with the defaults of a new one. Unkeyed, it would be the first dialog with
 * another cluster's name in its text, and the tick still on: and confirming it
 * would delete the second cluster's Proxmox user and token on the strength of a
 * choice made for the first.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

// The revoke option is gated on GLOBAL manage:cluster, matching the server; the
// dialog is the only component of the page that asks.
vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: () => ({
    hasPermission: (action: string, resource: string) =>
      action === "manage" && resource === "cluster",
  }),
}));

// The whole page renders cold in the first test of this file, which is given the
// kit's patience for it (test/save-outcome-kit.tsx says why).
configure({ asyncUtilTimeout: PATIENCE_MS });

const mockedList = vi.mocked(apiClient.list);
const mockedDelete = vi.mocked(apiClient.delete);
const mockedToastError = vi.mocked(toast.error);

/** A cluster whose Proxmox credential Nexara minted, so its dialog offers the tick. */
function cluster(id: string, name: string, host: string): ClusterResponse {
  return {
    id,
    name,
    api_url: `https://${host}.example.com:8006`,
    token_id: "nexara@pve!token",
    tls_fingerprint: "",
    sync_interval_seconds: 60,
    is_active: true,
    status: "online",
    pve_version: "",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    credential_source: "bootstrap",
  };
}

const CLUSTERS = [
  cluster("cluster-id-1", "cluster01", "pve-01"),
  cluster("cluster-id-2", "cluster02", "pve-02"),
];
// What each delete sends: the typed name as confirm, and the tick as 1.
const FIRST_DELETE =
  "/api/v1/clusters/cluster-id-1?confirm=cluster01&revoke_pve_credentials=1";
const SECOND_DELETE = "/api/v1/clusters/cluster-id-2?confirm=cluster02";
const REVOKE = /also delete the proxmox user and api token/i;

type UserEvent = ReturnType<typeof userEvent.setup>;

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(path === "/api/v1/clusters" ? CLUSTERS : []),
  );
});

afterEach(() => {
  signOutForGood();
});

/** Waits for a toast, then for anything else a second one could be late with. */
async function expectOneToast(message: string): Promise<void> {
  await waitUntil(() => {
    expect(mockedToastError).toHaveBeenCalledWith(message);
  });
  await flushInAct();
  expect(mockedToastError).toHaveBeenCalledTimes(1);
}

/**
 * Opens the first cluster's Delete dialog, ticks the revoke option, types the
 * cluster's name and presses Delete, and waits for the request to be out.
 */
async function sendDeleteOfFirst(user: UserEvent): Promise<HTMLElement> {
  await user.click(
    await screen.findByRole("button", { name: "Delete cluster01" }),
  );
  const first = await screen.findByRole("dialog", { name: "Delete Cluster" });
  await user.click(within(first).getByLabelText(REVOKE));
  await user.type(within(first).getByPlaceholderText("cluster01"), "cluster01");
  // What there is to carry over: the tick on, and the name typed.
  expect(within(first).getByLabelText(REVOKE)).toHaveAttribute(
    "data-state",
    "checked",
  );
  expect(within(first).getByPlaceholderText("cluster01")).toHaveValue(
    "cluster01",
  );
  await user.click(
    within(first).getByRole("button", { name: "Delete Cluster" }),
  );
  expect(
    await within(first).findByRole("button", { name: "Deleting..." }),
  ).toBeDisabled();
  return first;
}

/** Sends the first delete, held, and opens the second cluster's dialog over it. */
async function replaceWithSecond(user: UserEvent): Promise<HTMLElement> {
  await sendDeleteOfFirst(user);
  await tabTo(
    user,
    screen.getByRole("button", { name: "Delete cluster02", hidden: true }),
  );
  await user.keyboard("{Enter}");
  const second = await screen.findByRole("dialog", { name: "Delete Cluster" });
  await within(second).findByPlaceholderText("cluster02");
  return second;
}

/** What a dialog that was just opened for a cluster holds: nothing ticked, nothing typed, nothing out. */
function expectNewDialogDefaults(dialog: HTMLElement) {
  expect(within(dialog).getByLabelText(REVOKE)).toHaveAttribute(
    "data-state",
    "unchecked",
  );
  expect(within(dialog).getByPlaceholderText("cluster02")).toHaveValue("");
  const button = within(dialog).getByRole("button", { name: "Delete Cluster" });
  expect(button).toBeDisabled();
}

describe("a cluster delete whose dialog is replaced by another cluster's from the keyboard", () => {
  it("opens the second cluster's dialog with the defaults of a new one, whatever was ticked and typed for the first, and confirming it revokes nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete
      .mockReturnValueOnce(held.promise)
      .mockResolvedValueOnce(undefined);
    renderOnAppClient(<ClustersListPage />, { router: true });

    const second = await replaceWithSecond(user);

    expectNewDialogDefaults(second);
    // The first delete is still out, and is the only request there has been.
    expect(mockedDelete.mock.calls).toEqual([[FIRST_DELETE]]);

    // What the tick would have done. The operator confirms the second cluster's
    // delete as the dialog stands, the first one's request still out, and no
    // Proxmox user or token of the second cluster's is asked to be removed.
    await user.type(
      within(second).getByPlaceholderText("cluster02"),
      "cluster02",
    );
    await user.click(
      within(second).getByRole("button", { name: "Delete Cluster" }),
    );
    await waitUntil(() => {
      expect(mockedDelete).toHaveBeenCalledTimes(2);
    });
    expect(mockedDelete.mock.calls).toEqual([[FIRST_DELETE], [SECOND_DELETE]]);
  });

  it("reports the first delete's failure once, in a toast, and puts nothing of it in the second cluster's dialog, whose own delete then revokes nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete
      .mockReturnValueOnce(held.promise)
      .mockResolvedValueOnce(undefined);
    renderOnAppClient(<ClustersListPage />, { router: true });

    const second = await replaceWithSecond(user);
    held.reject(denied());

    await expectOneToast(DENIED);
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
    expectNewDialogDefaults(second);

    // The first delete has failed and the second cluster's dialog is what is
    // left. Confirmed as it stands, it asks for no Proxmox user or token of the
    // second cluster's to be removed. (Unkeyed, it shows the tick the operator
    // gave the first cluster, still on, and sends it.)
    await user.type(
      within(second).getByPlaceholderText("cluster02"),
      "cluster02",
    );
    await user.click(
      within(second).getByRole("button", { name: "Delete Cluster" }),
    );
    await waitUntil(() => {
      expect(mockedDelete).toHaveBeenCalledTimes(2);
    });
    expect(mockedDelete.mock.calls).toEqual([[FIRST_DELETE], [SECOND_DELETE]]);
  });

  it("does not close the second cluster's dialog when the first delete succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<ClustersListPage />, { router: true });

    const second = await replaceWithSecond(user);
    held.resolve(undefined);
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expectNewDialogDefaults(second);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  // The same page and the same dialog with no swap: a delete that is the
  // dialog's own is reported there, and its tick is what it sends.
  it("control: sends the tick it was given, and closes the dialog, when its own delete succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<ClustersListPage />, { router: true });

    const first = await sendDeleteOfFirst(user);
    held.resolve(undefined);
    await waitForSuccess(qc);

    await waitUntil(() => {
      expect(first).not.toBeInTheDocument();
    });
    expect(mockedDelete.mock.calls).toEqual([[FIRST_DELETE]]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows the failure of its own delete in the dialog", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderOnAppClient(<ClustersListPage />, { router: true });

    const first = await sendDeleteOfFirst(user);
    held.reject(denied());

    expect(await within(first).findByText(DENIED)).toBeInTheDocument();
    expect(first).toHaveAttribute("data-state", "open");
  });
});
