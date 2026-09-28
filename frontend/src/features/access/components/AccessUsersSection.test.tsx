import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  onlineManager,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";

import { apiClient, ApiClientError } from "@/lib/api-client";
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

const mockedList = vi.mocked(apiClient.list);
const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);

const CLUSTER = "cccccccc-0000-0000-0000-000000000005";
const USERS_URL = `/api/v1/clusters/${CLUSTER}/access/users`;
// The account Nexara authenticates as, by its documented default name.
const OWN = "nexara@pve";
const OWN_URL = `${USERS_URL}/nexara%40pve`;
// The detail read's cache key, to see what state the query is in.
const OWN_KEY = ["clusters", CLUSTER, "access", "users", OWN];

const CONFIRM_TITLE = "This will cut off Nexara's access";
// Worded as guardSelfCredential (internal/api/handlers/access.go) words it.
const REFUSAL =
  "This is the user nexara@pve Nexara uses to reach this cluster. " +
  "Continuing will cut Nexara off until the cluster is reconfigured with " +
  "new credentials. Retry with force=true to proceed anyway.";

// The edit saveDisabled makes: the account disabled, and nothing else because
// nothing else was touched.
const DISABLE = { enable: false };
// What a gateway failure reads as, wherever a test needs one.
const UNREACHABLE = "Failed to connect to Proxmox";

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

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

/** Renders the section, and hands back its client to read queries again. */
function renderSection(): QueryClient {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <AccessUsersSection clusterId={CLUSTER} capabilities={capabilities} />
    </QueryClientProvider>,
  );
  return qc;
}

/** Opens Edit on Nexara's own account and returns its dialog. */
async function openEdit(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: `Edit ${OWN}` }));
  return screen.findByRole("dialog", { name: `Edit ${OWN}` });
}

/**
 * Opens Edit on Nexara's own account, unticks "Account enabled" and saves.
 * Returns the edit dialog, which an override hides from role queries.
 */
async function saveDisabled(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  const edit = await openEdit(user);
  await user.click(
    await within(edit).findByRole("checkbox", { name: "Account enabled" }),
  );
  await user.click(within(edit).getByRole("button", { name: "Save" }));
  return edit;
}

/** The override, once open, with the user id typed into it. */
async function typedOverride(user: ReturnType<typeof userEvent.setup>) {
  const confirm = await screen.findByRole("dialog", { name: CONFIRM_TITLE });
  await user.type(
    within(confirm).getByRole("textbox", { name: "Confirmation text" }),
    OWN,
  );
  return confirm;
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

beforeEach(() => {
  vi.resetAllMocks();
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(
      path === USERS_URL
        ? [{ userid: OWN, enable: true, comment: "service account" }]
        : [],
    ),
  );
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
    await user.click(overrideAction(await typedOverride(user)));

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
    const confirm = await typedOverride(user);
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
    await user.click(overrideAction(await typedOverride(user)));

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
    const confirm = await screen.findByRole("dialog", { name: CONFIRM_TITLE });
    expect(within(confirm).getByText(REFUSAL)).toBeInTheDocument();
    expect(
      within(confirm).queryByRole("button", { name: "Delete User" }),
    ).toBeNull();
    const saveAnyway = within(confirm).getByRole("button", {
      name: "Save Anyway",
    });
    expect(saveAnyway).toBeDisabled();

    await typedOverride(user);
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

  it.each([
    [
      "Cancel",
      async (user: ReturnType<typeof userEvent.setup>) => {
        const confirm = screen.getByRole("dialog", { name: CONFIRM_TITLE });
        await user.click(
          within(confirm).getByRole("button", { name: "Cancel" }),
        );
      },
    ],
    [
      "Escape",
      async (user: ReturnType<typeof userEvent.setup>) => {
        await user.keyboard("{Escape}");
      },
    ],
  ])("goes back to the form as it was on %s", async (_, dismiss) => {
    const user = userEvent.setup();
    mockedPut.mockRejectedValueOnce(refused());
    renderSection();

    await saveDisabled(user);
    await typedOverride(user);
    await dismiss(user);

    await waitFor(() => {
      expect(
        screen.queryByRole("dialog", { name: CONFIRM_TITLE }),
      ).toBeNull();
    });
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
      const confirm = await typedOverride(user);
      await user.click(
        within(confirm).getByRole("button", { name: "Save Anyway" }),
      );

      await waitFor(() => {
        expect(
          screen.queryByRole("dialog", { name: CONFIRM_TITLE }),
        ).toBeNull();
      });
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
      expect(
        screen.queryByRole("dialog", { name: CONFIRM_TITLE }),
      ).toBeNull();
      expect(mockedPut).toHaveBeenCalledTimes(1);
      expect(mockedDelete).not.toHaveBeenCalled();
    },
  );
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
    const confirm = await typedOverride(user);
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
