import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { apiClient } from "@/lib/api-client";
import {
  CANCEL_BUTTON,
  type DialogSave,
  type Replacement,
  type UserEvent,
  SIGN_OUT,
  TAKEN_OVER,
  describeDialogSaves,
  describeReplacements,
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
import { toastsRaised } from "@/test/late-toast-sessions";
import type { AccessCapabilities } from "../api/access-queries";
import { AccessACLSection } from "./AccessACLSection";
import { AccessGroupsSection } from "./AccessGroupsSection";
import { AccessRolesSection } from "./AccessRolesSection";

/**
 * What the group, role and permission sections pass useSaveOutcome: the `shown`
 * each tells its dialogs apart by, and the handlers and action that go with it.
 * The outcome matrix is hooks/useSaveOutcome.test.tsx's; deletes do not use the
 * hook and have their own guard, tested below.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);

const CLUSTER = "cccccccc-0000-0000-0000-000000000008";
const ACCESS = `/api/v1/clusters/${CLUSTER}/access`;

/** What the server holds for the Operator role: a test of an edit changes it when the save goes through. */
let operatorPrivs = "VM.Audit,VM.PowerMgmt";

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

/** The table row whose text is `text`, to look for a button inside it. */
function rowOf(text: string): HTMLElement {
  const row = screen.getByText(text).closest("tr");
  if (!row) throw new Error(`no table row holds ${text}`);
  return row;
}

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  operatorPrivs = "VM.Audit,VM.PowerMgmt";
  mockedList.mockImplementation((path: string) => {
    if (path === `${ACCESS}/groups`) {
      return Promise.resolve([
        { groupid: "operators", comment: "day shift" },
        { groupid: "auditors", comment: "read only" },
      ]);
    }
    if (path === `${ACCESS}/roles`) {
      return Promise.resolve([
        { roleid: "Operator", privs: operatorPrivs, special: false },
        { roleid: "Auditor", privs: "VM.Audit", special: false },
      ]);
    }
    if (path === `${ACCESS}/acl`) {
      return Promise.resolve([
        {
          path: "/",
          type: "user",
          ugid: "alice@pve",
          roleid: "Operator",
          propagate: true,
        },
        {
          path: "/vms/100",
          type: "group",
          ugid: "operators",
          roleid: "Auditor",
          propagate: false,
        },
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

// ── Senders: open the dialog, press its button, wait for the request to be out ─

function renderGroups() {
  return renderOnAppClient(
    <AccessGroupsSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

function renderRoles() {
  return renderOnAppClient(
    <AccessRolesSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

function renderACL() {
  return renderOnAppClient(
    <AccessACLSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

/** The id is typed with a space either side, as a pasted one is: the request and the toast carry it trimmed. */
async function createGroupNamed(
  user: UserEvent,
  groupid: string,
  comment?: string,
): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: "Create Group" }));
  const dialog = await screen.findByRole("dialog", { name: "Create Group" });
  await user.type(within(dialog).getByLabelText("Group ID"), ` ${groupid} `);
  if (comment !== undefined) {
    await user.type(within(dialog).getByLabelText("Comment"), comment);
  }
  await user.click(within(dialog).getByRole("button", { name: "Create" }));
  expect(
    await within(dialog).findByRole("button", { name: "Creating..." }),
  ).toBeDisabled();
  return dialog;
}

async function saveGroupComment(
  user: UserEvent,
  groupid: string,
): Promise<HTMLElement> {
  await user.click(
    await screen.findByRole("button", { name: `Edit ${groupid}` }),
  );
  const dialog = await screen.findByRole("dialog", { name: `Edit ${groupid}` });
  await user.type(within(dialog).getByLabelText("Comment"), " edited");
  await user.click(within(dialog).getByRole("button", { name: "Save" }));
  expect(
    await within(dialog).findByRole("button", { name: "Saving..." }),
  ).toBeDisabled();
  return dialog;
}

async function saveRole(user: UserEvent, roleid: string): Promise<HTMLElement> {
  await screen.findByText(roleid);
  await user.click(within(rowOf(roleid)).getByRole("button", { name: "Edit" }));
  const dialog = await screen.findByRole("dialog", { name: `Edit ${roleid}` });
  await user.click(within(dialog).getByRole("button", { name: "Save" }));
  expect(
    await within(dialog).findByRole("button", { name: "Saving..." }),
  ).toBeDisabled();
  return dialog;
}

/** The id is typed with `padding` either side; the request and the toast carry it trimmed. */
async function createRoleNamed(
  user: UserEvent,
  roleid: string,
  padding = " ",
): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: "Create Role" }));
  const dialog = await screen.findByRole("dialog", { name: "Create Role" });
  await user.type(
    within(dialog).getByLabelText("Role ID"),
    `${padding}${roleid}${padding}`,
  );
  await user.click(within(dialog).getByRole("button", { name: "Save" }));
  expect(
    await within(dialog).findByRole("button", { name: "Saving..." }),
  ).toBeDisabled();
  return dialog;
}

const TOKEN_SUBJECT = "alice@pve!token01";
const GRANT = `Granting Operator on / to ${TOKEN_SUBJECT}`;

/** The Operator role on the root path, to an API token: the one subject typed in rather than picked. */
async function grantToToken(user: UserEvent): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: "Grant Access" }));
  const dialog = await screen.findByRole("dialog", { name: "Grant Access" });
  await user.click(within(dialog).getByRole("combobox", { name: "Role" }));
  await user.click(await screen.findByRole("option", { name: "Operator" }));
  await user.click(
    within(dialog).getByRole("combobox", { name: "Subject type" }),
  );
  await user.click(await screen.findByRole("option", { name: "API token" }));
  await user.type(within(dialog).getByLabelText("Token"), TOKEN_SUBJECT);
  await user.click(within(dialog).getByRole("button", { name: "Grant" }));
  expect(
    await within(dialog).findByRole("button", { name: "Granting..." }),
  ).toBeDisabled();
  return dialog;
}

