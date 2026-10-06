import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { ApiClientError, apiClient } from "@/lib/api-client";
import {
  type DialogSave,
  type Replacement,
  type SaveRequest,
  type SessionEnding,
  type UserEvent,
  SIGN_OUT,
  TAKEN_OVER,
  describeDialogSaves,
  describeReplacements,
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
import type { AccessCapabilities } from "../api/access-queries";
import { AccessUsersSection } from "./AccessUsersSection";

/**
 * What AccessUsersSection passes useSaveOutcome: creating a user, deleting one,
 * and creating, revoking and regenerating a token. Editing a user is tested with
 * the rest of its dialog in AccessUsersSection.test.tsx, apart from its dialog
 * being replaced by another account's from the keyboard (last here). The outcome
 * matrix is hooks/useSaveOutcome.test.tsx's.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

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
// Not a real secret: what a token's one-time answer carries in this file.
const SECRET = "00000000-0000-0000-0000-000000000000";

// Worded as guardSelfCredential (internal/api/handlers/access.go) words it.
const REFUSAL =
  "This is the user alice@pve Nexara uses to reach this cluster. " +
  "Continuing can cut Nexara off, or take away permissions it relies on, " +
  "until its credentials are updated in Nexara or the change is undone in " +
  "Proxmox. Retry with force=true to proceed anyway.";

/**
 * The toast a refusal leaves once its dialog is gone, when the refusal had an
 * override to be given: what became of the action and what to do, quoting none
 * of the server's words, which end in "Retry with force=true to proceed anyway".
 */
function refusedToast(action: string): string {
  return `${action} was refused, and nothing was changed, because it could cut Nexara off from the cluster. To go ahead anyway, do it again and confirm the override that is then offered.`;
}

// How long the notice that a token's secret could not be shown stays up
// (SECRET_NOTICE_MS in the section): long enough to be read by someone who has
// just left the page, and not for ever, since sonner hands a toast that was not
// dismissed to the next Toaster that mounts.
const SECRET_NOTICE_MS = 30_000;

/** The notice that a secret could not be shown: exactly one toast, with its long duration. */
async function expectOneNotice(message: string): Promise<void> {
  await expectOneToast(message);
  expect(mockedToastError).toHaveBeenCalledWith(message, {
    duration: SECRET_NOTICE_MS,
  });
}

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

function renderSection() {
  return renderOnAppClient(
    <AccessUsersSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
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

// ── Create user ─────────────────────────────────────────────────────────────

async function openCreateUser(user: UserEvent): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: "Create User" }));
  return screen.findByRole("dialog", { name: "Create Proxmox User" });
}

/** The id is typed with a space either side, as a pasted one is: the request and the toast carry it trimmed. */
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

// Told apart by its open flag. A dismissal clears the form, so the next dialog
// starts blank, and a late answer must leave what is typed in it alone.
describeDialogSaves([
  {
    name: "user create",
    render: renderSection,
    request: mockedPost,
    sent: [USERS_URL, { userid: CAROL }],
    button: "Create",
    send: (user) => createUserNamed(user, CAROL),
    action: `Creating user ${CAROL}`,
    reopen: openCreateUser,
    draft: {
      values: (dialog) => [
        within(dialog).getByLabelText<HTMLInputElement>("User ID").value,
      ],
      left: [""],
      append: (user, dialog) =>
        user.type(within(dialog).getByLabelText("User ID"), "x"),
    },
    silentAfter: SIGN_OUT,
  } satisfies DialogSave,
]);

// ── Tokens ──────────────────────────────────────────────────────────────────

/** Expands the user's row, which reads its tokens. */
async function expandAlice(user: UserEvent): Promise<void> {
  await user.click(await screen.findByText(ALICE));
  await screen.findByText(FULL_TOKEN);
}

/** The name is typed with a space either side: the request and the toast carry it trimmed. */
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

/** The secret is in no toast, whatever kind it was raised as. */
function expectSecretInNoToast(): void {
  for (const spy of [toast.error, toast.success, toast.warning]) {
    expect(JSON.stringify(vi.mocked(spy).mock.calls)).not.toContain(SECRET);
  }
}

const SECRET_DIALOG = { name: "API Token Created" };

