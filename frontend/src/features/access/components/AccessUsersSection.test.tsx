import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  onlineManager,
  QueryClient,
  QueryClientProvider,
  useMutation,
} from "@tanstack/react-query";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import type { AccessCapabilities } from "../api/access-queries";
import { AccessUsersSection } from "./AccessUsersSection";

/**
 * The server refuses, with a 409, to disable or delete the account Nexara
 * authenticates with unless ?force=true says so, and each refusal has its own
 * type-to-confirm override: the edit's forces the EDIT, the delete's the
 * DELETE. An edit's refusal once opened the delete's, so typing the user id to
 * save an edit force-deleted the user and every token it owned.
 *
 * An edit also sends only the fields the operator touched. The update is
 * tristate per field, so one sent from a form that had never read the account,
 * or had read it before it changed, overwrote the real value: saving an e-mail
 * could re-enable a disabled account, and rewrote its comment from whatever the
 * form showed.
 *
 * Every confirmation holds while its request is in flight. Closing one does not
 * recall the request, and a refusal that arrived after it had gone opened an
 * override over whatever the operator had moved on to: the delete's over the
 * edit dialog, a form to save an edit under a prompt to force-delete the user.
 * A held alert dialog keeps keyboard focus too: both its buttons are disabled,
 * which leaves the focus trap nothing to wrap between, and Tab would walk out
 * into the page. And each override's title names the action it confirms, so the
 * four are told apart by more than their button.
 */

// The transport is mocked, not the hooks, so the real queries and mutations
// run and each test asserts the request that would leave the browser.
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

// The app's mutation-error net (lib/query-client.ts) toasts through sonner, so
// this one mock sees every toast a run can raise.
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedList = vi.mocked(apiClient.list);
const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000005";
const USERS_URL = `/api/v1/clusters/${CLUSTER}/access/users`;
// The account Nexara authenticates as, by its documented default name.
const OWN = "nexara@pve";
const OWN_URL = `${USERS_URL}/nexara%40pve`;
// The detail read's cache key, to see what state the query is in.
const OWN_KEY = ["clusters", CLUSTER, "access", "users", OWN];

// Another account, for an edit opened while a save of Nexara's own is still out.
const OTHER = "alice@pve";
const OTHER_URL = `${USERS_URL}/alice%40pve`;

// An API token of that account, by the id the section shows it under.
const TOKEN = "token01";
const FULL_TOKEN = `${OWN}!${TOKEN}`;
const TOKENS_URL = `${OWN_URL}/tokens`;
const TOKEN_URL = `${TOKENS_URL}/${TOKEN}`;

// Each override's title names the action it confirms. They end alike, which
// ANY_OVERRIDE matches, for asserting that none of them is open.
const EDIT_TITLE = "Saving this edit will cut off Nexara's access";
const DELETE_TITLE = "Deleting this user will cut off Nexara's access";
const REVOKE_TITLE = "Revoking this token will cut off Nexara's access";
const REGENERATE_TITLE = "Regenerating this token will cut off Nexara's access";
const ANY_OVERRIDE = /will cut off Nexara's access$/;
// Worded as guardSelfCredential (internal/api/handlers/access.go) words it.
const REFUSAL =
  "This is the user nexara@pve Nexara uses to reach this cluster. " +
  "Continuing can cut Nexara off, or take away permissions it relies on, " +
  "until its credentials are updated in Nexara or the change is undone in " +
  "Proxmox. Retry with force=true to proceed anyway.";

// The edit saveDisabled makes: the account disabled, and nothing else because
// nothing else was touched.
const DISABLE = { enable: false };
// What a gateway failure reads as, wherever a test needs one.
const UNREACHABLE = "Failed to connect to Proxmox";
// The toast for a save of Nexara's own account that fails once its dialog is
// gone. It names the account, because it can land on another page or over the
// Edit dialog of another account.
function savingFailed(message: string): string {
  return `Saving ${OWN} failed: ${message}`;
}

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

type UserEvent = ReturnType<typeof userEvent.setup>;

function refused(): ApiClientError {
  return new ApiClientError(409, { error: "Conflict", message: REFUSAL });
}

function unreachable(): ApiClientError {
  return new ApiClientError(502, {
    error: "Bad Gateway",
    message: UNREACHABLE,
  });
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  let reject: (reason: unknown) => void = () => undefined;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/**
 * Lets whatever is already queued run, timers included, and React draw what it
 * set: for looking at what did NOT happen once a request has settled.
 */
async function flush(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/** Renders the section on `qc`, and hands it back to read queries again. */
function renderOn(qc: QueryClient): QueryClient {
  render(
    <QueryClientProvider client={qc}>
      <AccessUsersSection clusterId={CLUSTER} capabilities={capabilities} />
    </QueryClientProvider>,
  );
  return qc;
}

/** Renders the section, and hands back its client to read queries again. */
function renderSection(): QueryClient {
  return renderOn(
    new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    }),
  );
}

/** renderSection, on the app's own kind of client (test/app-query-client.ts). */
function renderSectionOnAppClient(): QueryClient {
  return renderOn(createAppQueryClient());
}

/** Opens Edit on Nexara's own account and returns its dialog. */
async function openEdit(user: UserEvent): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: `Edit ${OWN}` }));
  return screen.findByRole("dialog", { name: `Edit ${OWN}` });
}

/**
 * Opens Edit on Nexara's own account, unticks "Account enabled" and saves.
 * Returns the edit dialog, which an override hides from role queries.
 */
