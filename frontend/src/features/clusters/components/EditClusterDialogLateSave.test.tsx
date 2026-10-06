import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ApiClientError, apiClient } from "@/lib/api-client";
import {
  type UserEvent,
  CANCEL_BUTTON,
  ESCAPE,
  SIGN_OUT,
  dismiss,
  expectNoToast,
  expectOneToast,
  expectSilence,
  failedToast,
  heldOnce,
} from "@/test/late-save-kit";
import {
  DENIED,
  denied,
  flushInAct,
  renderOnAppClient,
  signIn,
  signOutForGood,
  tabTo,
  waitForSuccess,
} from "@/test/save-outcome-kit";
import type { ClusterResponse } from "@/types/api";
import { EditClusterDialog } from "./EditClusterDialog";

/**
 * What EditClusterDialog passes useSaveOutcome: `open ? cluster.id : null` as
 * what it is told apart by, and the cluster's name as the action. The outcome
 * matrix is hooks/useSaveOutcome.test.tsx's.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedPut = vi.mocked(apiClient.put);
const mockedGet = vi.mocked(apiClient.get);

const CLUSTER = {
  id: "cluster-1",
  name: "cluster02",
  api_url: "https://pve.example.com:8006",
  token_id: "nexara@pve!token",
  tls_fingerprint: "AA:BB:CC:DD",
} as unknown as ClusterResponse;
const CLUSTER_URL = "/api/v1/clusters/cluster-1";
const SAVING = "Saving cluster cluster02";
// The cluster a parent gives the dialog in place of CLUSTER.
const OTHER = {
  id: "cluster-0",
  name: "cluster01",
  api_url: "https://pve-01.example.com:8006",
  token_id: "nexara@pve!token",
  tls_fingerprint: "AA:BB:CC:EE",
} as unknown as ClusterResponse;

/**
 * The dialog the way every caller mounts it, only while it is open (`keep`
 * false), or kept mounted and driven by its open prop, which its own reset()
 * calls are written for: closing and reopening it is then the same component
 * showing a different dialog. `closed` hears every answer to onOpenChange.
 */
function Host({
  closed,
  keep = false,
}: {
  closed: (open: boolean) => void;
  keep?: boolean;
}) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button
        type="button"
        onClick={() => {
          setOpen(true);
        }}
      >
        Edit again
      </button>
      {(keep || open) && (
        <EditClusterDialog
          cluster={CLUSTER}
          open={open}
          onOpenChange={(next) => {
            closed(next);
            setOpen(next);
          }}
        />
      )}
    </>
  );
}

/**
 * The one dialog mounted, with a button beside it that gives it another cluster
 * without remounting it: what a parent that does not key the dialog on its
 * cluster does when another cluster's Edit is pressed.
 */
function GivenAnotherCluster({ closed }: { closed: (open: boolean) => void }) {
  const [cluster, setCluster] = useState(CLUSTER);
  return (
    <>
      <button
        type="button"
        onClick={() => {
          setCluster(OTHER);
        }}
      >
        Show the other cluster
      </button>
      <EditClusterDialog
        cluster={cluster}
        open
        onOpenChange={(next) => {
          closed(next);
        }}
      />
    </>
  );
}

/** Renames the cluster in the open dialog and presses Save, and waits for the request to be out. */
async function saveRename(user: UserEvent): Promise<HTMLElement> {
  const dialog = await screen.findByRole("dialog", { name: "Edit Cluster" });
  await user.type(within(dialog).getByLabelText("Name"), " renamed");
  await user.click(within(dialog).getByRole("button", { name: "Save" }));
  expect(
    await within(dialog).findByRole("button", { name: "Saving..." }),
  ).toBeDisabled();
  return dialog;
}

const RENAME = [CLUSTER_URL, { name: "cluster02 renamed" }];

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  // The SSH credentials lookup needs manage:ssh_credentials. A caller that lacks
  // it is the plain case, and its failure is not what is under test.
  mockedGet.mockRejectedValue(
    new ApiClientError(403, { error: "forbidden", message: "denied" }),
  );
  mockedPut.mockResolvedValue({ cluster: CLUSTER });
});

afterEach(() => {
  signOutForGood();
});

describe("a cluster save that settles", () => {
  it("shows a failure in the dialog, with no toast, and closes it when the second try succeeds", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    const closed = vi.fn();
    renderOnAppClient(<Host closed={closed} />);

    const dialog = await saveRename(user);
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expectNoToast();
    await user.click(
      await within(dialog).findByRole("button", { name: "Save" }),
    );
    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(closed.mock.calls).toEqual([[false]]);
    expect(mockedPut.mock.calls).toEqual([RENAME, RENAME]);
    expectNoToast();
  });

  // A refusal the dialog would have answered with a prompt: gone, it is a
  // toast in the server's words, and nothing acknowledges it for anyone.
  const REFUSAL =
    "Node addresses are learned from this API, so the stored SSH credential and every pinned host key were entrusted to machines this cluster is leaving.";
  it.each([
    ["a failure", denied(), DENIED],
    [
      "a refusal that asks for an acknowledgement",
      new ApiClientError(422, {
        error: "ssh_trust_reset_confirm_required",
        message: REFUSAL,
        details: { clears_ssh_credential: true, clears_pinned_hosts: 2 },
      }),
      REFUSAL,
    ],
  ])(
    "toasts %s that comes after the dialog was dismissed, once, naming the cluster",
    async (_, failure, said) => {
      const user = userEvent.setup();
      const held = heldOnce(mockedPut);
      renderOnAppClient(<Host closed={vi.fn()} />);

      await dismiss(user, await saveRename(user));
      held.reject(failure);

      await expectOneToast(failedToast(SAVING, said));
      expect(mockedPut).toHaveBeenCalledTimes(1);
    },
  );

  it("says nothing of a failure that comes after a sign-out", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    renderOnAppClient(<Host closed={vi.fn()} />);

    await dismiss(user, await saveRename(user));
    SIGN_OUT[1]();
    held.reject(denied());

    await expectSilence();
  });
});