// The secret dialog is drawn from state of the row that asked for it: with the
// row collapsed or the page left there is nothing to draw it, so what is told
// of a secret that came too late is a toast, never the secret itself.
describe("a token create that settles", () => {
  it("shows a failure in the row while the row is there, with no toast", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPost);
    renderSection();

    await createNewToken(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expectNoToast();
  });

  it("shows the one-time secret when it succeeds while the row is there", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPost);
    renderSection();

    await createNewToken(user);
    held.resolve(minted(FULL_NEW_TOKEN));

    const shown = await screen.findByRole("dialog", SECRET_DIALOG);
    expect(within(shown).getByText(SECRET)).toBeInTheDocument();
    expectNoToast();
    expect(mockedPost.mock.calls).toEqual([[NEW_TOKEN_URL, { privsep: true }]]);
  });

  /** Sends the create, then collapses the row: its tokens, and what asked for the secret, are gone. */
  async function createThenCollapse(user: UserEvent) {
    const held = heldOnce(mockedPost);
    renderSection();
    await createNewToken(user);
    await user.click(screen.getByText(ALICE));
    await waitFor(() => {
      expect(screen.queryByText(FULL_TOKEN)).toBeNull();
    });
    return held;
  }

  it("tells the operator the secret could not be shown, naming the token and never giving the secret, when it succeeds after the row was collapsed", async () => {
    const held = await createThenCollapse(userEvent.setup());
    held.resolve(minted(FULL_NEW_TOKEN));

    await expectOneNotice(
      `Created the API token ${FULL_NEW_TOKEN}, but its secret could not be shown because this view was closed. Proxmox shows a secret only once: regenerate the token to get a new one.`,
    );
    expectSecretInNoToast();
  });

  it("toasts a failure once, naming the token, when it comes after the row was collapsed", async () => {
    const held = await createThenCollapse(userEvent.setup());
    held.reject(denied());

    await expectOneToast(failedToast(`Creating token ${FULL_NEW_TOKEN}`));
  });

  it("shows nothing, and never the secret, when it succeeds after a sign-out, with the row collapsed", async () => {
    const held = await createThenCollapse(userEvent.setup());
    SIGN_OUT[1]();
    held.resolve(minted(FULL_NEW_TOKEN));

    await expectSilence();
    expectSecretInNoToast();
  });

  it("shows nothing, and never the secret, when it succeeds after someone else signed in, with the row still there", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPost);
    renderSection();

    await createNewToken(user);
    TAKEN_OVER[1]();
    held.resolve(minted(FULL_NEW_TOKEN));

    await expectSilence();
    expect(screen.queryByRole("dialog", SECRET_DIALOG)).toBeNull();
    expectSecretInNoToast();
    expect(document.body).not.toHaveTextContent(SECRET);
  });
});

// ── The confirmations: user delete, token revoke, token regenerate ──────────

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

/** A token's row buttons are named for its own id; its dialogs for the full one. */
function tokenAction(
  verb: "Revoke" | "Regenerate",
  send: string,
  sending: string,
) {
  return async (user: UserEvent): Promise<HTMLElement> => {
    await expandAlice(user);
    await user.click(screen.getByRole("button", { name: `${verb} ${TOKEN}` }));
    const ask = await screen.findByRole("alertdialog", {
      name: `${verb} ${FULL_TOKEN}?`,
    });
    await user.click(within(ask).getByRole("button", { name: send }));
    expect(
      await within(ask).findByRole("button", { name: sending }),
    ).toBeDisabled();
    return ask;
  };
}

const CONFIRMATIONS: {
  name: string;
  request: SaveRequest;
  send: (user: UserEvent) => Promise<HTMLElement>;
  action: string;
  sent: unknown[][];
  /** What a success that comes after the page was left does: nothing, or tells of a secret. */
  late: "nothing" | "notice";
  silentAfter?: SessionEnding;
}[] = [
  {
    name: "user delete",
    request: mockedDelete,
    send: deleteAlice,
    action: `Deleting user ${ALICE}`,
    sent: [[ALICE_URL]],
    late: "nothing",
    silentAfter: TAKEN_OVER,
  },
  {
    name: "token revoke",
    request: mockedDelete,
    send: tokenAction("Revoke", "Revoke Token", "Revoking..."),
    action: `Revoking token ${FULL_TOKEN}`,
    sent: [[TOKEN_URL]],
    late: "nothing",
  },
  {
    name: "token regenerate",
    request: mockedPut,
    send: tokenAction("Regenerate", "Regenerate", "Regenerating..."),
    action: `Regenerating token ${FULL_TOKEN}`,
    sent: [[TOKEN_URL, { regenerate: true }]],
    late: "notice",
  },
];

