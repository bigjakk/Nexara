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
import { AccessACLSection } from "./AccessACLSection";
import { AccessGroupsSection } from "./AccessGroupsSection";
import { AccessRolesSection } from "./AccessRolesSection";

/**
 * The saves of the group, role and permission sections that opt out of the
 * global error toast: creating and editing a group, creating and editing a
 * role, and granting and revoking a permission. Each is reported by whatever
 * sent it while that is on screen, and once it is gone by a toast that names the
 * object, unless the session has ended too (hooks/useSaveOutcome.ts).
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

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);
const mockedToastError = vi.mocked(toast.error);

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

type UserEvent = ReturnType<typeof userEvent.setup>;

/** What each way of getting rid of a dialog does to it. */
const DISMISSALS: [
  name: string,
  dismiss: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
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
];

const PAGE_LEFT: [
  name: string,
  leave: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
] = [
  "the page being left",
  () => {
    cleanup();
    return Promise.resolve();
  },
];

/** The toast a failed save leaves once what sent it is gone. */
function failedToast(action: string, message = DENIED): string {
  return `${action} failed: ${message}`;
}

/** Waits for a toast, then for anything else a second one could be late with. */
async function expectOneToast(message: string): Promise<void> {
  await waitFor(() => {
    expect(mockedToastError).toHaveBeenCalledWith(message);
  });
  await flushInAct();
  expect(mockedToastError).toHaveBeenCalledTimes(1);
}

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

// ── Groups ──────────────────────────────────────────────────────────────

