import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { configure, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { ApiClientError, apiClient } from "@/lib/api-client";
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
 * The Edit Cluster dialog is not held while its request is out, and the button
 * that sent it is disabled by the request, so focus is lost and Tab walks out of
 * the modal to the Edit buttons behind it, which a pointer cannot reach. Enter on
 * another cluster's puts its dialog where the first was, with nothing closed
 * between the two (EditClusterDialog.tsx; hooks/useSaveOutcome.ts).
 *
 * The dialog seeds its form from its props once and reads the error of its save
 * from state it keeps, so the page keys it on the cluster: the second is then a
 * dialog of its own, with its own form, and nothing of the first's save in it.
 * Unkeyed, it would be the first dialog with another cluster's name in its
 * heading.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

// The whole page renders cold in the first test of this file, which is given the
// kit's patience for it (test/save-outcome-kit.tsx says why).
configure({ asyncUtilTimeout: PATIENCE_MS });

const mockedList = vi.mocked(apiClient.list);
const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

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
    credential_source: "manual",
  };
}

const CLUSTERS = [
  cluster("cluster-id-1", "cluster01", "pve-01"),
  cluster("cluster-id-2", "cluster02", "pve-02"),
];

type UserEvent = ReturnType<typeof userEvent.setup>;

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(path === "/api/v1/clusters" ? CLUSTERS : []),
  );
  // The SSH credentials lookup needs a permission this caller lacks.
  mockedGet.mockRejectedValue(
    new ApiClientError(403, { error: "forbidden", message: "denied" }),
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

/** Opens the first cluster's dialog, renames it and presses Save, and waits for the request to be out. */
async function sendRename(user: UserEvent): Promise<HTMLElement> {
  await user.click(
    await screen.findByRole("button", { name: "Edit cluster01" }),
  );
  const first = await screen.findByRole("dialog", { name: "Edit Cluster" });
  await user.type(within(first).getByLabelText("Name"), "x");
  await user.click(within(first).getByRole("button", { name: "Save" }));
  await within(first).findByRole("button", { name: "Saving..." });
  return first;
}

/** Sends the first cluster's rename with the request held, and opens the second's dialog over it. */
async function replaceWithSecond(user: UserEvent) {
  await sendRename(user);
  await tabTo(
    user,
    screen.getByRole("button", { name: "Edit cluster02", hidden: true }),
  );
  await user.keyboard("{Enter}");
  const second = await screen.findByRole("dialog", { name: "Edit Cluster" });
  await within(second).findByText(/Update the configuration for cluster02/);
  return second;
}

describe("an edit of a cluster whose dialog is replaced by another cluster's from the keyboard", () => {
  it("toasts the failure, once, naming the first cluster, and shows nothing of it in the second's dialog", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<ClustersListPage />, { router: true });

    const second = await replaceWithSecond(user);
    held.reject(denied());

    await expectOneToast(`Saving cluster cluster01 failed: ${DENIED}`);
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
    // The second cluster's own form, not the first's with a name typed into it,
    // and a Save that is not held for a request that was not its own.
    expect(within(second).getByLabelText("Name")).toHaveValue("cluster02");
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("does not close the second cluster's dialog when the first succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<ClustersListPage />, { router: true });

    const second = await replaceWithSecond(user);
    held.resolve({ cluster: CLUSTERS[0] });
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByLabelText("Name")).toHaveValue("cluster02");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  // The same page and the same dialog with no swap: a save that is the dialog's
  // own is reported there.
  it("control: shows the failure of its own save in the dialog, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<ClustersListPage />, { router: true });

    const dialog = await sendRename(user);
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: closes the dialog when its own save succeeds, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<ClustersListPage />, { router: true });

    const dialog = await sendRename(user);
    held.resolve({ cluster: CLUSTERS[0] });
    await waitForSuccess(qc);

    await waitUntil(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});
