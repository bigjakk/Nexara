import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { apiClient, ApiClientError } from "@/lib/api-client";
import type { AccessCapabilities } from "../api/access-queries";
import { AccessUsersSection } from "./AccessUsersSection";

/**
 * The server refuses, with a 409, to disable or delete the account Nexara
 * authenticates with unless ?force=true says so, and each refusal has its own
 * type-to-confirm override: the edit's forces the EDIT, the delete's the
 * DELETE. An edit's refusal once opened the delete's, so typing the user id to
 * save an edit force-deleted the user and every token it owned.
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

const CONFIRM_TITLE = "This will cut off Nexara's access";
// Worded as guardSelfCredential (internal/api/handlers/access.go) words it.
const REFUSAL =
  "This is the user nexara@pve Nexara uses to reach this cluster. " +
  "Continuing will cut Nexara off until the cluster is reconfigured with " +
  "new credentials. Retry with force=true to proceed anyway.";

// The edit saveDisabled makes: the account disabled, the rest as fetched.
const DISABLE = { comment: "service account", email: "", enable: false };

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

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
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

/**
 * Opens Edit on Nexara's own account, unticks "Account enabled" and saves.
 * Returns the edit dialog, which an override hides from role queries.
 */
async function saveDisabled(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  await user.click(await screen.findByRole("button", { name: `Edit ${OWN}` }));
  const edit = await screen.findByRole("dialog", { name: `Edit ${OWN}` });
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

    await saveDisabled(user);
    const confirm = await typedOverride(user);
    // The account's comment changes elsewhere and is read again while the
    // override is open; the form, where it was left untouched, follows.
    mockedGet.mockImplementation((path: string) =>
      path === OWN_URL
        ? Promise.resolve({
            userid: OWN,
            enable: true,
            comment: "changed elsewhere",
          })
        : Promise.reject(new Error(`unexpected GET ${path}`)),
    );
    await act(async () => {
      await qc.invalidateQueries();
    });
    await waitFor(() => {
      expect(document.getElementById("edit-comment")).toHaveValue(
        "changed elsewhere",
      );
    });
    await user.click(overrideAction(confirm));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedPut.mock.calls).toEqual([
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
      [OWN_URL, { ...DISABLE, enable: true }],
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