async function saveDisabled(user: UserEvent): Promise<HTMLElement> {
  const edit = await openEdit(user);
  await user.click(
    await within(edit).findByRole("checkbox", { name: "Account enabled" }),
  );
  await user.click(within(edit).getByRole("button", { name: "Save" }));
  return edit;
}

/**
 * The override with that title, once open, with what it asks for typed into it:
 * the user id unless told otherwise.
 */
async function typedOverride(user: UserEvent, title: string, typed = OWN) {
  const confirm = await screen.findByRole("dialog", { name: title });
  await user.type(
    within(confirm).getByRole("textbox", { name: "Confirmation text" }),
    typed,
  );
  return confirm;
}

/**
 * Asserts that no override is open. It looks at hidden elements too: Radix hides
 * everything outside the topmost modal from role queries, and an override is
 * spared that only while the aria-live region of the CopyableName inside it
 * keeps it visible, which no assertion here should depend on.
 */
function expectNoOverride() {
  expect(
    screen.queryByRole("dialog", { name: ANY_OVERRIDE, hidden: true }),
  ).toBeNull();
}

/**
 * The override's action, whatever it is labelled: its one <button> that is
 * neither Cancel nor Close (the user id it shows is a span[role=button]).
 */
function overrideAction(confirm: HTMLElement): HTMLElement {
  return within(confirm).getByRole("button", {
    name: (name, el) =>
      el.tagName === "BUTTON" && name !== "Cancel" && name !== "Close",
  });
}

/**
 * The backdrop behind a dialog. Radix portals it in just before the dialog's
 * own element, and a click on it is a click outside the dialog.
 */
function backdropOf(dialog: HTMLElement): HTMLElement {
  const backdrop = dialog.previousElementSibling;
  if (!(backdrop instanceof HTMLElement)) {
    throw new Error("the dialog has no backdrop before it");
  }
  return backdrop;
}

/**
 * The ways out of an open dialog other than its own buttons. Each is tried on
 * the same dialog both with nothing in flight, where it has to close it, and
 * while its request is, where it has to be ignored: a dismissal that never
 * reached the dialog would pass the second without proving anything.
 */
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
    "a click outside it",
    async (user, dialog) => {
      await user.click(backdropOf(dialog));
    },
  ],
  [
    "its Close button",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Close" }));
    },
  ],
];

/**
 * Where focus goes once a click on an override's backdrop has dropped it to the
 * page: the keys that carry it on from there and, by focusing them, the form's
 * own Close and Cancel, where a browser puts it on Shift+Tab. user-event cannot
 * say so itself: it treats a page with nothing focused as the top of the
 * document, so its Shift+Tab lands on the last element, not the one before the
 * backdrop. What the trap does about it is the same whichever way focus arrives.
 */
const ESCAPES: [
  name: string,
  escape: (user: UserEvent, form: HTMLElement) => Promise<void>,
][] = [
  [
    "Shift+Tab",
    async (user) => {
      await user.tab({ shift: true });
    },
  ],
  [
    "Tab",
    async (user) => {
      await user.tab();
    },
  ],
  [
    "focus landing on the form's Close button",
    (_, form) => {
      within(form).getByRole("button", { name: "Close", hidden: true }).focus();
      return Promise.resolve();
    },
  ],
  [
    "focus landing on the form's Cancel button",
    (_, form) => {
      within(form)
        .getByRole("button", { name: "Cancel", hidden: true })
        .focus();
      return Promise.resolve();
    },
  ],
];

beforeEach(() => {
  vi.resetAllMocks();
  mockedList.mockImplementation((path: string) => {
    if (path === USERS_URL) {
      return Promise.resolve([
        { userid: OWN, enable: true, comment: "service account" },
      ]);
    }
    if (path === TOKENS_URL) {
      return Promise.resolve([{ userid: OWN, tokenid: TOKEN, privsep: true }]);
    }
    return Promise.resolve([]);
  });
  mockedGet.mockImplementation((path: string) =>
    path === OWN_URL
      ? Promise.resolve({ userid: OWN, enable: true, comment: "service account" })
      : Promise.reject(new Error(`unexpected GET ${path}`)),
  );
  mockedPut.mockResolvedValue({ status: "ok" });
  mockedDelete.mockResolvedValue(undefined);
});