const FIRST_ENTRY = "Revoke Operator on /";
const SECOND_ENTRY = "Revoke Auditor on /vms/100";
const REVOKE_FIRST = "Revoking Operator on / from alice@pve";
const REVOKE_SECOND = "Revoking Auditor on /vms/100 from operators";

async function revokeEntry(
  user: UserEvent,
  entry: string,
): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: entry }));
  const ask = await screen.findByRole("alertdialog", {
    name: "Revoke access?",
  });
  await user.click(within(ask).getByRole("button", { name: "Revoke" }));
  expect(
    await within(ask).findByRole("button", { name: "Revoking..." }),
  ).toBeDisabled();
  return ask;
}

// ── The dialogs that save through useSaveOutcome ────────────────────────────

// The group and grant dialogs are told apart by an open flag, and the section
// keeps their form between dialogs: what a dismissal leaves in it is the next
// dialog's text.
const GROUP_CREATE = {
  name: "group create",
  render: renderGroups,
  request: mockedPost,
  sent: [`${ACCESS}/groups`, { groupid: "reviewers", comment: "night shift" }],
  button: "Create",
  send: (user) => createGroupNamed(user, "reviewers", "night shift"),
  action: "Creating group reviewers",
  reopen: async (user) => {
    await user.click(screen.getByRole("button", { name: "Create Group" }));
    return screen.findByRole("dialog", { name: "Create Group" });
  },
  draft: {
    values: (dialog) => [
      within(dialog).getByLabelText<HTMLInputElement>("Group ID").value,
      within(dialog).getByLabelText<HTMLInputElement>("Comment").value,
    ],
    left: [" reviewers ", "night shift"],
    append: (user, dialog) =>
      user.type(within(dialog).getByLabelText("Group ID"), "x"),
  },
  silentAfter: SIGN_OUT,
} satisfies DialogSave;

const GRANT_ACCESS = {
  name: "permission grant",
  render: renderACL,
  request: mockedPut,
  sent: [
    `${ACCESS}/acl`,
    { path: "/", roles: "Operator", tokens: TOKEN_SUBJECT, propagate: true },
  ],
  button: "Grant",
  send: grantToToken,
  action: GRANT,
  reopen: async (user) => {
    await user.click(screen.getByRole("button", { name: "Grant Access" }));
    return screen.findByRole("dialog", { name: "Grant Access" });
  },
  draft: {
    values: (dialog) => [
      within(dialog).getByLabelText<HTMLInputElement>("Token").value,
    ],
    left: [TOKEN_SUBJECT],
    append: (user, dialog) =>
      user.type(within(dialog).getByLabelText("Token"), "x"),
  },
} satisfies DialogSave;

