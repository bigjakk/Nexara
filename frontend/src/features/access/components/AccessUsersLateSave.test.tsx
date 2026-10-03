import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
import type { AccessCapabilities } from "../api/access-queries";
import { AccessUsersSection } from "./AccessUsersSection";

/**
 * The saves of this section that opt out of the global error toast: creating a
 * user, deleting one, and creating, revoking and regenerating a token. Each is
 * reported by whatever sent it while that is on screen, and once it is gone —
 * the dialog dismissed, the row collapsed, the page left — by a toast that
 * names the object, unless the session has ended too (hooks/useSaveOutcome.ts).
 *
 * Editing a user is the same story and is tested beside the rest of that
 * dialog, in AccessUsersSection.test.tsx, apart from one thing that is about how
 * the section mounts it: its dialog replaced by another account's from the
 * keyboard (the last describe below).
 *
 * These run on the app's own kind of client, whose mutation cache is the one
 * that raises the global toast, so that "once" means once and "none" means none:
 * on a client without it, the opt-out could be taken off every hook and the
 * tests would pass.
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

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedList = vi.mocked(apiClient.list);
const mockedGet = vi.mocked(apiClient.get);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000007";
const USERS_URL = `/api/v1/clusters/${CLUSTER}/access/users`;
const ALICE = "alice@pve";
const ALICE_URL = `${USERS_URL}/alice%40pve`;
const TOKEN = "token01";
const FULL_TOKEN = `${ALICE}!${TOKEN}`;
const TOKENS_URL = `${ALICE_URL}/tokens`;
const TOKEN_URL = `${TOKENS_URL}/${TOKEN}`;
// The token a test creates, and the user it creates.
const NEW_TOKEN = "token02";
const FULL_NEW_TOKEN = `${ALICE}!${NEW_TOKEN}`;
const NEW_TOKEN_URL = `${TOKENS_URL}/${NEW_TOKEN}`;
const CAROL = "carol@pve";
const DAVE = "dave@pve";
// Not a real secret: what a token's one-time answer carries in this file.
const SECRET = "00000000-0000-0000-0000-000000000000";

// Worded as guardSelfCredential (internal/api/handlers/access.go) words it.
const REFUSAL =
  "This is the user alice@pve Nexara uses to reach this cluster. " +
  "Continuing can cut Nexara off, or take away permissions it relies on, " +
  "until its credentials are updated in Nexara or the change is undone in " +
  "Proxmox. Retry with force=true to proceed anyway.";

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

type UserEvent = ReturnType<typeof userEvent.setup>;

function renderSection() {
  return renderOnAppClient(
    <AccessUsersSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

/** What each way of getting rid of a dialog does to it. */
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
    "the page being left",
    () => {
      cleanup();
      return Promise.resolve();
    },
  ],
];

/** The toast a failed save leaves once what sent it is gone. */
function failedToast(action: string, message = DENIED): string {
  return `${action} failed: ${message}`;
}

/**
 * The toast a refusal leaves once its dialog is gone, when the refusal had an
 * override to be given. It says what became of the action and what to do, and
 * quotes none of the server's words: they end in "Retry with force=true to
 * proceed anyway", which a person cannot act on from a toast.
 */
function refusedToast(action: string): string {
  return `${action} was refused, and nothing was changed, because it could cut Nexara off from the cluster. To go ahead anyway, do it again and confirm the override that is then offered.`;
}

/** Waits for a toast, then for anything else a second one could be late with. */
async function expectOneToast(message: string): Promise<void> {
  await waitFor(() => {
    expect(mockedToastError).toHaveBeenCalledWith(message);
  });
  await flushInAct();
  expect(mockedToastError).toHaveBeenCalledTimes(1);
}

// How long the notices that a token's secret could not be shown stay up, as
// the section says it (SECRET_NOTICE_MS): long enough to be read by someone who
// has just left the page, and not for ever — sonner hands a toast that was not
// dismissed to the next Toaster that mounts, which a sign-out and the next
// sign-in rebuild.
const SECRET_NOTICE_MS = 30_000;

/**
 * Waits for a notice that a token's secret could not be shown, which stays up
 * for SECRET_NOTICE_MS where every other toast here takes sonner's default, and
 * then for anything else a second one could be late with.
 */