describe("an edit refused as Nexara's own credential", () => {
  it("never sends a DELETE when its override is confirmed, but the edit again with force", async () => {
    const user = userEvent.setup();
    mockedPut.mockRejectedValueOnce(refused());
    renderSection();

    await saveDisabled(user);
    await user.click(overrideAction(await typedOverride(user, EDIT_TITLE)));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedDelete).not.toHaveBeenCalled();
    expect(mockedPut.mock.calls).toEqual([
      [OWN_URL, DISABLE],
      [`${OWN_URL}?force=true`, DISABLE],
    ]);
  });

  it("re-sends the edit as it was refused, not the form as read again since", async () => {
    const user = userEvent.setup();
    mockedPut.mockRejectedValueOnce(refused());
    const qc = renderSection();

    // Two fields touched and one not: a note added, the account disabled, and
    // the e-mail left as it was.
    const edit = await openEdit(user);
    const comment = await within(edit).findByRole("textbox", {
      name: "Comment",
    });
    await user.clear(comment);
    await user.type(comment, "retired");
    await user.click(
      within(edit).getByRole("checkbox", { name: "Account enabled" }),
    );
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    const confirm = await typedOverride(user, EDIT_TITLE);
    // The account's e-mail changes elsewhere and is read again while the
    // override is open; the form, where it was left untouched, follows, and
    // the two fields the operator did touch stay as they set them.
    mockedGet.mockImplementation((path: string) =>
      path === OWN_URL
        ? Promise.resolve({
            userid: OWN,
            enable: true,
            comment: "service account",
            email: "ops@example.com",
          })
        : Promise.reject(new Error(`unexpected GET ${path}`)),
    );
    await act(async () => {
      await qc.invalidateQueries();
    });
    await waitFor(() => {
      expect(document.getElementById("edit-email")).toHaveValue(
        "ops@example.com",
      );
    });
    expect(document.getElementById("edit-comment")).toHaveValue("retired");
    // Under the override, so by label rather than by role. The account is read
    // as enabled again by now, and the operator's untick still holds.
    expect(within(edit).getByLabelText("Account enabled")).not.toBeChecked();
    await user.click(overrideAction(confirm));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // No e-mail, though the form now shows one: Save Anyway confirms the edit
    // that was refused, field for field.
    const refusedEdit = { comment: "retired", enable: false };
    expect(mockedPut.mock.calls).toStrictEqual([
      [OWN_URL, refusedEdit],
      [`${OWN_URL}?force=true`, refusedEdit],
    ]);
  });

  it("re-sends the edit as it was refused, not the form as edited again while it was in flight", async () => {
    const user = userEvent.setup();
    const first = deferred<unknown>();
    mockedPut.mockReturnValueOnce(first.promise);
    renderSection();

    const edit = await openEdit(user);
    await user.click(
      await within(edit).findByRole("checkbox", { name: "Account enabled" }),
    );
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    // Save is held while the request is out, but the fields are not: before
    // the refusal comes back the operator ticks the account enabled again and
    // adds an e-mail.
    expect(
      await within(edit).findByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    const enabled = within(edit).getByRole("checkbox", {
      name: "Account enabled",
    });
    await user.click(enabled);
    const email = within(edit).getByRole("textbox", { name: "Email" });
    await user.type(email, "ops@example.com");
    expect(enabled).toBeChecked();
    expect(email).toHaveValue("ops@example.com");
    first.reject(refused());
    await user.click(overrideAction(await typedOverride(user, EDIT_TITLE)));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // The override describes the disable that was refused, so that is what it
    // forces, not the form as it stands now.
    expect(mockedPut.mock.calls).toStrictEqual([
      [OWN_URL, DISABLE],
      [`${OWN_URL}?force=true`, DISABLE],
    ]);
  });

  it("asks to Save Anyway with the server's reason, and holds it while the forced edit is in flight", async () => {
    const user = userEvent.setup();
    const forced = deferred<unknown>();
    mockedPut
      .mockRejectedValueOnce(refused())
      .mockReturnValueOnce(forced.promise);
    renderSection();

    await saveDisabled(user);
    const confirm = await screen.findByRole("dialog", { name: EDIT_TITLE });
    expect(within(confirm).getByText(REFUSAL)).toBeInTheDocument();
    expect(
      within(confirm).queryByRole("button", { name: "Delete User" }),
    ).toBeNull();
    const saveAnyway = within(confirm).getByRole("button", {
      name: "Save Anyway",
    });
    expect(saveAnyway).toBeDisabled();

    await typedOverride(user, EDIT_TITLE);
    await user.click(saveAnyway);
    expect(
      await within(confirm).findByRole("button", { name: "Working..." }),
    ).toBeDisabled();

    forced.resolve({ status: "ok" });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedDelete).not.toHaveBeenCalled();
  });

  it.each(DISMISSALS)(
    "ignores %s while the forced edit is in flight, with Cancel disabled",
    async (_, dismiss) => {
      const user = userEvent.setup();
      const forced = deferred<unknown>();
      mockedPut
        .mockRejectedValueOnce(refused())
        .mockReturnValueOnce(forced.promise);
      renderSection();

      const edit = await saveDisabled(user);
      const confirm = await typedOverride(user, EDIT_TITLE);
      await user.click(
        within(confirm).getByRole("button", { name: "Save Anyway" }),
      );
      // The button says so once the request is out, which is when the dialog
      // holds; a dismissal before then would close it whatever the guard does.
      expect(
        await within(confirm).findByRole("button", { name: "Working..." }),
      ).toBeDisabled();
      expect(
        within(confirm).getByRole("button", { name: "Cancel" }),
      ).toBeDisabled();

      await dismiss(user, confirm);

      // Neither the override nor the form under it went, and nothing else was
      // sent: closing would not have recalled the forced edit.
      expect(confirm).toBeInTheDocument();
      expect(confirm).toHaveAttribute("data-state", "open");
      expect(edit).toBeInTheDocument();
      expect(mockedPut).toHaveBeenCalledTimes(2);

      // The edit settles on the override it was sent from.
      forced.resolve({ status: "ok" });
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).toBeNull();
      });
      expect(mockedDelete).not.toHaveBeenCalled();
    },
  );

  // The override's confirm button is the last thing focused in it when the
  // request disables it, and the focus trap pulls focus back by refocusing the
  // last element that had it, which does nothing to a disabled button. Focus
  // that gets out then stays out: a click on the backdrop drops it to the page,
  // and where it goes next can be the form's own Cancel or Close, whose Enter
  // closes the form and unmounts the held override, its edit still on the way.
  it.each(ESCAPES)(
    "brings focus back into the override after %s, while the forced edit is in flight",
    async (_, escape) => {
      const user = userEvent.setup();
      const forced = deferred<unknown>();
      mockedPut
        .mockRejectedValueOnce(refused())
        .mockReturnValueOnce(forced.promise);
      renderSection();

      const form = await saveDisabled(user);
      const confirm = await typedOverride(user, EDIT_TITLE);
      await user.click(
        within(confirm).getByRole("button", { name: "Save Anyway" }),
      );
      expect(
        await within(confirm).findByRole("button", { name: "Working..." }),
      ).toBeDisabled();

      await user.click(backdropOf(confirm));
      await escape(user, form);

      expect(
        confirm.contains(document.activeElement),
        "focus is outside the override",
      ).toBe(true);
      expect(form).toBeInTheDocument();
      expect(mockedPut).toHaveBeenCalledTimes(2);

      forced.resolve({ status: "ok" });
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).toBeNull();
      });
    },
  );

  it.each([
    [
      "Cancel",
      async (user: UserEvent, dialog: HTMLElement) => {
        await user.click(
          within(dialog).getByRole("button", { name: "Cancel" }),
        );
      },
    ],
    ...DISMISSALS,
  ])("goes back to the form as it was on %s", async (_, dismiss) => {
    const user = userEvent.setup();
    mockedPut.mockRejectedValueOnce(refused());
    renderSection();

    await saveDisabled(user);
    await dismiss(user, await typedOverride(user, EDIT_TITLE));

    await waitFor(expectNoOverride);
    const edit = screen.getByRole("dialog", { name: `Edit ${OWN}` });
    const enabled = within(edit).getByRole("checkbox", {
      name: "Account enabled",
    });
    expect(enabled).not.toBeChecked();
    expect(mockedPut).toHaveBeenCalledTimes(1);

    // The form still saves, as an ordinary edit.
    await user.click(enabled);
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedPut.mock.calls).toEqual([
      [OWN_URL, DISABLE],
      [OWN_URL, { enable: true }],
    ]);
    expect(mockedDelete).not.toHaveBeenCalled();
  });

  it.each([
    [
      "a gateway failure",
      new ApiClientError(502, {
        error: "Bad Gateway",
        message: "Failed to connect to Proxmox",
      }),
    ],
    ["a second refusal", refused()],
  ])(
    "reports %s of the forced edit in the form, not in an override",
    async (_, failure) => {
      const user = userEvent.setup();
      mockedPut.mockRejectedValueOnce(refused()).mockRejectedValueOnce(failure);
      renderSection();

      await saveDisabled(user);
      const confirm = await typedOverride(user, EDIT_TITLE);
      await user.click(
        within(confirm).getByRole("button", { name: "Save Anyway" }),
      );

      await waitFor(expectNoOverride);
      const edit = screen.getByRole("dialog", { name: `Edit ${OWN}` });
      expect(within(edit).getByText(failure.message)).toBeInTheDocument();
      expect(mockedPut).toHaveBeenCalledTimes(2);
      expect(mockedDelete).not.toHaveBeenCalled();
    },
  );
});