describe.each(CONFIRMATIONS)("a $name that settles", (c) => {
  it("shows a failure on the page while the page is there, with no toast", async () => {
    const user = userEvent.setup();
    const held = heldOnce(c.request);
    renderSection();

    await c.send(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expectNoToast();
  });

  it("toasts a failure once, naming it, when it comes after the page was left", async () => {
    const user = userEvent.setup();
    const held = heldOnce(c.request);
    renderSection();

    await c.send(user);
    cleanup();
    held.reject(denied());

    await expectOneToast(failedToast(c.action));
    expect(c.request.mock.calls).toEqual(c.sent);
  });

  it("toasts the refusal that comes after the page was left as a refusal, saying what to do and not quoting the server", async () => {
    const user = userEvent.setup();
    const held = heldOnce(c.request);
    renderSection();

    await c.send(user);
    cleanup();
    held.reject(
      new ApiClientError(409, { error: "Conflict", message: REFUSAL }),
    );

    await expectOneToast(refusedToast(c.action));
    // Nothing forced it: there was no one to ask.
    expect(c.request.mock.calls).toEqual(c.sent);
  });

  if (c.late === "notice") {
    it("tells the operator the new secret could not be shown, and that the old one no longer works, when it succeeds after the page was left", async () => {
      const user = userEvent.setup();
      const held = heldOnce(c.request);
      renderSection();

      await c.send(user);
      cleanup();
      held.resolve(minted(FULL_TOKEN));

      await expectOneNotice(
        `Regenerated the API token ${FULL_TOKEN}, but its new secret could not be shown because this view was closed. The old secret no longer works, and Proxmox shows a secret only once: regenerate the token again to get a new one.`,
      );
      expectSecretInNoToast();
    });

    it("shows the new secret when it succeeds while the row is there", async () => {
      const user = userEvent.setup();
      const held = heldOnce(c.request);
      renderSection();

      await c.send(user);
      held.resolve(minted(FULL_TOKEN));

      const shown = await screen.findByRole("dialog", SECRET_DIALOG);
      expect(within(shown).getByText(SECRET)).toBeInTheDocument();
      expectNoToast();
    });
  } else {
    it("says nothing when it succeeds after the page was left: it is gone, and the list behind it is refreshed", async () => {
      const user = userEvent.setup();
      const held = heldOnce(c.request);
      const { qc } = renderSection();

      await c.send(user);
      cleanup();
      held.resolve(undefined);
      await waitForSuccess(qc);

      expectNoToast();
      expect(c.request.mock.calls).toEqual(c.sent);
    });
  }

  if (c.silentAfter) {
    const [after, end] = c.silentAfter;
    it(`says nothing of a failure that comes after ${after}, with the page left`, async () => {
      const user = userEvent.setup();
      const held = heldOnce(c.request);
      renderSection();

      await c.send(user);
      cleanup();
      end();
      held.reject(denied());

      await expectSilence();
    });
  }
});

// ── Edit user: its dialog replaced by another account's from the keyboard ────

// The section keys the dialog on the account, so the second is a new one with a
// form and a pending save of its own; unkeyed, it would be the first with
// another account's name over it.
const BOB = "bob@pve";
const BOB_URL = `${USERS_URL}/bob%40pve`;

function serveBob() {
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
}

const EDIT_USER: Replacement = {
  name: "user edit",
  render: () => {
    serveBob();
    return renderSection();
  },
  request: mockedPut,
  action: `Saving ${ALICE}`,
  replace: async (user) => {
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
  },
  // What an unkeyed dialog would not show, since the first's comment is typed in it.
  own: (second) => {
    expect(within(second).getByLabelText("Comment")).toHaveValue("read only");
  },
};

describeReplacements([EDIT_USER]);