function renderGroups() {
  return renderOnAppClient(
    <AccessGroupsSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

/**
 * Opens the Create Group dialog, types the id (and a comment, if there is one)
 * and presses Create, and waits for the request to be out. The id is typed with
 * a space either side, as a pasted one often has: the request and the toast
 * carry it trimmed, and the dialog keeps it as typed.
 */
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

describe("a group create that settles", () => {
  it("control: closes its dialog when it succeeds while the dialog is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderGroups();

    const dialog = await createGroupNamed(user, "reviewers");
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedPost.mock.calls).toEqual([
      [`${ACCESS}/groups`, { groupid: "reviewers" }],
    ]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows a failure in its dialog while the dialog is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderGroups();

    const dialog = await createGroupNamed(user, "reviewers");
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each([...DISMISSALS, PAGE_LEFT])(
    "toasts a failure that comes after %s, once, naming the group",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderGroups();

      const dialog = await createGroupNamed(user, "reviewers");
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(failedToast("Creating group reviewers"));
    },
  );

  it("toasts a failure that comes after the dialog was dismissed and opened again, and shows nothing in the new one", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderGroups();

    const first = await createGroupNamed(user, "reviewers");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Create Group" }));
    const second = await screen.findByRole("dialog", { name: "Create Group" });
    held.reject(denied());

    await expectOneToast(failedToast("Creating group reviewers"));
    expect(second).toBeInTheDocument();
    expect(within(second).queryByText(DENIED)).toBeNull();
  });

  it("does not close or clear the dialog opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    const { qc } = renderGroups();

    const first = await createGroupNamed(user, "reviewers", "night shift");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Create Group" }));
    const second = await screen.findByRole("dialog", { name: "Create Group" });
    // The section keeps what was typed across a dismissal, so start over with
    // the id; the comment stays as the first dialog left it, and is the new
    // dialog's text now.
    await user.clear(within(second).getByLabelText("Group ID"));
    await user.type(within(second).getByLabelText("Group ID"), "admins");
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByLabelText("Group ID")).toHaveValue("admins");
    expect(within(second).getByLabelText("Comment")).toHaveValue("night shift");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  // The section keeps what was typed when a dialog is dismissed, and what it
  // keeps is the id of a group that now exists: left there, the next Create
  // would offer it again.
  it("control: keeps what was typed when the dialog is dismissed and nothing has gone through", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderGroups();

    const first = await createGroupNamed(user, "reviewers", "night shift");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Create Group" }));
    const second = await screen.findByRole("dialog", { name: "Create Group" });

    // As typed, with the stray spaces it was typed with.
    expect(within(second).getByLabelText("Group ID")).toHaveValue(
      " reviewers ",
    );
    expect(within(second).getByLabelText("Comment")).toHaveValue("night shift");
  });

  it("puts away what was typed when it succeeds after the dialog was dismissed and none is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    const { qc } = renderGroups();

    const first = await createGroupNamed(user, "reviewers", "night shift");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    held.resolve({});
    await waitForSuccess(qc);
    await user.click(screen.getByRole("button", { name: "Create Group" }));
    const second = await screen.findByRole("dialog", { name: "Create Group" });

    expect(within(second).getByLabelText("Group ID")).toHaveValue("");
    expect(within(second).getByLabelText("Comment")).toHaveValue("");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderGroups();

      await createGroupNamed(user, "reviewers");
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

/** Opens the Edit dialog of a group, edits its comment and presses Save, and waits for the request to be out. */
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

describe("a group edit that settles", () => {
  const CANCEL: [
    name: string,
    cancel: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
  ] = [
    "Cancel",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    },
  ];

  it("control: closes its dialog when it succeeds while the dialog is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderGroups();

    const dialog = await saveGroupComment(user, "operators");
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedPut.mock.calls).toEqual([
      [`${ACCESS}/groups/operators`, { comment: "day shift edited" }],
    ]);
  });

  it("control: shows a failure in its dialog while the dialog is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderGroups();

    const dialog = await saveGroupComment(user, "operators");
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each([...DISMISSALS, CANCEL, PAGE_LEFT])(
    "toasts a failure that comes after %s, once, naming the group",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderGroups();

      const dialog = await saveGroupComment(user, "operators");
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(failedToast("Saving group operators"));
    },
  );

  /**
   * Saves the first group with the request held, dismisses its dialog, and
   * opens the Edit dialog of the other group in its place.
   */
  async function dismissForAnother(user: UserEvent) {
    const first = await saveGroupComment(user, "operators");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Edit auditors" }));
    return screen.findByRole("dialog", { name: "Edit auditors" });
  }

  it("does not close the edit dialog opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderGroups();

    const second = await dismissForAnother(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("names the group in the toast when it fails over another group's open dialog, and leaves that dialog alone", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderGroups();

    const second = await dismissForAnother(user);
    held.reject(denied());

    await expectOneToast(failedToast("Saving group operators"));
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderGroups();

      await saveGroupComment(user, "operators");
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

// Neither dialog is held while its request is out, and the button that sent it
// is disabled by the request, so focus is lost and Tab walks out of the modal to
// the controls behind it, which a pointer cannot reach. Enter on one of them
// puts another group's dialog where the first was, with nothing closed between
// the two: a dialog told apart only by whether one is open, or by an instance
// that is not keyed on its group, would hand the second the first's outcome.
describe("a group edit whose dialog is replaced by another group's from the keyboard", () => {
  /** Saves the first group with the request held, and opens the other's dialog over it. */
  async function replaceWithAuditors(user: UserEvent) {
    await saveGroupComment(user, "operators");
    await tabTo(
      user,
      screen.getByRole("button", { name: "Edit auditors", hidden: true }),
    );
    await user.keyboard("{Enter}");
    return screen.findByRole("dialog", { name: "Edit auditors" });
  }

  it("toasts the failure, once, naming the group, and shows nothing of it in the other group's dialog", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderGroups();

    const second = await replaceWithAuditors(user);
    held.reject(denied());

    await expectOneToast(failedToast("Saving group operators"));
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
    // The other group's own form, not the first's: its comment, and a Save that
    // is not held for a request that was not its own.
    expect(within(second).getByLabelText("Comment")).toHaveValue("read only");
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("does not close the other group's dialog when it succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderGroups();

    const second = await replaceWithAuditors(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByLabelText("Comment")).toHaveValue("read only");
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

// The delete confirmations keep the app's global toast on purpose (their section
// has nowhere to show a delete's error) and close when their request settles,
// whatever the outcome. They are not held while the request is out, and the
// button that sent it is disabled by it, so Tab walks out of the modal to the
// Delete buttons behind it, which a pointer cannot reach, and Enter on another's
// opens that one's confirmation in place of the first's, with nothing closed
// between the two. The first delete settling must not close it.
describe("a group delete whose confirmation is replaced by another group's from the keyboard", () => {
  /** Deletes the first group with the request held, and opens the other's confirmation over it. */
  async function replaceWithAuditors(user: UserEvent) {
    await user.click(
      await screen.findByRole("button", { name: "Delete operators" }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: "Delete group operators?",
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Group" }));
    await within(ask).findByRole("button", { name: "Deleting..." });
    await tabTo(
      user,
      screen.getByRole("button", { name: "Delete auditors", hidden: true }),
    );
    await user.keyboard("{Enter}");
    return screen.findByRole("alertdialog", { name: "Delete group auditors?" });
  }

  it("control: closes its confirmation when its own delete succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderGroups();

    await user.click(
      await screen.findByRole("button", { name: "Delete operators" }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: "Delete group operators?",
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Group" }));
    await within(ask).findByRole("button", { name: "Deleting..." });
    held.resolve(undefined);
    await waitForSuccess(qc);

    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
    expect(mockedDelete.mock.calls).toEqual([[`${ACCESS}/groups/operators`]]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: closes its confirmation when its own delete fails, and toasts the failure once", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderGroups();

    await user.click(
      await screen.findByRole("button", { name: "Delete operators" }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: "Delete group operators?",
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Group" }));
    await within(ask).findByRole("button", { name: "Deleting..." });
    held.reject(denied());

    await expectOneToast(DENIED);
    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
  });

  it("reports the failure once, in the global toast, and does not close the other group's confirmation", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderGroups();

    const second = await replaceWithAuditors(user);
    held.reject(denied());

    await expectOneToast(DENIED);
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(
      screen.getByRole("alertdialog", { name: "Delete group auditors?" }),
    ).toBeInTheDocument();
  });

  it("does not close the other group's confirmation when the first delete succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderGroups();

    const second = await replaceWithAuditors(user);
    held.resolve(undefined);
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(
      screen.getByRole("alertdialog", { name: "Delete group auditors?" }),
    ).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

// ── Roles ───────────────────────────────────────────────────────────────

function renderRoles() {
  return renderOnAppClient(
    <AccessRolesSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

/** Opens the editor of a role and presses Save, and waits for the request to be out. */
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

/**
 * Opens the Create Role editor, types the id and presses Save, and waits for
 * the request to be out. The id is typed with `padding` either side, a space
 * unless said otherwise, as a pasted one often has: the request and the toast
 * carry it trimmed.
 */
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

describe("a role save that settles", () => {
  const CANCEL: [
    name: string,
    cancel: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
  ] = [
    "Cancel",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    },
  ];

  it("control: closes the editor when an edit succeeds while the editor is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderRoles();

    const dialog = await saveRole(user, "Operator");
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedPut.mock.calls).toEqual([
      [`${ACCESS}/roles/Operator`, { privs: "VM.Audit,VM.PowerMgmt" }],
    ]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows the failure of an edit in the editor while the editor is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderRoles();

    const dialog = await saveRole(user, "Operator");
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each([...DISMISSALS, CANCEL, PAGE_LEFT])(
    "toasts the failure of an edit that comes after %s, once, naming the role",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderRoles();

      const dialog = await saveRole(user, "Operator");
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(failedToast("Saving role Operator"));
    },
  );

  it.each([...DISMISSALS, CANCEL, PAGE_LEFT])(
    "toasts the failure of a create that comes after %s, once, naming the role",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderRoles();

      const dialog = await createRoleNamed(user, "Reviewer");
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(failedToast("Creating role Reviewer"));
      expect(mockedPost.mock.calls).toEqual([
        [`${ACCESS}/roles`, { roleid: "Reviewer", privs: "" }],
      ]);
    },
  );

  /**
   * Saves a role with the request held, dismisses its editor, and opens the
   * editor of another role in its place: the same component hosts both.
   */
  async function dismissForAnother(user: UserEvent) {
    const first = await saveRole(user, "Operator");
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(
      within(rowOf("Auditor")).getByRole("button", { name: "Edit" }),
    );
    return screen.findByRole("dialog", { name: "Edit Auditor" });
  }

  it("never shows the failure of an older save in the editor opened after it", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderRoles();

    const second = await dismissForAnother(user);
    held.reject(denied());

    await expectOneToast(failedToast("Saving role Operator"));
    // The editor that is open is the other role's, and this was not its save.
    expect(second).toBeInTheDocument();
    expect(within(second).queryByText(DENIED)).toBeNull();
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("does not close the editor opened after an older save when that succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderRoles();

    const second = await dismissForAnother(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("shows the failure of the newer editor's own save, whatever happened to the older one", async () => {
    // The control for the two above: the newer editor reports what it sent.
    const user = userEvent.setup();
    const older = deferred<unknown>();
    const newer = deferred<unknown>();
    mockedPut
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    renderRoles();

    const second = await dismissForAnother(user);
    older.reject(denied());
    await expectOneToast(failedToast("Saving role Operator"));
    // The section's one mutation per kind of save still counts the older save
    // as pending until it settles, which holds the newer editor's Save.
    await user.click(
      await within(second).findByRole("button", { name: "Save" }),
    );
    await within(second).findByRole("button", { name: "Saving..." });
    newer.reject(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Permission check failed",
      }),
    );

    expect(
      await within(second).findByText("Permission check failed"),
    ).toBeInTheDocument();
    // Still the one toast, the older save's.
    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderRoles();

      await saveRole(user, "Operator");
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

// The editor is not held while its request is out, and the button that sent it
// is disabled by the request, so focus is lost and Tab walks out of the modal to
// the Edit buttons and Create Role behind it, which a pointer cannot reach. Enter
// on one of them sets another editor over the first, with no closed state
// between the two, so an editor told apart only by whether one is open would
// give the second the first's outcome.
describe("a role save whose editor is replaced by another from the keyboard", () => {
  const REPLACEMENTS: [
    name: string,
    opener: () => HTMLElement,
    title: string,
  ][] = [
    [
      "another role's Edit button",
      () =>
        within(rowOf("Auditor")).getByRole("button", {
          name: "Edit",
          hidden: true,
        }),
      "Edit Auditor",
    ],
    [
      "Create Role",
      () => screen.getByRole("button", { name: "Create Role", hidden: true }),
      "Create Role",
    ],
  ];

  /** Saves the Operator role with the request held, and opens another editor over it. */
  async function replaceBy(
    user: UserEvent,
    opener: () => HTMLElement,
    title: string,
  ) {
    await saveRole(user, "Operator");
    await tabTo(user, opener());
    await user.keyboard("{Enter}");
    return screen.findByRole("dialog", { name: title });
  }

  it.each(REPLACEMENTS)(
    "toasts the failure, once, naming the role, and shows nothing of it in the editor that replaced it by %s",
    async (_, opener, title) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderRoles();

      const second = await replaceBy(user, opener, title);
      held.reject(denied());

      await expectOneToast(failedToast("Saving role Operator"));
      expect(second).toBeInTheDocument();
      expect(second).toHaveAttribute("data-state", "open");
      expect(within(second).queryByText(DENIED)).toBeNull();
    },
  );

  it.each(REPLACEMENTS)(
    "does not close the editor that replaced it by %s when it succeeds, and toasts nothing",
    async (_, opener, title) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      const { qc } = renderRoles();

      const second = await replaceBy(user, opener, title);
      held.resolve({});
      await waitForSuccess(qc);

      expect(second).toBeInTheDocument();
      expect(second).toHaveAttribute("data-state", "open");
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

// Opening the editor that is already up is not opening another one. Create Role
// pressed over a Create Role editor, and the Edit button of the role being
// edited, leave the editor as it is, draft and all, so the save that is out is the
// live editor's own: its failure shows there, and its success closes it. A second
// editor, seeded from the list as it was while that save was out, would still hold
// the old privileges once the save had gone through, and saving it would write
// them back.
describe("a role save whose own editor is opened again from the keyboard", () => {
  /** Saves a new role "Reviewer" with the request held, and presses Create Role again over it. */
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

  /** Saves the Operator role, a privilege ticked off, with the request held, and presses its Edit again over it. */
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
    await tabTo(
      user,
      within(rowOf("Operator")).getByRole("button", {
        name: "Edit",
        hidden: true,
      }),
    );
    await user.keyboard("{Enter}");
    await flushInAct();
    return editor;
  }

  it("shows the failure of a create in the editor that is still up, draft and all, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderRoles();

    const editor = await createAgain(user);
    held.reject(denied());

    expect(await within(editor).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(editor).toHaveAttribute("data-state", "open");
    // The draft of the editor that sent the save, not a blank form: what was
    // typed stays, as typed.
    expect(within(editor).getByLabelText("Role ID")).toHaveValue(" Reviewer ");
    expect(within(editor).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("closes the editor of a create when its save succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
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
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("shows the failure of an edit in the editor that is still up, draft and all, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderRoles();

    const editor = await editAgain(user);
    held.reject(denied());

    expect(await within(editor).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(editor).toHaveAttribute("data-state", "open");
    // The draft, not the role as stored: the privilege that was ticked off stays
    // off.
    expect(
      within(editor).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    ).not.toBeChecked();
    expect(within(editor).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("closes the editor of an edit when its save succeeds, so that no second one is left to write the old privileges back", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderRoles();

    const editor = await editAgain(user);
    // The server holds the draft once the save has gone through, and the list is
    // read again.
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
    // The one save there has been is the draft.
    expect(mockedPut.mock.calls).toEqual([
      [`${ACCESS}/roles/Operator`, { privs: "VM.Audit" }],
    ]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

// A Create Role editor is not a role's editor, whatever has been typed into it:
// the id of a role that exists is a slip the server answers, and the Edit button
// of that role opens that role's editor over the create's, with the create's save
// still out.
describe("a role create whose editor is replaced from the keyboard by the Edit button of the role it names", () => {
  /**
   * Saves a new role "Operator", which exists, with the request held, and opens
   * Operator's own editor over it. The id is typed exactly as the role has it,
   * with no padding, so that the create editor holds the id of the role whose Edit
   * is pressed.
   */
  async function replaceByEdit(user: UserEvent) {
    await createRoleNamed(user, "Operator", "");
    await tabTo(
      user,
      within(rowOf("Operator")).getByRole("button", {
        name: "Edit",
        hidden: true,
      }),
    );
    await user.keyboard("{Enter}");
    return screen.findByRole("dialog", { name: "Edit Operator" });
  }

  it("toasts the failure, once, naming the role, and shows nothing of it in the role's own editor", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderRoles();

    const second = await replaceByEdit(user);
    held.reject(denied());

    await expectOneToast(failedToast("Creating role Operator"));
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
    // The role as stored, not the create's blank form.
    expect(
      within(second).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    ).toBeChecked();
  });

  it("does not close the role's own editor when the create succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    const { qc } = renderRoles();

    const second = await replaceByEdit(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(
      within(second).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    ).toBeChecked();
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

// What an editor is told apart by is its opening, not what is in it: the id
// typed into a Create Role editor and the privileges ticked in either kind
// change with every key and click, also while the editor's own save is out, and
// the save is still the editor's.
describe("a role save whose editor is edited while its request is out", () => {
  it("shows the failure of a create in the editor, with no toast, when its Role ID is edited", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderRoles();

    const dialog = await createRoleNamed(user, "Reviewer");
    await user.type(within(dialog).getByLabelText("Role ID"), "x");
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("closes the editor of a create when it succeeds, with no toast, when its Role ID is edited", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderRoles();

    const dialog = await createRoleNamed(user, "Reviewer");
    await user.type(within(dialog).getByLabelText("Role ID"), "x");
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("shows the failure of an edit in the editor, with no toast, when a privilege is ticked", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderRoles();

    const dialog = await saveRole(user, "Operator");
    await user.click(
      within(dialog).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    );
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("closes the editor of an edit when it succeeds, with no toast, when a privilege is ticked", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderRoles();

    const dialog = await saveRole(user, "Operator");
    await user.click(
      within(dialog).getByRole("checkbox", { name: "VM.PowerMgmt" }),
    );
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

// The same for the role delete confirmation: kept on the global toast, closed
// when its request settles, and not held while it is out.
describe("a role delete whose confirmation is replaced by another role's from the keyboard", () => {
  /** Deletes the Operator role with the request held, and opens the Auditor's confirmation over it. */
  async function replaceWithAuditor(user: UserEvent) {
    await screen.findByText("Operator");
    await user.click(
      await screen.findByRole("button", { name: "Delete Operator" }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: "Delete role Operator?",
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Role" }));
    await within(ask).findByRole("button", { name: "Deleting..." });
    await tabTo(
      user,
      screen.getByRole("button", { name: "Delete Auditor", hidden: true }),
    );
    await user.keyboard("{Enter}");
    return screen.findByRole("alertdialog", { name: "Delete role Auditor?" });
  }

  it("control: closes its confirmation when its own delete succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderRoles();

    await user.click(
      await screen.findByRole("button", { name: "Delete Operator" }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: "Delete role Operator?",
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Role" }));
    await within(ask).findByRole("button", { name: "Deleting..." });
    held.resolve(undefined);
    await waitForSuccess(qc);

    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
    expect(mockedDelete.mock.calls).toEqual([[`${ACCESS}/roles/Operator`]]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: closes its confirmation when its own delete fails, and toasts the failure once", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderRoles();

    await user.click(
      await screen.findByRole("button", { name: "Delete Operator" }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: "Delete role Operator?",
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Role" }));
    await within(ask).findByRole("button", { name: "Deleting..." });
    held.reject(denied());

    await expectOneToast(DENIED);
    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
  });

  it("reports the failure once, in the global toast, and does not close the other role's confirmation", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    renderRoles();

    const second = await replaceWithAuditor(user);
    held.reject(denied());

    await expectOneToast(DENIED);
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(
      screen.getByRole("alertdialog", { name: "Delete role Auditor?" }),
    ).toBeInTheDocument();
  });

  it("does not close the other role's confirmation when the first delete succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(held.promise);
    const { qc } = renderRoles();

    const second = await replaceWithAuditor(user);
    held.resolve(undefined);
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(
      screen.getByRole("alertdialog", { name: "Delete role Auditor?" }),
    ).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

// ── Permissions ─────────────────────────────────────────────────────────

function renderACL() {
  return renderOnAppClient(
    <AccessACLSection clusterId={CLUSTER} capabilities={capabilities} />,
  );
}

const TOKEN_SUBJECT = "alice@pve!token01";
const GRANT = `Granting Operator on / to ${TOKEN_SUBJECT}`;

/**
 * Opens the Grant Access dialog and fills it in — the Operator role, on the
 * root path, to an API token, which is the one subject typed in rather than
 * picked — presses Grant, and waits for the request to be out.
 */
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

describe("a permission grant that settles", () => {
  it("control: closes its dialog when it succeeds while the dialog is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const dialog = await grantToToken(user);
    held.resolve({});

    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    expect(mockedPut.mock.calls).toEqual([
      [
        `${ACCESS}/acl`,
        {
          path: "/",
          roles: "Operator",
          tokens: TOKEN_SUBJECT,
          propagate: true,
        },
      ],
    ]);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows a failure in its dialog while the dialog is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const dialog = await grantToToken(user);
    held.reject(denied());

    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each([...DISMISSALS, PAGE_LEFT])(
    "toasts a failure that comes after %s, once, naming the grant",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderACL();

      const dialog = await grantToToken(user);
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      held.reject(denied());

      await expectOneToast(failedToast(GRANT));
    },
  );

  it("toasts a failure that comes after the dialog was dismissed and opened again, and shows nothing in the new one", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const first = await grantToToken(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Grant Access" }));
    const second = await screen.findByRole("dialog", { name: "Grant Access" });
    held.reject(denied());

    await expectOneToast(failedToast(GRANT));
    expect(second).toBeInTheDocument();
    expect(within(second).queryByText(DENIED)).toBeNull();
  });

  it("does not close or clear the dialog opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderACL();

    const first = await grantToToken(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Grant Access" }));
    const second = await screen.findByRole("dialog", { name: "Grant Access" });
    // The section keeps what was filled in across a dismissal; add to it.
    await user.type(within(second).getByLabelText("Token"), "x");
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByLabelText("Token")).toHaveValue(
      `${TOKEN_SUBJECT}x`,
    );
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  // The section keeps what was filled in when a dialog is dismissed, and what it
  // keeps is the subject of a grant that now exists: left there, the next Grant
  // would offer it again.
  it("control: keeps the subject when the dialog is dismissed and nothing has gone through", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const first = await grantToToken(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Grant Access" }));
    const second = await screen.findByRole("dialog", { name: "Grant Access" });

    expect(within(second).getByLabelText("Token")).toHaveValue(TOKEN_SUBJECT);
  });

  it("puts away the subject when it succeeds after the dialog was dismissed and none is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderACL();

    const first = await grantToToken(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    held.resolve({});
    await waitForSuccess(qc);
    await user.click(screen.getByRole("button", { name: "Grant Access" }));
    const second = await screen.findByRole("dialog", { name: "Grant Access" });

    expect(within(second).getByLabelText("Token")).toHaveValue("");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderACL();

      await grantToToken(user);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

const REVOKE_FIRST = "Revoking Operator on / from alice@pve";
const REVOKE_SECOND = "Revoking Auditor on /vms/100 from operators";

/** Opens the confirmation for an entry, presses Revoke, and waits for the request to be out. */
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

const FIRST_ENTRY = "Revoke Operator on /";
const SECOND_ENTRY = "Revoke Auditor on /vms/100";

describe("a permission revoke that settles", () => {
  it("control: shows a failure in the section's banner while the page is there, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const ask = await revokeEntry(user, FIRST_ENTRY);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut.mock.calls).toEqual([
      [
        `${ACCESS}/acl`,
        { path: "/", roles: "Operator", users: "alice@pve", delete: true },
      ],
    ]);
  });

  it("toasts a failure that comes after the page was left, once, naming the entry", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    await revokeEntry(user, FIRST_ENTRY);
    cleanup();
    held.reject(denied());

    await expectOneToast(failedToast(REVOKE_FIRST));
  });

  it("toasts a failure, once, naming the entry, when its confirmation was dismissed while it was out, and puts nothing in the banner", async () => {
    // The banner is where a failure goes while its confirmation is open (the
    // control above). Once it is dismissed the banner would be the only other
    // place for it, and it names no entry.
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const ask = await revokeEntry(user, FIRST_ENTRY);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
    held.reject(denied());

    await expectOneToast(failedToast(REVOKE_FIRST));
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  /**
   * Revokes the first entry with the request held, dismisses its confirmation,
   * and opens the confirmation of the second entry in its place.
   */
  async function dismissForAnother(user: UserEvent) {
    const first = await revokeEntry(user, FIRST_ENTRY);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: SECOND_ENTRY }));
    return screen.findByRole("alertdialog", { name: "Revoke access?" });
  }

  it("does not close the confirmation opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderACL();

    const second = await dismissForAnother(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByText("operators")).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("toasts a failure, once, naming the entry, when another entry's confirmation is open, and neither closes that one nor puts anything in the banner behind it", async () => {
    // The banner is behind the open confirmation and is cleared by the next
    // revoke, so a failure left to it is reported nowhere.
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const second = await dismissForAnother(user);
    held.reject(denied());

    await expectOneToast(failedToast(REVOKE_FIRST));
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("closes the confirmation that sent it when it settles while that is still open", async () => {
    // The control for the two above: a confirmation that is still the one open
    // is closed, as it always was.
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderACL();

    const ask = await revokeEntry(user, FIRST_ENTRY);
    held.resolve({});
    await waitForSuccess(qc);

    await waitFor(() => {
      expect(ask).not.toBeInTheDocument();
    });
  });

  it("toasts nothing for a success that comes after the page was left", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderACL();

    await revokeEntry(user, SECOND_ENTRY);
    cleanup();
    held.resolve({});
    await waitForSuccess(qc);

    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("names the second entry when that is the one whose revoke fails after the page was left", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    await revokeEntry(user, SECOND_ENTRY);
    cleanup();
    held.reject(denied());

    await expectOneToast(failedToast(REVOKE_SECOND));
    expect(mockedPut.mock.calls).toEqual([
      [
        `${ACCESS}/acl`,
        {
          path: "/vms/100",
          roles: "Auditor",
          groups: "operators",
          delete: true,
        },
      ],
    ]);
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderACL();

      await revokeEntry(user, FIRST_ENTRY);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

// The confirmation is not held while its request is out, and the button that sent
// it is disabled by the request, so focus is lost and Tab walks out of the modal
// to the rows behind it, which a pointer cannot reach. Enter on another row's
// Revoke opens that entry's confirmation in place of the first's, with nothing
// closed between the two.
describe("a permission revoke whose confirmation is replaced by another entry's from the keyboard", () => {
  /** Revokes the first entry with the request held, and opens the second's confirmation over it. */
  async function replaceWithSecond(user: UserEvent) {
    await revokeEntry(user, FIRST_ENTRY);
    await tabTo(
      user,
      screen.getByRole("button", { name: SECOND_ENTRY, hidden: true }),
    );
    await user.keyboard("{Enter}");
    // The second entry's confirmation names the group, where the first's names
    // the user.
    const ask = await screen.findByRole("alertdialog", {
      name: "Revoke access?",
    });
    await waitFor(() => {
      expect(within(ask).getByText("operators")).toBeInTheDocument();
    });
    expect(within(ask).queryByText("alice@pve")).toBeNull();
    return ask;
  }

  it("toasts the failure, once, naming the first entry, and neither closes the second's confirmation nor puts anything in the banner", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderACL();

    const second = await replaceWithSecond(user);
    held.reject(denied());

    await expectOneToast(failedToast(REVOKE_FIRST));
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByText("operators")).toBeInTheDocument();
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("does not close the second entry's confirmation when the first succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderACL();

    const second = await replaceWithSecond(user);
    held.resolve({});
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByText("operators")).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});