describe("an edit that fails for any other reason", () => {
  it.each([
    [403, "Proxmox API permission denied"],
    [502, "Failed to connect to Proxmox"],
  ])(
    "reports a %i in the form and offers no override",
    async (status, message) => {
      const user = userEvent.setup();
      mockedPut.mockRejectedValueOnce(
        new ApiClientError(status, { error: "failed", message }),
      );
      renderSection();

      const edit = await saveDisabled(user);

      expect(await within(edit).findByText(message)).toBeInTheDocument();
      expectNoOverride();
      expect(mockedPut).toHaveBeenCalledTimes(1);
      expect(mockedDelete).not.toHaveBeenCalled();
    },
  );
});

// The app's QueryClient toasts, through the MutationCache in
// lib/query-client.ts, every failed mutation whose HOOK has no onError of its
// own; the onError a component gives mutate() does not count. While the edit
// dialog is open it shows the failure itself, the override for a refusal and
// the form for anything else, so useUpdateAccessUser opts out
// (errorsHandledLocally), and once the dialog is gone EditUserDialog toasts it
// (below). Without the opt-out each failure was reported twice, and a refusal
// put a toast up beside the override that answers it.
//
// These run on the app's own kind of client, and start from the control its docs
// ask for (test/app-query-client.ts).
describe("an edit's failure is reported once, not also as a toast", () => {
  const PROBE = "PROBE-NOT-A-REAL-FAILURE";

  it("control: a failed mutation with no onError of its own toasts on this client", async () => {
    // On the client the tests below get, rendered as they are, so a change to
    // that client shows up here.
    const qc = renderSectionOnAppClient();
    // What each hook in access-queries.ts is before it opts out: a mutationFn
    // and no onError.
    const { result } = renderHook(
      () => useMutation({ mutationFn: () => Promise.reject(new Error(PROBE)) }),
      {
        wrapper: ({ children }: { children: ReactNode }) => (
          <QueryClientProvider client={qc}>{children}</QueryClientProvider>
        ),
      },
    );

    await act(async () => {
      await result.current.mutateAsync().catch(() => undefined);
    });

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it("opens the Save Anyway override for a refusal, with no toast beside it", async () => {
    const user = userEvent.setup();
    mockedPut.mockRejectedValueOnce(refused());
    renderSectionOnAppClient();

    await saveDisabled(user);
    const confirm = await screen.findByRole("dialog", { name: EDIT_TITLE });

    // The net runs before a failure reaches the component's own handler, so
    // with the override on screen, a toast that was coming has come.
    expect(within(confirm).getByText(REFUSAL)).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut.mock.calls).toEqual([[OWN_URL, DISABLE]]);
  });

  it.each([
    [
      "a 400",
      new ApiClientError(400, {
        error: "bad_request",
        message: "Parameter verification failed",
      }),
      "Parameter verification failed",
    ],
    [
      "a 500",
      new ApiClientError(500, {
        error: "internal_error",
        message: "Internal server error",
      }),
      "Internal server error",
    ],
    // Not the server's answer, so the form words it itself, where the net
    // would have toasted the error's own text.
    [
      "a failure that is not the server's",
      new Error("socket hang up"),
      "Failed to update user",
    ],
  ])(
    "shows %s in the form, with no override and no toast",
    async (_, failure, shown) => {
      const user = userEvent.setup();
      mockedPut.mockRejectedValueOnce(failure);
      renderSectionOnAppClient();

      const edit = await saveDisabled(user);

      expect(await within(edit).findByText(shown)).toBeInTheDocument();
      expectNoOverride();
      expect(mockedToastError).not.toHaveBeenCalled();
      expect(mockedPut).toHaveBeenCalledTimes(1);
    },
  );

  it("shows the failure of a forced edit in the form, with no toast for it or for the refusal before it", async () => {
    const user = userEvent.setup();
    mockedPut
      .mockRejectedValueOnce(refused())
      .mockRejectedValueOnce(unreachable());
    renderSectionOnAppClient();

    await saveDisabled(user);
    const confirm = await typedOverride(user, EDIT_TITLE);
    await user.click(
      within(confirm).getByRole("button", { name: "Save Anyway" }),
    );

    await waitFor(expectNoOverride);
    const edit = screen.getByRole("dialog", { name: `Edit ${OWN}` });
    expect(within(edit).getByText(UNREACHABLE)).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut).toHaveBeenCalledTimes(2);
  });
});