async function expectOneNotice(message: string): Promise<void> {
  await waitFor(() => {
    expect(mockedToastError).toHaveBeenCalledWith(message, {
      duration: SECRET_NOTICE_MS,
    });
  });
  await flushInAct();
  expect(mockedToastError).toHaveBeenCalledTimes(1);
}

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  mockedList.mockImplementation((path: string) => {
    if (path === USERS_URL) {
      return Promise.resolve([
        { userid: ALICE, enable: true, comment: "service account" },
      ]);
    }
    if (path === TOKENS_URL) {
      return Promise.resolve([
        { userid: ALICE, tokenid: TOKEN, privsep: true },
      ]);
    }
    return Promise.resolve([]);
  });
  mockedPost.mockResolvedValue({});
  mockedPut.mockResolvedValue({});
  mockedDelete.mockResolvedValue(undefined);
});

afterEach(() => {
  signOutForGood();
});

// ── Create user ─────────────────────────────────────────────────────────

/** Opens the Create User dialog. */
async function openCreateUser(user: UserEvent): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: "Create User" }));
  return screen.findByRole("dialog", { name: "Create Proxmox User" });
}

/**
 * Opens it, types the id and presses Create, and waits for the request to be
 * out. The id is typed with a space either side, as a pasted one often has: the
 * request and the toast carry it trimmed.
 */
async function createUserNamed(
  user: UserEvent,
  userid: string,
): Promise<HTMLElement> {
  const dialog = await openCreateUser(user);
  await user.type(within(dialog).getByLabelText("User ID"), ` ${userid} `);
  await user.click(within(dialog).getByRole("button", { name: "Create" }));
  expect(
    await within(dialog).findByRole("button", { name: "Creating..." }),
  ).toBeDisabled();
  return dialog;
}

describe("a user create that settles", () => {
  it("control: closes its dialog when it succeeds while the dialog is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderSection();

    const dialog = await createUserNamed(user, CAROL);
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedPost.mock.calls).toEqual([[USERS_URL, { userid: CAROL }]]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows a failure in its dialog while the dialog is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderSection();

    const dialog = await createUserNamed(user, CAROL);
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it.each(LEAVES)(
    "toasts a failure that comes after %s, once, naming the user",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      const dialog = await createUserNamed(user, CAROL);
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(failedToast(`Creating user ${CAROL}`));
      expect(mockedPost).toHaveBeenCalledTimes(1);
    },
  );

  it("toasts a failure that comes after the dialog was dismissed and opened again, and shows nothing in the new one", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderSection();

    const first = await createUserNamed(user, CAROL);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    const second = await openCreateUser(user);
    await user.type(within(second).getByLabelText("User ID"), DAVE);
    held.reject(denied());

    await expectOneToast(failedToast(`Creating user ${CAROL}`));
    // The dialog that is open is the second one's, and this was not its save.
    expect(second).toBeInTheDocument();
    expect(within(second).queryByText(DENIED)).toBeNull();
    expect(within(second).getByLabelText("User ID")).toHaveValue(DAVE);
  });

  it("does not close or clear the dialog opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    const { qc } = renderSection();

    const first = await createUserNamed(user, CAROL);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    const second = await openCreateUser(user);
    await user.type(within(second).getByLabelText("User ID"), DAVE);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByLabelText("User ID")).toHaveValue(DAVE);
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      await createUserNamed(user, CAROL);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
      expect(mockedPost).toHaveBeenCalledTimes(1);
    },
  );
});

// ── Delete user ─────────────────────────────────────────────────────────

/** Opens the delete confirmation for the user, confirms it, and waits for the request to be out. */
async function deleteAlice(user: UserEvent): Promise<HTMLElement> {
  await user.click(
    await screen.findByRole("button", { name: `Delete ${ALICE}` }),
  );
  const ask = await screen.findByRole("alertdialog", {
    name: `Delete ${ALICE}?`,
  });
  await user.click(within(ask).getByRole("button", { name: "Delete User" }));
  expect(
    await within(ask).findByRole("button", { name: "Deleting..." }),
  ).toBeDisabled();
  return ask;
}

