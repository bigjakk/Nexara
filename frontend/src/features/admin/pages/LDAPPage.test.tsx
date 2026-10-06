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
  waitForSuccess,
} from "@/test/save-outcome-kit";
import type { LDAPConfig } from "@/types/api";
import { LDAPPage } from "./LDAPPage";

/**
 * Saving an LDAP configuration opts out of the global error toast
 * (useCreateLDAPConfig, useUpdateLDAPConfig), because the open form shows the
 * failure itself. Cancel does not wait for the request, so the form can be gone
 * — closed, replaced by another config's, or the page left — when it fails. The
 * failure is then a toast naming the configuration, unless the session has
 * ended too (hooks/useSaveOutcome.ts); a success that comes after the form is
 * gone closes nothing.
 *
 * These run on the app's own kind of client, whose mutation cache is the one
 * that raises the global toast, so that "once" means once and "none" means none.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const FIRST = "directory01";
const SECOND = "directory02";
const FIRST_URL = "/api/v1/ldap/configs/ldap-01";
const CREATE_URL = "/api/v1/ldap/configs";
const TRANSPORT_REFUSAL =
  "The bind password would be sent in cleartext to ldap://ldap.example.com:389.";

function ldapConfig(id: string, name: string): LDAPConfig {
  return {
    id,
    name,
    enabled: true,
    server_url: "ldaps://ldap.example.com:636",
    start_tls: false,
    skip_tls_verify: false,
    bind_dn: "cn=reader,dc=example,dc=com",
    bind_password_set: true,
    search_base_dn: "dc=example,dc=com",
    user_filter: "(|(uid={{username}})(mail={{username}}))",
    username_attribute: "uid",
    email_attribute: "mail",
    display_name_attribute: "cn",
    group_search_base_dn: "ou=groups,dc=example,dc=com",
    group_filter: "(member={{userDN}})",
    group_attribute: "cn",
    group_role_mapping: {},
    default_role_id: null,
    sync_interval_minutes: 60,
    last_sync_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

type UserEvent = ReturnType<typeof userEvent.setup>;

function renderPage() {
  return renderOnAppClient(<LDAPPage />, { router: true });
}

/** The heading of the form that is open. */
const EDIT_FORM = { name: "Edit LDAP Configuration" };
const NEW_FORM = { name: "New LDAP Configuration" };

/** Opens the form of the config in the list's `position`th row. */
async function openEdit(user: UserEvent, position = 0): Promise<void> {
  const edits = await screen.findAllByRole("button", { name: "Edit" });
  const edit = edits[position];
  if (!edit) throw new Error(`no config in row ${String(position)}`);
  await user.click(edit);
  await screen.findByRole("heading", EDIT_FORM);
}

/** Opens the form of a config, edits nothing and presses Save. */
async function saveConfig(user: UserEvent, position = 0): Promise<void> {
  await openEdit(user, position);
  await pressSave(user, "Save");
}

/** Presses the form's Save (or Create) and waits for its request to be out. */
async function pressSave(user: UserEvent, label: string): Promise<void> {
  await user.click(screen.getByRole("button", { name: label }));
  await waitFor(() => {
    expect(screen.getByRole("button", { name: label })).toBeDisabled();
  });
}

/** Opens the form for a new config and presses Create. */
async function createConfig(user: UserEvent): Promise<void> {
  await user.click(await screen.findByRole("button", { name: "Add Config" }));
  await screen.findByRole("heading", NEW_FORM);
  await pressSave(user, "Create");
}

/** What each way of leaving the form does to it. */
const LEAVES: [name: string, leave: (user: UserEvent) => Promise<void>][] = [
  [
    "Cancel",
    async (user) => {
      await user.click(screen.getByRole("button", { name: "Cancel" }));
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

/** The refusal the server gives a save that downgrades the transport, which the form turns into a prompt. */
function transportRefusal(): ApiClientError {
  return new ApiClientError(422, {
    error: "insecure_ldap_transport_confirm_required",
    message: TRANSPORT_REFUSAL,
    details: { transport_kind: "cleartext" },
  });
}

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(
      path === CREATE_URL
        ? [ldapConfig("ldap-01", FIRST), ldapConfig("ldap-02", SECOND)]
        : [],
    ),
  );
  mockedPost.mockResolvedValue(ldapConfig("ldap-03", "created"));
  mockedPut.mockResolvedValue(ldapConfig("ldap-01", FIRST));
});

afterEach(() => {
  signOutForGood();
});