const GROUP_EDIT = {
  name: "group edit",
  render: renderGroups,
  request: mockedPut,
  sent: [`${ACCESS}/groups/operators`, { comment: "day shift edited" }],
  button: "Save",
  send: (user) => saveGroupComment(user, "operators"),
  action: "Saving group operators",
  dismissal: CANCEL_BUTTON,
  reopen: async (user) => {
    await user.click(screen.getByRole("button", { name: "Edit auditors" }));
    return screen.findByRole("dialog", { name: "Edit auditors" });
  },
  silentAfter: TAKEN_OVER,
} satisfies DialogSave;

// One editor for create and edit, told apart by its opening.
const ROLE_EDIT = {
  name: "role edit",
  render: renderRoles,
  request: mockedPut,
  sent: [`${ACCESS}/roles/Operator`, { privs: "VM.Audit,VM.PowerMgmt" }],
  button: "Save",
  send: (user) => saveRole(user, "Operator"),
  action: "Saving role Operator",
  dismissal: CANCEL_BUTTON,
  reopen: async (user) => {
    await user.click(
      within(rowOf("Auditor")).getByRole("button", { name: "Edit" }),
    );
    return screen.findByRole("dialog", { name: "Edit Auditor" });
  },
  silentAfter: SIGN_OUT,
} satisfies DialogSave;

const ROLE_CREATE = {
  name: "role create",
  render: renderRoles,
  request: mockedPost,
  sent: [`${ACCESS}/roles`, { roleid: "Reviewer", privs: "" }],
  button: "Save",
  send: (user) => createRoleNamed(user, "Reviewer"),
  action: "Creating role Reviewer",
  reopen: async (user) => {
    await user.click(screen.getByRole("button", { name: "Create Role" }));
    return screen.findByRole("dialog", { name: "Create Role" });
  },
} satisfies DialogSave;

describeDialogSaves([
  GROUP_CREATE,
  GROUP_EDIT,
  ROLE_EDIT,
  ROLE_CREATE,
  GRANT_ACCESS,
]);

// The comment is optional: a group created without one sends none, not an empty one.
it("sends no comment for a group created without one, and closes its dialog when it succeeds", async () => {
  const user = userEvent.setup();
  const held = heldOnce(mockedPost);
  renderGroups();

  const dialog = await createGroupNamed(user, "reviewers");
  held.resolve({});

  await waitFor(() => {
    expect(dialog).not.toBeInTheDocument();
  });
  expect(mockedPost.mock.calls).toEqual([
    [`${ACCESS}/groups`, { groupid: "reviewers" }],
  ]);
  expectNoToast();
});

describe.each([GROUP_CREATE, GRANT_ACCESS])(
  "a $name that succeeds after its dialog was dismissed and none is open",
  (s) => {
    it("puts away the form the dismissal left, so the next dialog starts blank", async () => {
      const user = userEvent.setup();
      const held = heldOnce(s.request);
      const { qc } = s.render();

      await dismiss(user, await s.send(user));
      held.resolve({});
      await waitForSuccess(qc);

      expect(s.draft.values(await s.reopen(user))).toEqual(
        s.draft.left.map(() => ""),
      );
      expectNoToast();
    });
  },
);

// ── A dialog replaced by another from the keyboard ──────────────────────────

/** Sends the first save and Tabs to `opener`, which opens its dialog in place of the first's. */
async function replacedBy(
  user: UserEvent,
  send: (user: UserEvent) => Promise<HTMLElement>,
  opener: () => HTMLElement,
  title: string,
  role: "dialog" | "alertdialog" = "dialog",
): Promise<HTMLElement> {
  await send(user);
  await tabTo(user, opener());
  await user.keyboard("{Enter}");
  return screen.findByRole(role, { name: title });
}

const roleEditButton = (roleid: string) => () =>
  within(rowOf(roleid)).getByRole("button", { name: "Edit", hidden: true });