describe("a user delete that settles", () => {
  it("control: shows a failure in the section while the page is there, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderSection();

    await deleteAlice(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedDelete.mock.calls).toEqual([[ALICE_URL]]);
  });

  it("toasts a failure that comes after the page was left, once, naming the user", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderSection();

    await deleteAlice(user);
    cleanup();
    held.reject(denied());

    await expectOneToast(failedToast(`Deleting user ${ALICE}`));
    expect(mockedDelete.mock.calls).toEqual([[ALICE_URL]]);
  });

  it("toasts the refusal that comes after the page was left as a refusal, saying what to do and not quoting the server", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderSection();

    await deleteAlice(user);
    cleanup();
    held.reject(
      new ApiClientError(409, { error: "Conflict", message: REFUSAL }),
    );

    await expectOneToast(refusedToast(`Deleting user ${ALICE}`));
    // Nothing forced it: there was no one to ask.
    expect(mockedDelete.mock.calls).toEqual([[ALICE_URL]]);
  });

  it("toasts nothing for a success that comes after the page was left", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderSection();

    await deleteAlice(user);
    cleanup();
    held.resolve(undefined);
    await waitForSuccess(qc);

    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedDelete.mockReturnValueOnce(held.promise);
      renderSection();

      await deleteAlice(user);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
      expect(mockedDelete).toHaveBeenCalledTimes(1);
    },
  );
});

// ── Tokens ──────────────────────────────────────────────────────────────

/** Expands the user's row, which reads its tokens. */
async function expandAlice(user: UserEvent): Promise<void> {
  await user.click(await screen.findByText(ALICE));
  await screen.findByText(FULL_TOKEN);
}

/**
 * Expands the row, types a name into the create form and presses Create Token.
 * The name is typed with a space either side: the request and the toast carry
 * it trimmed.
 */
async function createNewToken(user: UserEvent): Promise<void> {
  await expandAlice(user);
  await user.type(screen.getByLabelText("New token name"), ` ${NEW_TOKEN} `);
  await user.click(screen.getByRole("button", { name: "Create Token" }));
  expect(
    await screen.findByRole("button", { name: "Creating..." }),
  ).toBeDisabled();
}

/** What Proxmox answers a token create or regenerate with. */
function minted(fullTokenId: string) {
  return { "full-tokenid": fullTokenId, value: SECRET };
}

/** Asserts that the secret is in no toast, whatever kind it was raised as. */
function expectSecretInNoToast(): void {
  for (const spy of [toast.error, toast.success, toast.warning]) {
    expect(JSON.stringify(vi.mocked(spy).mock.calls)).not.toContain(SECRET);
  }
}

/**
 * Asserts that the secret is not on the page. Only for a test in which the
 * section is still mounted when the answer arrives: the secret dialog is drawn
 * from state of the row that asked for it, so with the row collapsed or the page
 * left there is nothing to draw it, and the page cannot show the secret
 * whatever the code does. A test that has left has the toast half only.
 */
function expectSecretNotOnPage(): void {
  expect(document.body).not.toHaveTextContent(SECRET);
}