describe("an LDAP configuration save that settles", () => {
  it("control: closes the form when it succeeds while the form is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderPage();

    await saveConfig(user);
    held.resolve(ldapConfig("ldap-01", FIRST));
    await waitForSuccess(qc);

    await waitFor(() => {
      expect(screen.queryByRole("heading", EDIT_FORM)).toBeNull();
    });
    expect(mockedPut.mock.calls).toHaveLength(1);
    expect(mockedPut.mock.calls[0]?.[0]).toBe(FIRST_URL);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: shows a failure in the form while the form is open, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await saveConfig(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    expect(screen.getByRole("heading", EDIT_FORM)).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("control: turns the server's transport refusal into a prompt in the form, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await saveConfig(user);
    held.reject(transportRefusal());

    const prompt = await screen.findByTestId("confirm-required-warning");
    expect(
      within(prompt).getByText("Passwords would travel in cleartext"),
    ).toBeInTheDocument();
    expect(
      within(prompt).getByRole("button", { name: "Save without encryption" }),
    ).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(LEAVES)(
    "toasts the failure of an edit that comes after %s, once, naming the configuration",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderPage();

      await saveConfig(user);
      await leave(user);
      held.reject(denied());

      await expectOneToast(
        `Saving the LDAP configuration "${FIRST}" failed: ${DENIED}`,
      );
      expect(mockedPut).toHaveBeenCalledTimes(1);
    },
  );

  it.each(LEAVES)(
    "toasts the failure of a create that comes after %s, once, naming the configuration",
    async (_, leave) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPost.mockReturnValueOnce(held.promise);
      renderPage();

      await createConfig(user);
      await leave(user);
      held.reject(denied());

      // The name a new configuration starts with.
      await expectOneToast(
        `Creating the LDAP configuration "Default" failed: ${DENIED}`,
      );
      expect(mockedPost.mock.calls[0]?.[0]).toBe(CREATE_URL);
    },
  );

  it("names the stored configuration, not the name typed into the form, when an edit that renames it fails", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await openEdit(user);
    const name = screen.getByPlaceholderText("Default");
    await user.clear(name);
    await user.type(name, "renamed");
    await pressSave(user, "Save");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    held.reject(denied());

    await expectOneToast(
      `Saving the LDAP configuration "${FIRST}" failed: ${DENIED}`,
    );
  });

  it("toasts the transport refusal that comes after the form was cancelled", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await saveConfig(user);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    held.reject(transportRefusal());

    await expectOneToast(
      `Saving the LDAP configuration "${FIRST}" failed: ${TRANSPORT_REFUSAL}`,
    );
    // Nothing acknowledged it: there was no one to ask.
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  /**
   * Saves the first config with the request held, cancels its form, and opens
   * the form of the second config in its place: the same page shows both.
   */
  async function cancelForAnother(user: UserEvent) {
    await saveConfig(user, 0);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await openEdit(user, 1);
    await waitFor(() => {
      expect(screen.getByDisplayValue(SECOND)).toBeInTheDocument();
    });
  }

  it("toasts a failure that comes after another config's form was opened, and shows nothing of it in that form", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await cancelForAnother(user);
    held.reject(denied());

    await expectOneToast(
      `Saving the LDAP configuration "${FIRST}" failed: ${DENIED}`,
    );
    expect(screen.getByRole("heading", EDIT_FORM)).toBeInTheDocument();
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("toasts a refusal that comes after another config's form was opened", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await cancelForAnother(user);
    held.reject(transportRefusal());

    await expectOneToast(
      `Saving the LDAP configuration "${FIRST}" failed: ${TRANSPORT_REFUSAL}`,
    );
  });

  it("does not close the form of another config, opened after it, when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderPage();

    await cancelForAnother(user);
    held.resolve(ldapConfig("ldap-01", FIRST));
    await waitForSuccess(qc);

    expect(screen.getByRole("heading", EDIT_FORM)).toBeInTheDocument();
    expect(screen.getByDisplayValue(SECOND)).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("does not close the form for a new config, opened after another create, when that create succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    const { qc } = renderPage();

    await createConfig(user);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(await screen.findByRole("button", { name: "Add Config" }));
    await screen.findByRole("heading", NEW_FORM);
    held.resolve(ldapConfig("ldap-03", "created"));
    await waitForSuccess(qc);

    expect(screen.getByRole("heading", NEW_FORM)).toBeInTheDocument();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("toasts nothing for a success that comes after the page was left", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderPage();

    await saveConfig(user);
    cleanup();
    held.resolve(ldapConfig("ldap-01", FIRST));
    await waitForSuccess(qc);

    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the page left",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderPage();

      await saveConfig(user);
      cleanup();
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure that comes after %s, with the form cancelled",
    async (_, end) => {
      const user = userEvent.setup();
      const held = deferred<unknown>();
      mockedPut.mockReturnValueOnce(held.promise);
      renderPage();

      await saveConfig(user);
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      end();
      held.reject(denied());
      await flushInAct();
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});