// Only Save is held while a save is out, so the dialog can be dismissed under
// it, and the whole page can be left, by navigating or logging out. TanStack
// runs the callbacks given to mutate() only while the component is mounted, and
// the hook has opted out of the global toast, so a save that fails after that
// would be reported nowhere: EditUserDialog toasts it instead, naming the
// account, and one that succeeds must not close the dialog opened in the
// meantime.
describe("a save that settles after its edit dialog is gone", () => {
  const DENIED = "Proxmox API permission denied";

  const LEAVES: [
    name: string,
    leave: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
  ][] = [
    [
      "Cancel",
      async (user, dialog) => {
        await user.click(
          within(dialog).getByRole("button", { name: "Cancel" }),
        );
      },
    ],
    ...DISMISSALS,
    [
      "the page being left",
      () => {
        cleanup();
        return Promise.resolve();
      },
    ],
  ];

  it.each(LEAVES)(
    "toasts a failure that comes after %s, once",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderSectionOnAppClient();

      const edit = await saveDisabled(user);
      expect(
        await within(edit).findByRole("button", { name: "Saving..." }),
      ).toBeDisabled();
      await leave(user, edit);
      await waitFor(() => {
        expect(edit).not.toBeInTheDocument();
      });

      held.reject(
        new ApiClientError(403, { error: "forbidden", message: DENIED }),
      );
      await waitFor(() => {
        expect(mockedToastError).toHaveBeenCalledWith(savingFailed(DENIED));
      });
      // Once: not also by the global net, and not again later.
      await flush();
      expect(mockedToastError).toHaveBeenCalledTimes(1);
      expect(mockedPut).toHaveBeenCalledTimes(1);
    },
  );

  it("toasts a refusal that comes after the dialog was dismissed, and opens no override", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderSectionOnAppClient();

    const edit = await saveDisabled(user);
    await within(edit).findByRole("button", { name: "Saving..." });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(edit).not.toBeInTheDocument();
    });

    held.reject(refused());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(savingFailed(REFUSAL));
    });
    await flush();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expectNoOverride();
    expect(screen.queryByRole("dialog")).toBeNull();
    // Nothing forced it: there was no one to ask.
    expect(mockedPut.mock.calls).toEqual([[OWN_URL, DISABLE]]);
  });

  it("toasts the failure of a forced edit whose page was left", async () => {
    const user = userEvent.setup();
    const forced = deferred<unknown>();
    mockedPut
      .mockRejectedValueOnce(refused())
      .mockReturnValueOnce(forced.promise);
    renderSectionOnAppClient();

    await saveDisabled(user);
    const confirm = await typedOverride(user, EDIT_TITLE);
    await user.click(
      within(confirm).getByRole("button", { name: "Save Anyway" }),
    );
    // Nothing on the page can dismiss the override while the forced edit is
    // out, but leaving the page still takes it away.
    expect(
      await within(confirm).findByRole("button", { name: "Working..." }),
    ).toBeDisabled();
    cleanup();

    forced.reject(unreachable());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(savingFailed(UNREACHABLE));
    });
    await flush();

    // The refusal before it, which the override answered, raised none.
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedPut).toHaveBeenCalledTimes(2);
  });

  /**
   * Saves Nexara's own account with the request held (the caller queues it),
   * dismisses that dialog with Escape, and opens the Edit dialog of another
   * account in its place.
   */
  async function dismissForAnother(user: UserEvent) {
    mockedList.mockImplementation((path: string) =>
      Promise.resolve(
        path === USERS_URL
          ? [
              { userid: OWN, enable: true, comment: "service account" },
              { userid: OTHER, enable: true },
            ]
          : [],
      ),
    );
    mockedGet.mockImplementation((path: string) => {
      if (path === OWN_URL) {
        return Promise.resolve({ userid: OWN, enable: true });
      }
      if (path === OTHER_URL) {
        return Promise.resolve({ userid: OTHER, enable: true });
      }
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
    const qc = renderSectionOnAppClient();

    const first = await saveDisabled(user);
    await within(first).findByRole("button", { name: "Saving..." });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(first).not.toBeInTheDocument();
    });
    await user.click(
      await screen.findByRole("button", { name: `Edit ${OTHER}` }),
    );
    const second = await screen.findByRole("dialog", { name: `Edit ${OTHER}` });
    await within(second).findByRole("textbox", { name: "Comment" });
    return { qc, second };
  }

  it("does not close the edit dialog opened in its place when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc, second } = await dismissForAnother(user);

    held.resolve({ status: "ok" });
    await waitFor(() => {
      expect(
        qc
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state.status),
      ).toEqual(["success"]);
    });
    await flush();

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  it("names the account in the toast when it fails over another account's open dialog, and leaves that dialog alone", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { second } = await dismissForAnother(user);

    held.reject(
      new ApiClientError(403, { error: "forbidden", message: DENIED }),
    );
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(savingFailed(DENIED));
    });
    await flush();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    // The dialog that is open is the other account's, and this was not its save.
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).queryByText(DENIED)).toBeNull();
    expectNoOverride();
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });
});

