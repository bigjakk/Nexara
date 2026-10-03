import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { ApiClientError, apiClient } from "@/lib/api-client";
import { deferred } from "@/test/fake-server";
import {
  DENIED,
  SESSION_ENDINGS,
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
 * Saving a cluster's settings opts out of the global error toast
 * (useUpdateCluster), because the open dialog shows the failure itself, and the
 * dialog can be dismissed while the request is out. Once it is gone the failure
 * is a toast naming the cluster, unless the session has ended too
 * (hooks/useSaveOutcome.ts); a success that comes after it is gone closes
 * nothing.
 *
 * These run on the app's own kind of client, whose mutation cache is the one
 * that raises the global toast, so that "once" means once and "none" means none.
 */

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      get: vi.fn(),
      list: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedPut = vi.mocked(apiClient.put);
const mockedGet = vi.mocked(apiClient.get);
const mockedToastError = vi.mocked(toast.error);

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

type UserEvent = ReturnType<typeof userEvent.setup>;

/**
 * The dialog the way every caller mounts it: only while it is open, and a button
 * beside it to open it again. `closed` hears every answer to onOpenChange.
 */
function MountedWhileOpen({ closed }: { closed: (open: boolean) => void }) {
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
      {open && (
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
 * The dialog kept mounted and driven by its open prop, which its own reset()
 * calls are written for: closing it and opening it again is the same component
 * showing a different dialog.
 */
function KeptMounted({ closed }: { closed: (open: boolean) => void }) {
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
      <EditClusterDialog
        cluster={CLUSTER}
        open={open}
        onOpenChange={(next) => {
          closed(next);
          setOpen(next);
        }}
      />
    </>
  );
}

/**
 * The one dialog mounted, with a button beside it that gives it another cluster
 * without remounting it: what a parent that does not key the dialog on its
 * cluster does when another cluster's Edit is pressed. (ClustersListPage keys
 * it, and its own test reaches the dialog the way a person does.)
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

/** What each way of getting rid of the dialog does to it. */
const LEAVES: [
  name: string,
  leave: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
][] = [
  [
    "Escape",
    async (user) => {
      await user.keyboard("{Escape}");
    },
  ],
  [
    "its Close button",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Close" }));
    },
  ],
  [
    "its Cancel button",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    },
  ],
  [
    "the page being left",
    () => {
      cleanup();
      return Promise.resolve();
    },
  ],
];

/** Waits for a toast, then for anything else a second one could be late with. */
async function expectOneToast(message: string): Promise<void> {
  await waitFor(() => {
    expect(mockedToastError).toHaveBeenCalledWith(message);
  });
  await flushInAct();
  expect(mockedToastError).toHaveBeenCalledTimes(1);
}

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  // The SSH credentials lookup needs manage:ssh_credentials. A caller that
  // lacks it is the plain case, and its failure is not what is under test.
  mockedGet.mockRejectedValue(
    new ApiClientError(403, { error: "forbidden", message: "denied" }),
  );
  mockedPut.mockResolvedValue({ cluster: CLUSTER });
});

afterEach(() => {
  signOutForGood();
});