// A caller that kept the dialog mounted would see the same component show a new
// dialog on every open, and reset() would be all that kept the last one's
// mutation out of it. None does today (ClusterCertificateBanner's comment says
// why), but the component's own reset() calls are written for it.
describe("a cluster save that settles after the dialog was closed and opened again", () => {
  /** Saves, dismisses the dialog with the request held, and opens it again. */
  async function closeAndReopen(user: UserEvent) {
    await dismiss(user, await saveRename(user));
    await user.click(screen.getByRole("button", { name: "Edit again" }));
    return screen.findByRole("dialog", { name: "Edit Cluster" });
  }

  // What reset() is there for: a failure the first dialog showed is gone from
  // the next. Cancel reaches only the effect on `open` (it calls the parent's
  // setter directly, not Radix's onOpenChange), Escape reaches both.
  it.each([
    ["Escape", ESCAPE],
    ["Cancel", CANCEL_BUTTON],
  ])(
    "shows nothing of a failure the dialog showed when it is closed with %s and opened again",
    async (_, leave) => {
      const user = userEvent.setup();
      mockedPut.mockRejectedValueOnce(denied());
      renderOnAppClient(<Host closed={vi.fn()} keep />);

      const first = await screen.findByRole("dialog", { name: "Edit Cluster" });
      await user.type(within(first).getByLabelText("Name"), " renamed");
      await user.click(within(first).getByRole("button", { name: "Save" }));
      expect(await within(first).findByText(DENIED)).toBeInTheDocument();
      await dismiss(user, first, leave);
      await user.click(screen.getByRole("button", { name: "Edit again" }));
      const second = await screen.findByRole("dialog", {
        name: "Edit Cluster",
      });

      expect(within(second).queryByText(DENIED)).toBeNull();
      expectNoToast();
    },
  );

  it("toasts its failure, and shows nothing of it in the new dialog, whose Save is not held for it", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    renderOnAppClient(<Host closed={vi.fn()} keep />);

    const second = await closeAndReopen(user);
    held.reject(denied());

    await expectOneToast(failedToast(SAVING));
    expect(second).toBeInTheDocument();
    expect(within(second).queryByText(DENIED)).toBeNull();
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("does not ask the parent to close the new dialog when it succeeds", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    const closed = vi.fn();
    const { qc } = renderOnAppClient(<Host closed={closed} keep />);

    const second = await closeAndReopen(user);
    closed.mockClear();
    held.resolve({ cluster: CLUSTER });
    await waitForSuccess(qc);

    expect(second).toHaveAttribute("data-state", "open");
    expect(closed).not.toHaveBeenCalled();
    expectNoToast();
  });

  it("shows the failure of the new dialog's own save in it, whatever became of the older one", async () => {
    const user = userEvent.setup();
    const older = heldOnce(mockedPut);
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Permission check failed",
      }),
    );
    renderOnAppClient(<Host closed={vi.fn()} keep />);

    const second = await closeAndReopen(user);
    await user.click(within(second).getByRole("button", { name: "Save" }));

    expect(
      await within(second).findByText("Permission check failed"),
    ).toBeInTheDocument();
    older.reject(denied());
    await expectOneToast(failedToast(SAVING));
    // The older save did not take the new dialog's error away.
    expect(
      within(second).getByText("Permission check failed"),
    ).toBeInTheDocument();
  });
});

// A parent can give the one mounted dialog another cluster while a save is out:
// the dialog is then for another cluster, and the save left behind is not its
// own. What it is told apart by is the cluster, not whether it is open, which
// stays true across the change.
describe.each([
  ["still has its cluster", false],
  ["was given another cluster", true],
])("a cluster save that settles when the dialog %s", (_, given) => {
  const PRIVATE_ADDRESS = "That address is on a private network.";

  /** Saves, then gives the dialog the other cluster from the keyboard, if the case says so. */
  async function save(user: UserEvent) {
    await saveRename(user);
    if (!given) return;
    await tabTo(
      user,
      screen.getByRole("button", {
        name: "Show the other cluster",
        hidden: true,
      }),
    );
    await user.keyboard("{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "Edit Cluster" });
    await within(dialog).findByText(/Update the configuration for cluster01/);
  }

  it("answers a refusal that asks for a prompt with the prompt, or with a toast naming the first cluster and no prompt", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    renderOnAppClient(<GivenAnotherCluster closed={vi.fn()} />);

    await save(user);
    held.reject(
      new ApiClientError(422, {
        error: "private_address_confirm_required",
        message: PRIVATE_ADDRESS,
        details: { ip: "192.0.2.10" },
      }),
    );

    if (given) {
      await expectOneToast(failedToast(SAVING, PRIVATE_ADDRESS));
      expect(screen.queryByText("Private network address")).toBeNull();
    } else {
      expect(
        await screen.findByText("Private network address"),
      ).toBeInTheDocument();
      await flushInAct();
      expectNoToast();
    }
  });

  it("asks the parent to close the dialog when it succeeds, or does not, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    const closed = vi.fn();
    const { qc } = renderOnAppClient(<GivenAnotherCluster closed={closed} />);

    await save(user);
    held.resolve({ cluster: CLUSTER });
    await waitForSuccess(qc);

    expect(closed.mock.calls).toEqual(given ? [] : [[false]]);
    expectNoToast();
  });
});