describe("an edit sends only what the operator touched", () => {
  it("sends an e-mail changed alone as that field alone", async () => {
    const user = userEvent.setup();
    renderSection();

    const edit = await openEdit(user);
    const email = await within(edit).findByRole("textbox", { name: "Email" });
    await user.type(email, "ops@example.com");
    expect(email).toHaveValue("ops@example.com");
    await user.click(within(edit).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // The form showed the comment and an enabled account throughout, and
    // neither was touched, so neither goes out.
    expect(mockedPut.mock.calls).toStrictEqual([
      [OWN_URL, { email: "ops@example.com" }],
    ]);
  });

  it("leaves enable out when only the comment of a disabled account is edited", async () => {
    const user = userEvent.setup();
    mockedGet.mockImplementation((path: string) =>
      path === OWN_URL
        ? Promise.resolve({
            userid: OWN,
            enable: false,
            comment: "service account",
          })
        : Promise.reject(new Error(`unexpected GET ${path}`)),
    );
    renderSection();

    const edit = await openEdit(user);
    expect(
      await within(edit).findByRole("checkbox", { name: "Account enabled" }),
    ).not.toBeChecked();
    const comment = within(edit).getByRole("textbox", { name: "Comment" });
    await user.clear(comment);
    await user.type(comment, "retired");
    expect(comment).toHaveValue("retired");
    await user.click(within(edit).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedPut.mock.calls).toStrictEqual([
      [OWN_URL, { comment: "retired" }],
    ]);
  });

  it("holds Save until a field is touched", async () => {
    const user = userEvent.setup();
    renderSection();

    const edit = await openEdit(user);
    const save = await within(edit).findByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await user.click(save);
    expect(mockedPut).not.toHaveBeenCalled();

    await user.type(
      within(edit).getByRole("textbox", { name: "Comment" }),
      "!",
    );
    expect(save).toBeEnabled();
  });

  it("sends nothing when the form is submitted with nothing touched", async () => {
    const user = userEvent.setup();
    renderSection();

    const edit = await openEdit(user);
    const comment = await within(edit).findByRole("textbox", {
      name: "Comment",
    });
    const form = edit.querySelector("form");
    if (form === null) throw new Error("the edit dialog has no form");

    // A submit that does not go through the disabled Save button, as
    // form.requestSubmit() does not. The mutation calls the transport a few
    // microtasks after it is asked to, so let them run before looking.
    fireEvent.submit(form);
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockedPut).not.toHaveBeenCalled();

    // The same submit does reach the handler: with a field touched it sends,
    // and it is then the only request there has been.
    await user.type(comment, "!");
    fireEvent.submit(form);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(mockedPut.mock.calls).toStrictEqual([
      [OWN_URL, { comment: "service account!" }],
    ]);
  });
});