// Each row says what opens the second dialog: another role's Edit button, Create
// Role, or the Edit button of the role that a Create Role editor names.
const REPLACEMENTS: Replacement[] = [
  {
    name: "group edit",
    render: renderGroups,
    request: mockedPut,
    action: "Saving group operators",
    replace: (user) =>
      replacedBy(
        user,
        (u) => saveGroupComment(u, "operators"),
        () =>
          screen.getByRole("button", { name: "Edit auditors", hidden: true }),
        "Edit auditors",
      ),
    // Its own form and its own Save, not the first's: a request that was not its own holds neither.
    own: (second) => {
      expect(within(second).getByLabelText("Comment")).toHaveValue("read only");
      expect(
        within(second).getByRole("button", { name: "Save" }),
      ).toBeEnabled();
    },
  },
  {
    name: "role edit by Edit",
    render: renderRoles,
    request: mockedPut,
    action: "Saving role Operator",
    replace: (user) =>
      replacedBy(
        user,
        (u) => saveRole(u, "Operator"),
        roleEditButton("Auditor"),
        "Edit Auditor",
      ),
    own: (second) => {
      expect(
        within(second).getByRole("button", { name: "Save" }),
      ).toBeEnabled();
    },
  },
  {
    name: "role edit by Create Role",
    render: renderRoles,
    request: mockedPut,
    action: "Saving role Operator",
    replace: (user) =>
      replacedBy(
        user,
        (u) => saveRole(u, "Operator"),
        () => screen.getByRole("button", { name: "Create Role", hidden: true }),
        "Create Role",
      ),
  },
  {
    // A Create Role editor is not a role's editor, whatever is typed into it:
    // the id of a role that exists is a slip the server answers, and that role's
    // Edit button opens its editor over the create's.
    name: "role create by Edit",
    render: renderRoles,
    request: mockedPost,
    action: "Creating role Operator",
    replace: (user) =>
      replacedBy(
        user,
        (u) => createRoleNamed(u, "Operator", ""),
        roleEditButton("Operator"),
        "Edit Operator",
      ),
    // The role as stored, not the create's blank form.
    own: (second) => {
      expect(
        within(second).getByRole("checkbox", { name: "VM.PowerMgmt" }),
      ).toBeChecked();
    },
  },
  {
    name: "permission revoke",
    render: renderACL,
    request: mockedPut,
    action: REVOKE_FIRST,
    replace: async (user) => {
      const ask = await replacedBy(
        user,
        (u) => revokeEntry(u, FIRST_ENTRY),
        () => screen.getByRole("button", { name: SECOND_ENTRY, hidden: true }),
        "Revoke access?",
        "alertdialog",
      );
      // The second entry's confirmation names the group, where the first's names the user.
      await waitFor(() => {
        expect(within(ask).getByText("operators")).toBeInTheDocument();
      });
      expect(within(ask).queryByText("alice@pve")).toBeNull();
      return ask;
    },
    own: (second) => {
      expect(within(second).getByText("operators")).toBeInTheDocument();
    },
  },
];

describeReplacements(REPLACEMENTS);

// ── Role editors: what an opening is, and what it is not ────────────────────

