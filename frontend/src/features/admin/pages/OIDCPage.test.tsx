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
import type { OIDCConfig } from "@/types/api";
import { OIDCPage } from "./OIDCPage";

// An OIDC save opts out of the global toast because the open form shows the failure; Cancel
// does not wait, so a failure after the form is gone is a toast naming the configuration
// (unless the session ended: hooks/useSaveOutcome.ts), and a late success closes nothing.
// On the app's kind of client, so "once" means once and "none" means none.

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const FIRST = "provider01";
const SECOND = "provider02";
const FIRST_URL = "/api/v1/oidc/configs/oidc-01";
const CREATE_URL = "/api/v1/oidc/configs";
const REDIRECT_REFUSAL =
  "The authorization code would travel in cleartext to http://nexara.example.com/api/v1/auth/oidc/callback.";

function oidcConfig(id: string, name: string): OIDCConfig {
  return {
    id,
    name,
    enabled: true,
    issuer_url: "https://idp.example.com/realms/example",
    client_id: "nexara",
    client_secret_set: true,
    redirect_uri: "https://nexara.example.com/api/v1/auth/oidc/callback",
    scopes: ["openid", "email", "profile"],
    email_claim: "email",
    display_name_claim: "name",
    groups_claim: "groups",
    group_role_mapping: {},
    default_role_id: null,
    auto_provision: true,
    allowed_domains: [],
    allow_private_address: false,
    relaxed_step_up: false,
    linked_accounts: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

type UserEvent = ReturnType<typeof userEvent.setup>;

function renderPage() {
  return renderOnAppClient(<OIDCPage />, { router: true });
}

/** The heading of the form that is open. */
const EDIT_FORM = { name: "Edit OIDC configuration" };
const NEW_FORM = { name: "New OIDC configuration" };

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
  await user.click(
    await screen.findByRole("button", { name: "Add configuration" }),
  );
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

/** The refusal the server gives a save with a cleartext callback, which the form turns into a prompt. */
function redirectRefusal(): ApiClientError {
  return new ApiClientError(422, {
    error: "insecure_oidc_redirect_confirm_required",
    message: REDIRECT_REFUSAL,
  });
}

beforeEach(() => {
  vi.resetAllMocks();
  signIn();
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(
      path === CREATE_URL
        ? [oidcConfig("oidc-01", FIRST), oidcConfig("oidc-02", SECOND)]
        : [],
    ),
  );
  mockedPost.mockResolvedValue(oidcConfig("oidc-03", "created"));
  mockedPut.mockResolvedValue(oidcConfig("oidc-01", FIRST));
});

afterEach(() => {
  signOutForGood();
});

describe("an OIDC configuration save that settles", () => {
  it("control: closes the form when it succeeds while the form is open", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderPage();

    await saveConfig(user);
    held.resolve(oidcConfig("oidc-01", FIRST));
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

  it("control: turns the server's cleartext-callback refusal into a prompt in the form, with no toast", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await saveConfig(user);
    held.reject(redirectRefusal());

    const prompt = await screen.findByTestId("confirm-required-warning");
    expect(
      within(prompt).getByText("Callback is not encrypted"),
    ).toBeInTheDocument();
    expect(
      within(prompt).getByRole("button", {
        name: "Save with a cleartext callback",
      }),
    ).toBeInTheDocument();
    await flushInAct();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("says on edit that the stored issuer was confirmed for a private address, and does not resend it", async () => {
    const user = userEvent.setup();
    mockedList.mockResolvedValue([
      { ...oidcConfig("oidc-01", FIRST), allow_private_address: true },
    ]);
    renderPage();

    await openEdit(user);
    expect(
      screen.getByText(/Confirmed for a private network address/),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[1]).not.toHaveProperty(
      "allow_private_address",
    );
  });

  it("saves the relaxed 2FA confirmation the operator turns on", async () => {
    const user = userEvent.setup();
    renderPage();

    await openEdit(user);
    await user.click(
      screen.getByRole("switch", {
        name: /Allow relaxed 2FA confirmation for a provider that cannot report a fresh sign-in/,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[1]).toMatchObject({
      relaxed_step_up: true,
    });
  });

  it("asks to confirm an issuer on a private address, and resends the save confirmed", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPost.mockReturnValueOnce(held.promise);
    renderPage();

    await createConfig(user);
    held.reject(
      new ApiClientError(422, {
        error: "private_address_confirm_required",
        message:
          "The issuer URL resolves to 10.0.0.5, an address on a private network.",
        details: { ip: "10.0.0.5", address_kind: "private" },
      }),
    );
    expect(
      await screen.findByText(/resolves to an address on a private network/),
    ).toHaveTextContent("10.0.0.5");
    await user.click(
      screen.getByRole("button", { name: "Continue with private address" }),
    );

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(2);
    });
    expect(mockedPost.mock.calls[0]?.[1]).not.toHaveProperty(
      "allow_private_address",
    );
    expect(mockedPost.mock.calls[1]?.[1]).toMatchObject({
      allow_private_address: true,
    });
  });

  it("warns that a new issuer strands the linked accounts, and resends the save confirmed", async () => {
    const user = userEvent.setup();
    const linked = { ...oidcConfig("oidc-01", FIRST), linked_accounts: 2 };
    mockedList.mockResolvedValue([linked]);
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(422, {
        error: "oidc_issuer_change_confirm_required",
        message: "2 account(s) are linked to the current issuer.",
        details: { linked_accounts: 2 },
      }),
    );
    renderPage();

    await openEdit(user);
    const issuer = screen.getByLabelText("Issuer URL");
    await user.clear(issuer);
    await user.type(issuer, "https://idp2.example.com/realms/example");
    expect(
      screen.getByText(/2 accounts are linked to the current issuer/),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await user.click(
      await screen.findByRole("button", { name: "Change the issuer" }),
    );

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[0]?.[1]).not.toHaveProperty(
      "acknowledge_issuer_change",
    );
    expect(mockedPut.mock.calls[1]?.[1]).toMatchObject({
      acknowledge_issuer_change: true,
    });
  });

  it("resets every SSO link only after a confirm", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    vi.mocked(apiClient.delete).mockResolvedValue({ reset: 2 });
    renderPage();

    await openEdit(user);
    await user.click(
      screen.getByRole("button", { name: "Reset all SSO links" }),
    );

    expect(confirm).toHaveBeenCalledOnce();
    await waitFor(() => {
      expect(apiClient.delete).toHaveBeenCalledWith("/api/v1/oidc/links");
    });
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
        `Saving the OIDC configuration "${FIRST}" failed: ${DENIED}`,
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
        `Creating the OIDC configuration "Default" failed: ${DENIED}`,
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
      `Saving the OIDC configuration "${FIRST}" failed: ${DENIED}`,
    );
  });

  it("toasts the cleartext-callback refusal that comes after the form was cancelled", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    renderPage();

    await saveConfig(user);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    held.reject(redirectRefusal());

    await expectOneToast(
      `Saving the OIDC configuration "${FIRST}" failed: ${REDIRECT_REFUSAL}`,
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
      `Saving the OIDC configuration "${FIRST}" failed: ${DENIED}`,
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
    held.reject(redirectRefusal());

    await expectOneToast(
      `Saving the OIDC configuration "${FIRST}" failed: ${REDIRECT_REFUSAL}`,
    );
  });

  it("does not close the form of another config, opened after it, when it succeeds", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const { qc } = renderPage();

    await cancelForAnother(user);
    held.resolve(oidcConfig("oidc-01", FIRST));
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
    await user.click(
      await screen.findByRole("button", { name: "Add configuration" }),
    );
    await screen.findByRole("heading", NEW_FORM);
    held.resolve(oidcConfig("oidc-03", "created"));
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
    held.resolve(oidcConfig("oidc-01", FIRST));
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