describe("an edit whose read of the account fails or stalls", () => {
  it.each([
    ["the server's message", unreachable(), UNREACHABLE],
    [
      "a fallback when the failure carries none",
      new Error("socket hang up"),
      "Failed to load user",
    ],
  ])("shows %s in place of the form", async (_, failure, shown) => {
    const user = userEvent.setup();
    mockedGet.mockRejectedValueOnce(failure);
    renderSection();

    const edit = await openEdit(user);

    expect(await within(edit).findByText(shown)).toBeInTheDocument();
    // Nothing to save from: no fields, and no Save to send an account of
    // empty fields with "Account enabled" ticked.
    expect(within(edit).queryByRole("textbox")).toBeNull();
    expect(within(edit).queryByRole("checkbox")).toBeNull();
    expect(within(edit).queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("reads the account again on Retry, then shows the form", async () => {
    const user = userEvent.setup();
    mockedGet.mockRejectedValueOnce(unreachable());
    renderSection();

    const edit = await openEdit(user);
    await user.click(
      await within(edit).findByRole("button", { name: "Retry" }),
    );

    expect(
      await within(edit).findByRole("textbox", { name: "Comment" }),
    ).toHaveValue("service account");
    expect(within(edit).queryByText(UNREACHABLE)).toBeNull();
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it("keeps the form, with a notice, when a later read of an account it already has fails", async () => {
    const user = userEvent.setup();
    const qc = renderSection();

    const edit = await openEdit(user);
    await within(edit).findByRole("textbox", { name: "Comment" });
    // Nothing to report while the reads are going well.
    expect(within(edit).queryByRole("status")).toBeNull();
    // An access change makes the account read again, and that read fails.
    // TanStack keeps the earlier data through it.
    mockedGet.mockRejectedValue(unreachable());
    await act(async () => {
      await qc.invalidateQueries();
    });
    expect(qc.getQueryState(OWN_KEY)?.status).toBe("error");
    expect(qc.getQueryState(OWN_KEY)?.data).toBeDefined();

    // The form is looked up again, not held from before the failed read: it
    // has to still be there, saying why what it shows may be out of date, and
    // to still save what is touched and nothing else.
    const notice = await within(edit).findByRole("status");
    expect(notice).toHaveTextContent(UNREACHABLE);
    expect(notice).toHaveTextContent("as last read");
    await user.type(
      within(edit).getByRole("textbox", { name: "Email" }),
      "ops@example.com",
    );
    await user.click(within(edit).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedPut.mock.calls).toStrictEqual([
      [OWN_URL, { email: "ops@example.com" }],
    ]);
  });

  it("draws no form while the read is paused, and the form once it resumes", async () => {
    const user = userEvent.setup();
    const qc = renderSection();
    await screen.findByRole("button", { name: `Edit ${OWN}` });

    // Offline, the read is neither loading nor failed and has no data, and it
    // stays so until the connection returns.
    act(() => {
      onlineManager.setOnline(false);
    });
    try {
      const edit = await openEdit(user);
      await waitFor(() => {
        expect(qc.getQueryState(OWN_KEY)?.fetchStatus).toBe("paused");
      });
      expect(within(edit).queryByRole("textbox")).toBeNull();
      expect(within(edit).queryByRole("checkbox")).toBeNull();
      expect(within(edit).queryByRole("button", { name: "Save" })).toBeNull();
      expect(mockedGet).not.toHaveBeenCalled();

      act(() => {
        onlineManager.setOnline(true);
      });
      expect(
        await within(edit).findByRole("textbox", { name: "Comment" }),
      ).toHaveValue("service account");
    } finally {
      // Whatever happened above, no later test starts offline.
      act(() => {
        onlineManager.setOnline(true);
      });
    }
  });
});

describe("a delete refused as Nexara's own credential", () => {
  it("keeps its own override, which forces the delete", async () => {
    const user = userEvent.setup();
    mockedDelete.mockRejectedValueOnce(refused());
    renderSection();

    await user.click(
      await screen.findByRole("button", { name: `Delete ${OWN}` }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: `Delete ${OWN}?`,
    });
    await user.click(within(ask).getByRole("button", { name: "Delete User" }));
    const confirm = await typedOverride(user, DELETE_TITLE);
    expect(
      within(confirm).queryByRole("button", { name: "Save Anyway" }),
    ).toBeNull();
    await user.click(
      within(confirm).getByRole("button", { name: "Delete User" }),
    );

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedDelete.mock.calls).toEqual([
      [OWN_URL],
      [`${OWN_URL}?force=true`],
    ]);
    expect(mockedPut).not.toHaveBeenCalled();
  });
});

// The three confirmations that hold while their request is out, each with the
// request it sends, the transport that must stay silent, and the override a
// refusal of it opens. A token's row buttons are named for its own id; its
// dialogs and override for the full one.
const SECRET = "00000000-0000-0000-0000-000000000000";
const HELD = [
  {
    name: "user delete",
    needsTokens: false,
    trigger: `Delete ${OWN}`,
    ask: `Delete ${OWN}?`,
    send: "Delete User",
    sending: "Deleting...",
    request: mockedDelete,
    other: mockedPut,
    title: DELETE_TITLE,
    typed: OWN,
    confirm: "Delete User",
    forced: undefined,
    sent: [[OWN_URL], [`${OWN_URL}?force=true`]],
  },
  {
    name: "token revoke",
    needsTokens: true,
    trigger: `Revoke ${TOKEN}`,
    ask: `Revoke ${FULL_TOKEN}?`,
    send: "Revoke Token",
    sending: "Revoking...",
    request: mockedDelete,
    other: mockedPut,
    title: REVOKE_TITLE,
    typed: FULL_TOKEN,
    confirm: "Revoke Token",
    forced: undefined,
    sent: [[TOKEN_URL], [`${TOKEN_URL}?force=true`]],
  },
  {
    name: "token regenerate",
    needsTokens: true,
    trigger: `Regenerate ${TOKEN}`,
    ask: `Regenerate ${FULL_TOKEN}?`,
    send: "Regenerate",
    sending: "Regenerating...",
    request: mockedPut,
    other: mockedDelete,
    title: REGENERATE_TITLE,
    typed: FULL_TOKEN,
    confirm: "Regenerate Token",
    forced: { "full-tokenid": FULL_TOKEN, value: SECRET },
    sent: [
      [TOKEN_URL, { regenerate: true }],
      [`${TOKEN_URL}?force=true`, { regenerate: true }],
    ],
  },
];
type Held = (typeof HELD)[number];

/** Expands Nexara's own account, when the confirmation is on one of its tokens. */
async function expandFor(user: UserEvent, held: Held) {
  if (held.needsTokens) await user.click(await screen.findByText(OWN));
}

/** Opens the confirmation from the button in the table that asks for it. */
async function openHeld(user: UserEvent, held: Held): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: held.trigger }));
  return screen.findByRole("alertdialog", { name: held.ask });
}

/**
 * Confirms an open confirmation, and waits for its button to say the request
 * is out. The hold starts there and not at the click: TanStack hands React
 * isPending a macrotask after mutate(), so a dismissal in between would close
 * the dialog whatever the guard does. No one presses a key that fast.
 */
async function sendHeld(user: UserEvent, held: Held, dialog: HTMLElement) {
  await user.click(within(dialog).getByRole("button", { name: held.send }));
  expect(
    await within(dialog).findByRole("button", { name: held.sending }),
  ).toBeDisabled();
}