describe("a token create that settles", () => {
  it("control: shows the one-time secret when it succeeds while the row is there", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderSection();

    await createNewToken(user);
    held.resolve(minted(FULL_NEW_TOKEN));

    const shown = await screen.findByRole("dialog", {
      name: "API Token Created",
    });
    expect(within(shown).getByText(SECRET)).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPost.mock.calls).toEqual([[NEW_TOKEN_URL, { privsep: true }]]);
  });

  it("control: shows a failure in the row while the row is there, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderSection();

    await createNewToken(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  const ROW_GONE: [name: string, leave: (user: UserEvent) => Promise<void>][] =
    [
      [
        "the row being collapsed",
        async (user) => {
          await user.click(screen.getByText(ALICE));
          await waitFor(() => {
            expect(screen.queryByText(FULL_TOKEN)).toBeNull();
          });
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

  it.each(ROW_GONE)(
    "tells the operator the secret could not be shown, naming the token and never giving the secret, and keeps saying so, when it succeeds after %s",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      await createNewToken(user);
      await leave(user);
      held.resolve(minted(FULL_NEW_TOKEN));

      await expectOneNotice(
        `Created the API token ${FULL_NEW_TOKEN}, but its secret could not be shown because this view was closed. Proxmox shows a secret only once: regenerate the token to get a new one.`,
      );
      expectSecretInNoToast();
    },
  );

  it.each(ROW_GONE)(
    "toasts a failure that comes after %s, once, naming the token",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      await createNewToken(user);
      await leave(user);
      held.reject(denied());

      await expectOneToast(failedToast(`Creating token ${FULL_NEW_TOKEN}`));
    },
  );

  it.each(SESSION_ENDINGS)(
    "shows nothing, and never the secret, when it succeeds after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      await createNewToken(user);
      cleanup();
      end();
      held.resolve(minted(FULL_NEW_TOKEN));
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
      expectSecretInNoToast();
    },
  );

  it.each(SESSION_ENDINGS)(
    "shows nothing, and never the secret, when it succeeds after %s, with the page still there",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      await createNewToken(user);
      end();
      held.resolve(minted(FULL_NEW_TOKEN));
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
      expect(
        screen.queryByRole("dialog", { name: "API Token Created" }),
      ).toBeNull();
      expectSecretInNoToast();
      expectSecretNotOnPage();
    },
  );

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderSection();

      await createNewToken(user);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

/**
 * The two confirmations that act on an existing token, each with the request it
 * sends and what its failure is called. A token's row buttons are named for its
 * own id; its dialogs for the full one.
 */
const TOKEN_ACTIONS = [
  {
    name: "revoke",
    trigger: `Revoke ${TOKEN}`,
    ask: `Revoke ${FULL_TOKEN}?`,
    send: "Revoke Token",
    sending: "Revoking...",
    request: mockedDelete,
    action: `Revoking token ${FULL_TOKEN}`,
  },
  {
    name: "regenerate",
    trigger: `Regenerate ${TOKEN}`,
    ask: `Regenerate ${FULL_TOKEN}?`,
    send: "Regenerate",
    sending: "Regenerating...",
    request: mockedPut,
    action: `Regenerating token ${FULL_TOKEN}`,
  },
];
type TokenAction = (typeof TOKEN_ACTIONS)[number];

/** Expands the row, confirms the action on the token, and waits for the request to be out. */
async function sendTokenAction(
  user: UserEvent,
  a: TokenAction,
): Promise<HTMLElement> {
  await expandAlice(user);
  await user.click(screen.getByRole("button", { name: a.trigger }));
  const ask = await screen.findByRole("alertdialog", { name: a.ask });
  await user.click(within(ask).getByRole("button", { name: a.send }));
  expect(
    await within(ask).findByRole("button", { name: a.sending }),
  ).toBeDisabled();
  return ask;
}

describe.each(TOKEN_ACTIONS)("a token $name that settles", (a) => {
  it("control: shows a failure in the row while the row is there, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    a.request.mockReturnValueOnce(held.promise);
    renderSection();

    await sendTokenAction(user, a);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("toasts a failure that comes after the page was left, once, naming the token", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    a.request.mockReturnValueOnce(held.promise);
    renderSection();

    await sendTokenAction(user, a);
    cleanup();
    held.reject(denied());

    await expectOneToast(failedToast(a.action));
  });

  it("toasts the refusal that comes after the page was left as a refusal, saying what to do and not quoting the server", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    a.request.mockReturnValueOnce(held.promise);
    renderSection();

    await sendTokenAction(user, a);
    cleanup();
    held.reject(
      new ApiClientError(409, { error: "Conflict", message: REFUSAL }),
    );

    await expectOneToast(refusedToast(a.action));
    // Nothing forced it: there was no one to ask.
    expect(a.request).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      a.request.mockReturnValueOnce(held.promise);
      renderSection();

      await sendTokenAction(user, a);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

describe("a token revoke that succeeds after the page was left", () => {
  it("says nothing: the token is gone, and the list behind it is refreshed", async () => {
    const [revoke] = TOKEN_ACTIONS;
    if (!revoke) throw new Error("no revoke action");
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderSection();

    await sendTokenAction(user, revoke);
    cleanup();
    held.resolve(undefined);
    await waitForSuccess(qc);

    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedDelete.mock.calls).toEqual([[TOKEN_URL]]);
  });
});

describe("a token regenerate that succeeds", () => {
  const regenerate = TOKEN_ACTIONS[1];
  if (!regenerate) throw new Error("no regenerate action");

  it("control: shows the new secret while the row is there", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderSection();

    await sendTokenAction(user, regenerate);
    held.resolve(minted(FULL_TOKEN));

    const shown = await screen.findByRole("dialog", {
      name: "API Token Created",
    });
    expect(within(shown).getByText(SECRET)).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("tells the operator the new secret could not be shown, and that the old one no longer works, after the page was left", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderSection();

    await sendTokenAction(user, regenerate);
    cleanup();
    held.resolve(minted(FULL_TOKEN));

    await expectOneNotice(
      `Regenerated the API token ${FULL_TOKEN}, but its new secret could not be shown because this view was closed. The old secret no longer works, and Proxmox shows a secret only once: regenerate the token again to get a new one.`,
    );
    expectSecretInNoToast();
    expect(mockedPut.mock.calls).toEqual([[TOKEN_URL, { regenerate: true }]]);
  });

  it.each(SESSION_ENDINGS)(
    "shows nothing, and never the secret, after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderSection();

      await sendTokenAction(user, regenerate);
      cleanup();
      end();
      held.resolve(minted(FULL_TOKEN));
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
      expectSecretInNoToast();
    },
  );
});

// The edit dialog is not held while its request is out, and the button that sent
// it is disabled by the request, so focus is lost and Tab walks out of the modal
// to the Edit buttons behind it, which a pointer cannot reach. Enter on another
// account's puts its dialog where the first was, with nothing closed between the
// two. The section keys the dialog on the account, so the second is a new one
// with a form and a pending save of its own; unkeyed, it would be the first with
// another account's name over it.
describe("an edit whose dialog is replaced by another account's from the keyboard", () => {
  const BOB = "bob@pve";
  const BOB_URL = `${USERS_URL}/bob%40pve`;

  beforeEach(() => {
    mockedList.mockImplementation((path: string) =>
      Promise.resolve(
        path === USERS_URL
          ? [
              { userid: ALICE, enable: true },
              { userid: BOB, enable: true },
            ]
          : [],
      ),
    );
    mockedGet.mockImplementation((path: string) => {
      if (path === ALICE_URL) {
        return Promise.resolve({
          userid: ALICE,
          enable: true,
          comment: "service account",
        });
      }
      if (path === BOB_URL) {
        return Promise.resolve({
          userid: BOB,
          enable: true,
          comment: "read only",
        });
      }
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
  });

  /** Edits the first account's comment with the request held, and opens the other's dialog over it. */
  async function replaceWithBob(user: UserEvent) {
    await user.click(
      await screen.findByRole("button", { name: `Edit ${ALICE}` }),
    );
    const first = await screen.findByRole("dialog", { name: `Edit ${ALICE}` });
    await user.type(await within(first).findByLabelText("Comment"), " edited");
    await user.click(within(first).getByRole("button", { name: "Save" }));
    await within(first).findByRole("button", { name: "Saving..." });
    await tabTo(
      user,
      screen.getByRole("button", { name: `Edit ${BOB}`, hidden: true }),
    );
    await user.keyboard("{Enter}");
    const second = await screen.findByRole("dialog", { name: `Edit ${BOB}` });
    // The other account's form, read from the other account.
    await within(second).findByLabelText("Comment");
    return second;
  }

  it("toasts the failure, once, naming the first account, and shows nothing of it in the other's dialog", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderSection();

    const second = await replaceWithBob(user);
    held.reject(denied());

    await expectOneToast(failedToast(`Saving ${ALICE}`));
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
    // The other account's own form, read from the other account: what an
    // unkeyed dialog would not show, since the first's comment is typed in it.
    expect(within(second).getByLabelText("Comment")).toHaveValue("read only");
  });

  it("does not close the other account's dialog when the first succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderSection();

    const second = await replaceWithBob(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByLabelText("Comment")).toHaveValue("read only");
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});