describe("a cluster save that settles", () => {
  it("control: closes the dialog when it succeeds while the dialog is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    const closed = vi.fn();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<MountedWhileOpen closed={closed} />);

    const dialog = await saveRename(user);
    held.resolve({ cluster: CLUSTER });

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(closed.mock.calls).toEqual([[false]]);
    expect(mockedPut.mock.calls).toEqual([
      [CLUSTER_URL, { name: "cluster02 renamed" }],
    ]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows a failure in the dialog while the dialog is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<MountedWhileOpen closed={vi.fn()} />);

    const dialog = await saveRename(user);
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(LEAVES)(
    "toasts a failure that comes after %s, once, naming the cluster",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderOnAppClient(<MountedWhileOpen closed={vi.fn()} />);

      const dialog = await saveRename(user);
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(`${SAVING} failed: ${DENIED}`);
      expect(mockedPut).toHaveBeenCalledTimes(1);
    },
  );

  it("toasts the refusal that comes after the dialog was dismissed", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    const REFUSAL =
      "Node addresses are learned from this API, so the stored SSH credential and every pinned host key were entrusted to machines this cluster is leaving.";
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<MountedWhileOpen closed={vi.fn()} />);

    const dialog = await saveRename(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    held.reject(
      new ApiClientError(422, {
        error: "ssh_trust_reset_confirm_required",
        message: REFUSAL,
        details: { clears_ssh_credential: true, clears_pinned_hosts: 2 },
      }),
    );

    await expectOneToast(`${SAVING} failed: ${REFUSAL}`);
    // Nothing acknowledged it: there was no one to ask.
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  it.each(LEAVES)(
    "toasts nothing, and closes nothing, for a success that comes after %s",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      const closed = vi.fn();
      mockedPut.mockReturnValueOnce(held.promise);
      const { qc } = renderOnAppClient(<MountedWhileOpen closed={closed} />);

      const dialog = await saveRename(user);
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      const answered = closed.mock.calls.length;
      held.resolve({ cluster: CLUSTER });
      await waitForSuccess(qc);

      expect(mockedToastError).not.toHaveBeenCalled();
      // Whatever the dismissal itself told the parent, the success added nothing.
      expect(closed.mock.calls).toHaveLength(answered);
    },
  );

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderOnAppClient(<MountedWhileOpen closed={vi.fn()} />);

      await saveRename(user);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

// A caller that kept the dialog mounted would see the same component show a new
// dialog on every open, and reset() would be all that kept the last one's
// mutation out of it. None does today (ClusterCertificateBanner's comment says
// why), but the component's own reset() calls are written for it.
describe("a cluster save that settles after the dialog was closed and opened again", () => {
  /** Saves, dismisses the dialog with the request held, and opens it again. */
  async function closeAndReopen(user: UserEvent, closed: () => void) {
    const first = await saveRename(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    closed();
    await user.click(screen.getByRole("button", { name: "Edit again" }));
    return screen.findByRole("dialog", { name: "Edit Cluster" });
  }

  // What reset() is there for, and what the settle above must leave alone: a
  // failure the first dialog showed is gone from the next. Cancel reaches only
  // the effect on `open` (it calls the parent's setter directly, not Radix's
  // onOpenChange), Escape reaches both.
  it.each([
    [
      "Escape",
      async (user: UserEvent) => {
        await user.keyboard("{Escape}");
      },
    ],
    [
      "Cancel",
      async (user: UserEvent, dialog: HTMLElement) => {
        await user.click(
          within(dialog).getByRole("button", { name: "Cancel" }),
        );
      },
    ],
  ] as [
    name: string,
    leave: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
  ][])(
    "control: a failure the dialog showed is gone when it is closed with %s and opened again",
    async (_, leave) => {
      const user = userEvent.setup();
      mockedPut.mockRejectedValueOnce(denied());
      renderOnAppClient(<KeptMounted closed={vi.fn()} />);

      const first = await screen.findByRole("dialog", { name: "Edit Cluster" });
      await user.type(within(first).getByLabelText("Name"), " renamed");
      await user.click(within(first).getByRole("button", { name: "Save" }));
      expect(await within(first).findByText(DENIED)).toBeInTheDocument();
      await leave(user, first);
      await waitFor(() => {
        expect(first).not.toBeInTheDocument();
      });
      await user.click(screen.getByRole("button", { name: "Edit again" }));
      const second = await screen.findByRole("dialog", {
        name: "Edit Cluster",
      });

      expect(within(second).queryByText(DENIED)).toBeNull();
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );

  it("does not close the new dialog when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    const closed = vi.fn();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<KeptMounted closed={closed} />);

    const second = await closeAndReopen(user, () => {
      closed.mockClear();
    });
    held.resolve({ cluster: CLUSTER });
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    // Nothing since the reopening asked the parent to close it.
    expect(closed).not.toHaveBeenCalled();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("toasts its failure, and shows nothing of it in the new dialog", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<KeptMounted closed={vi.fn()} />);

    const second = await closeAndReopen(user, () => undefined);
    held.reject(denied());

    await expectOneToast(`${SAVING} failed: ${DENIED}`);
    expect(second).toBeInTheDocument();
    expect(within(second).queryByText(DENIED)).toBeNull();
    // And Save is not held for a save that was the last dialog's.
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("shows the failure of the new dialog's own save in it, whatever became of the older one", async () => {
    // The control for the two above: the new dialog reports what it sent.
    const user = userEvent.setup();
    const older = deferred<unknown>();
    mockedPut.mockReturnValueOnce(older.promise).mockRejectedValueOnce(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Permission check failed",
      }),
    );
    renderOnAppClient(<KeptMounted closed={vi.fn()} />);

    const second = await closeAndReopen(user, () => undefined);
    await user.click(within(second).getByRole("button", { name: "Save" }));

    expect(
      await within(second).findByText("Permission check failed"),
    ).toBeInTheDocument();
    older.reject(denied());
    await expectOneToast(`${SAVING} failed: ${DENIED}`);
    // The older save did not take the new dialog's error away.
    expect(
      within(second).getByText("Permission check failed"),
    ).toBeInTheDocument();
  });
});

// A parent can give the one mounted dialog another cluster while a save is out:
// the dialog is not held while its request is out, and the button that sent it is
// disabled by the request, so Tab walks out of the modal to the controls behind
// it, which a pointer cannot reach. The dialog is then for another cluster, and
// the save left behind is not its own. What it is told apart by is the cluster,
// not whether it is open: that stays true across the change.
describe("a cluster save that settles after the dialog was given another cluster", () => {
  const PRIVATE = () =>
    new ApiClientError(422, {
      error: "private_address_confirm_required",
      message: "That address is on a private network.",
      details: { ip: "192.0.2.10" },
    });

  /** Saves, then gives the dialog the other cluster from the keyboard. */
  async function giveAnother(user: UserEvent) {
    await saveRename(user);
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
    return dialog;
  }

  it("control: raises the private-address prompt when the refusal comes while the dialog still has its cluster", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<GivenAnotherCluster closed={vi.fn()} />);

    await saveRename(user);
    held.reject(PRIVATE());

    expect(
      await screen.findByText("Private network address"),
    ).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("toasts the refusal, once, naming the first cluster, and raises no prompt in the dialog for the other", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderOnAppClient(<GivenAnotherCluster closed={vi.fn()} />);

    await giveAnother(user);
    held.reject(PRIVATE());

    await expectOneToast(
      `${SAVING} failed: That address is on a private network.`,
    );
    expect(screen.queryByText("Private network address")).toBeNull();
  });

  it("does not ask the parent to close the dialog for the other cluster when it succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    const closed = vi.fn();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<GivenAnotherCluster closed={closed} />);

    await giveAnother(user);
    held.resolve({ cluster: CLUSTER });
    await waitForSuccess(qc);

    expect(closed).not.toHaveBeenCalled();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("closes the dialog when it succeeds while the dialog still has its cluster", async () => {
    // The control for the one above: the parent is asked to close it.
    const user = userEvent.setup();
    const held = deferred<unknown>();
    const closed = vi.fn();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderOnAppClient(<GivenAnotherCluster closed={closed} />);

    await saveRename(user);
    held.resolve({ cluster: CLUSTER });
    await waitForSuccess(qc);

    expect(closed.mock.calls).toEqual([[false]]);
  });
});