// Opening the editor that is already up is not opening another one: Create Role
// over a Create Role editor, and the Edit button of the role being edited, leave
// it as it is, draft and all, so the save that is out is the live editor's own.
// A second editor, seeded from the list as it was while that save was out,
// would hold the old privileges once the save had gone through and write them
// back.
describe("a role save whose own editor is opened again from the keyboard", () => {
  async function createAgain(user: UserEvent) {
    const editor = await createRoleNamed(user, "Reviewer");
    await tabTo(
      user,
      screen.getByRole("button", { name: "Create Role", hidden: true }),
    );
    await user.keyboard("{Enter}");
    await flushInAct();
    return editor;
  }

  /** Saves the Operator role, a privilege ticked off, and presses its Edit again over it. */
  async function editAgain(user: UserEvent) {
    await screen.findByText("Operator");
    await user.click(
      within(rowOf("Operator")).getByRole("button", { name: "Edit" }),
    );
    const editor = await screen.findByRole("dialog", { name: "Edit Operator" });
    await user.click(
      within(editor).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    );
    await user.click(within(editor).getByRole("button", { name: "Save" }));
    await within(editor).findByRole("button", { name: "Saving..." });
    await tabTo(user, roleEditButton("Operator")());
    await user.keyboard("{Enter}");
    await flushInAct();
    return editor;
  }

  it("shows the failure of a create in the editor that is still up, draft and all, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPost);
    renderRoles();

    const editor = await createAgain(user);
    held.reject(denied());

    expect(await within(editor).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expectNoToast();
    expect(editor).toHaveAttribute("data-state", "open");
    expect(within(editor).getByLabelText("Role ID")).toHaveValue(" Reviewer ");
    expect(within(editor).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("closes the editor of a create when its save succeeds", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPost);
    const { qc } = renderRoles();

    const editor = await createAgain(user);
    held.resolve({});
    await waitForSuccess(qc);

    await waitFor(() => {
      expect(editor).not.toBeInTheDocument();
    });
    expect(mockedPost.mock.calls).toEqual([
      [`${ACCESS}/roles`, { roleid: "Reviewer", privs: "" }],
    ]);
    expectNoToast();
  });

  it("shows the failure of an edit in the editor that is still up, with the draft and not the role as stored", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    renderRoles();

    const editor = await editAgain(user);
    held.reject(denied());

    expect(await within(editor).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expectNoToast();
    expect(editor).toHaveAttribute("data-state", "open");
    expect(
      within(editor).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    ).not.toBeChecked();
    expect(within(editor).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("closes the editor of an edit when its save succeeds, so that no second one is left to write the old privileges back", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    const { qc } = renderRoles();

    const editor = await editAgain(user);
    // The server holds the draft once the save has gone through, and the list is read again.
    operatorPrivs = "VM.Audit";
    held.resolve({});
    await waitForSuccess(qc);

    await waitFor(() => {
      expect(editor).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(
        within(rowOf("Operator")).getByText("1 privilege"),
      ).toBeInTheDocument();
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(mockedPut.mock.calls).toEqual([
      [`${ACCESS}/roles/Operator`, { privs: "VM.Audit" }],
    ]);
    expectNoToast();
  });
});

// What an editor is told apart by is its opening, not what is in it: the id
// typed into a Create Role editor and the privileges ticked in either kind
// change with every key and click while the editor's own save is out.
describe("a role save whose editor is edited while its request is out", () => {
  it("shows the failure of a create in the editor, with no toast, when its Role ID is edited", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPost);
    renderRoles();

    const dialog = await createRoleNamed(user, "Reviewer");
    await user.type(within(dialog).getByLabelText("Role ID"), "x");
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expectNoToast();
  });

  it("closes the editor of an edit when it succeeds, with no toast, when a privilege is ticked", async () => {
    const user = userEvent.setup();
    const held = heldOnce(mockedPut);
    renderRoles();

    const dialog = await saveRole(user, "Operator");
    await user.click(
      within(dialog).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    );
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expectNoToast();
  });
});

// ── Permission revoke ───────────────────────────────────────────────────────

// A revoke reports a failure in the section's banner as well, but only while
// its confirmation is still open: the confirmation closes on a failure, which
// uncovers the banner. For one that was dismissed or replaced, the banner names
// no entry and sits behind whichever dialog is open next.
describe("a permission revoke that settles", () => {
  it.each([
    ["fails", FIRST_ENTRY],
    ["succeeds", SECOND_ENTRY],
  ])(
    "closes its confirmation when it %s, and a failure goes to the banner, not a toast",
    async (outcome, entry) => {
      const user = userEvent.setup();
      const held = heldOnce(mockedPut);
      const { qc } = renderACL();

      const ask = await revokeEntry(user, entry);
      if (outcome === "fails") {
        held.reject(denied());
        expect(await screen.findByText(DENIED)).toBeInTheDocument();
      } else {
        held.resolve({});
        await waitForSuccess(qc);
      }

      await waitFor(() => {
        expect(ask).not.toBeInTheDocument();
      });
      await flushInAct();
      expectNoToast();
      expect(mockedPut.mock.calls).toEqual([
        [
          `${ACCESS}/acl`,
          entry === FIRST_ENTRY
            ? { path: "/", roles: "Operator", users: "alice@pve", delete: true }
            : {
                path: "/vms/100",
                roles: "Auditor",
                groups: "operators",
                delete: true,
              },
        ],
      ]);
    },
  );

  /** Revokes the second entry with the request held, dismisses its confirmation, and opens the first's. */
  async function dismissedForAnother(user: UserEvent) {
    const held = heldOnce(mockedPut);
    const view = renderACL();
    await dismiss(user, await revokeEntry(user, SECOND_ENTRY));
    await user.click(screen.getByRole("button", { name: FIRST_ENTRY }));
    const second = await screen.findByRole("alertdialog", {
      name: "Revoke access?",
    });
    return { ...view, held, second };
  }

  it("toasts a failure once, naming the entry, when another entry's confirmation is open, and neither closes that one nor puts anything in the banner", async () => {
    const user = userEvent.setup();
    const { held, second } = await dismissedForAnother(user);
    held.reject(denied());

    await expectOneToast(failedToast(REVOKE_SECOND));
    expect(second).toHaveAttribute("data-state", "open");
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("does not close the confirmation opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const { held, qc, second } = await dismissedForAnother(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByText("alice@pve")).toBeInTheDocument();
    expectNoToast();
  });

  it("says nothing of a failure that comes after a sign-out", async () => {
    const user = userEvent.setup();
    const { held } = await dismissedForAnother(user);
    SIGN_OUT[1]();
    held.reject(denied());

    await expectSilence();
  });
});

// ── Deletes: not through useSaveOutcome ─────────────────────────────────────

// The confirmations keep the app's global toast (their section has nowhere to
// show a delete's error) and close when their request settles, whatever the
// outcome. They are not held while the request is out, so Tab walks out of one to
// the Delete buttons behind it and Enter on another's opens that one's
// confirmation in place of the first's. The first delete settling must not close it.
const DELETES = [
  {
    name: "group",
    render: renderGroups,
    first: "operators",
    second: "auditors",
    title: (id: string) => `Delete group ${id}?`,
    confirm: "Delete Group",
    url: (id: string) => `${ACCESS}/groups/${id}`,
  },
  {
    name: "role",
    render: renderRoles,
    first: "Operator",
    second: "Auditor",
    title: (id: string) => `Delete role ${id}?`,
    confirm: "Delete Role",
    url: (id: string) => `${ACCESS}/roles/${id}`,
  },
];

describe.each(DELETES)(
  "a $name delete whose confirmation is replaced by another's from the keyboard",
  (d) => {
    it.each(["fails", "succeeds"])(
      "leaves the confirmation that replaced it open when it %s, and closes each on its own answer",
      async (outcome) => {
        const failing = outcome === "fails";
        const user = userEvent.setup();
        const held = heldOnce(mockedDelete);
        const { qc } = d.render();

        await user.click(
          await screen.findByRole("button", { name: `Delete ${d.first}` }),
        );
        const ask = await screen.findByRole("alertdialog", {
          name: d.title(d.first),
        });
        await user.click(within(ask).getByRole("button", { name: d.confirm }));
        await within(ask).findByRole("button", { name: "Deleting..." });
        await tabTo(
          user,
          screen.getByRole("button", {
            name: `Delete ${d.second}`,
            hidden: true,
          }),
        );
        await user.keyboard("{Enter}");
        const second = await screen.findByRole("alertdialog", {
          name: d.title(d.second),
        });

        if (failing) {
          held.reject(denied());
          await expectOneToast(DENIED);
        } else {
          held.resolve(undefined);
          await waitForSuccess(qc);
          expectNoToast();
        }
        expect(second).toBeInTheDocument();
        expect(second).toHaveAttribute("data-state", "open");

        // The second confirmation goes on to be answered, and closed by it.
        if (failing) mockedDelete.mockRejectedValueOnce(denied());
        await user.click(
          within(second).getByRole("button", { name: d.confirm }),
        );
        await waitFor(() => {
          expect(second).not.toBeInTheDocument();
        });
        expect(mockedDelete.mock.calls).toEqual([
          [d.url(d.first)],
          [d.url(d.second)],
        ]);
        expect(toastsRaised()).toHaveLength(failing ? 2 : 0);
      },
    );
  },
);