describe("a confirmation held while its request is in flight", () => {
  it.each(HELD)(
    "holds the $name dialog, then the override its refusal opens, until their requests settle",
    async (held) => {
      const { request, other, title, typed, confirm, forced, sent } = held;
      const user = userEvent.setup();
      const first = deferred<unknown>();
      const second = deferred<unknown>();
      request
        .mockReturnValueOnce(first.promise)
        .mockReturnValueOnce(second.promise);
      renderSection();
      await expandFor(user, held);

      // With nothing in flight Cancel and Escape both close it: what the guard
      // must let through before the request is out, and stop after.
      const byCancel = await openHeld(user, held);
      await user.click(
        within(byCancel).getByRole("button", { name: "Cancel" }),
      );
      await waitFor(() => {
        expect(byCancel).not.toBeInTheDocument();
      });
      const byEscape = await openHeld(user, held);
      await user.keyboard("{Escape}");
      await waitFor(() => {
        expect(byEscape).not.toBeInTheDocument();
      });
      expect(request).not.toHaveBeenCalled();

      const dialog = await openHeld(user, held);
      await sendHeld(user, held, dialog);
      expect(
        within(dialog).getByRole("button", { name: "Cancel" }),
      ).toBeDisabled();

      // Both buttons are disabled, so the dialog has nothing to tab between,
      // and focus left on the button that was pressed would walk out into the
      // page on Tab, to a row's Edit that Enter opens the edit dialog from.
      // Focus is on the dialog itself instead, and Tab leaves it there.
      await user.tab();
      expect(
        dialog.contains(document.activeElement),
        "Tab left the dialog",
      ).toBe(true);
      await user.tab({ shift: true });
      expect(
        dialog.contains(document.activeElement),
        "Shift+Tab left the dialog",
      ).toBe(true);

      await user.keyboard("{Escape}");
      expect(dialog).toBeInTheDocument();
      expect(dialog).toHaveAttribute("data-state", "open");
      expect(request).toHaveBeenCalledTimes(1);

      // The refusal lands on the dialog it answers, which gives way to the
      // override that names this action. By element and not by role: under the
      // override, an alert dialog still open would be hidden from a role query.
      first.reject(refused());
      const override = await typedOverride(user, title, typed);
      expect(within(override).getByText(REFUSAL)).toBeInTheDocument();
      expect(dialog).not.toBeInTheDocument();

      // Confirmed, it sends the request it names and no other: the other
      // transport, where the wrong override once sent its request, stays quiet.
      // The override then holds in its turn. Each override's call site wires its
      // own pending, the token one to either token request, so each is proved
      // on its own.
      await user.click(within(override).getByRole("button", { name: confirm }));
      expect(other).not.toHaveBeenCalled();
      expect(
        await within(override).findByRole("button", { name: "Working..." }),
      ).toBeDisabled();
      expect(
        within(override).getByRole("button", { name: "Cancel" }),
      ).toBeDisabled();
      await user.keyboard("{Escape}");
      expect(override).toBeInTheDocument();
      expect(override).toHaveAttribute("data-state", "open");
      expect(request).toHaveBeenCalledTimes(2);

      // A regenerated token's secret opens over the override as it goes, so this
      // looks at the element rather than at what a role query can see past it.
      second.resolve(forced);
      await waitFor(() => {
        expect(override).not.toBeInTheDocument();
      });
      expect(request.mock.calls).toEqual(sent);
      expect(other).not.toHaveBeenCalled();
    },
  );

  // The override opens as the alert dialog that led to it closes, and cancelling
  // it sends focus back through that dialog to what opened it: the row's button.
  // The dialog it passes through is the one focus was moved to, not a button.
  it.each(HELD)(
    "sends focus back to the $name button when the override its refusal opens is cancelled",
    async (held) => {
      const user = userEvent.setup();
      const pending = deferred<unknown>();
      held.request.mockReturnValueOnce(pending.promise);
      renderSection();
      await expandFor(user, held);
      const trigger = await screen.findByRole("button", { name: held.trigger });
      const dialog = await openHeld(user, held);
      await sendHeld(user, held, dialog);

      pending.reject(refused());
      const override = await screen.findByRole("dialog", { name: held.title });
      await user.click(
        within(override).getByRole("button", { name: "Cancel" }),
      );

      await waitFor(() => {
        expect(trigger).toHaveFocus();
      });
      expect(held.other).not.toHaveBeenCalled();
    },
  );

  // A hold that outlived its request would leave the dialog up with nothing
  // that could close it, so it has to let go on every outcome.
  it.each(HELD)(
    "lets go of the $name dialog when its request succeeds",
    async (held) => {
      const user = userEvent.setup();
      const pending = deferred<unknown>();
      held.request.mockReturnValueOnce(pending.promise);
      renderSection();
      await expandFor(user, held);
      const dialog = await openHeld(user, held);
      await sendHeld(user, held, dialog);

      pending.resolve(held.forced);

      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      expectNoOverride();
      expect(held.request).toHaveBeenCalledTimes(1);
      expect(held.other).not.toHaveBeenCalled();
    },
  );

  it.each(HELD)(
    "lets go of the $name dialog when its request fails, and reports it without an override",
    async (held) => {
      const user = userEvent.setup();
      const pending = deferred<unknown>();
      held.request.mockReturnValueOnce(pending.promise);
      renderSection();
      await expandFor(user, held);
      const dialog = await openHeld(user, held);
      await sendHeld(user, held, dialog);

      pending.reject(unreachable());

      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      expect(await screen.findByText(UNREACHABLE)).toBeInTheDocument();
      expectNoOverride();
      expect(held.request).toHaveBeenCalledTimes(1);
      expect(held.other).not.toHaveBeenCalled();
    },
  );
});
