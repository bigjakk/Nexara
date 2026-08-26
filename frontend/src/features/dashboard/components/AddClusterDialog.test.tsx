import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { AddClusterDialog } from "./AddClusterDialog";
import type { ApiError } from "@/types/api";

// ApiClientError is kept real: the dialog branches on `instanceof`, so a stub
// class would make every error fall through to the default message and the
// tests below would pass without exercising anything.
vi.mock("@/lib/api-client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api-client")>();
  return {
    ...actual,
    apiClient: { post: vi.fn(), list: vi.fn(), put: vi.fn(), delete: vi.fn() },
  };
});

const mockedPost = vi.mocked(apiClient.post);

const TRUSTED_CERT = { fingerprint: "AA:BB:CC", self_signed: false };

function apiError(status: number, body: ApiError) {
  return new ApiClientError(status, body);
}

/** Walk the dialog from closed to the credential step. */
async function openToCredentialStep(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /add cluster/i }));
  await user.type(screen.getByLabelText(/cluster name/i), "Prod");
  await user.type(screen.getByLabelText(/api url/i), "https://pve.example.com:8006");
  await user.click(screen.getByRole("button", { name: /^connect$/i }));
  await screen.findByText(/trusted certificate/i);
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "isSecureContext", {
    value: true,
    configurable: true,
  });
  mockedPost.mockImplementation((path: string) => {
    if (path.endsWith("/fetch-fingerprint")) {
      return Promise.resolve(TRUSTED_CERT);
    }
    return Promise.reject(new Error(`unexpected POST ${path}`));
  });
});

describe("AddClusterDialog", () => {
  it("defaults to letting Nexara create the token, and hides the paste fields", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    expect(screen.getByLabelText(/proxmox password/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/api token secret/i)).not.toBeInTheDocument();
  });

  it("swaps to the paste-a-token fields when that mode is chosen", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    await user.click(screen.getByRole("tab", { name: /i have a token/i }));

    expect(screen.getByLabelText(/api token secret/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/proxmox password/i)).not.toBeInTheDocument();
  });

  it("gates the password behind a confirmation when the page is not on HTTPS", async () => {
    // The password belongs to a privileged human account that is very likely
    // reused elsewhere; over plain http it travels in the clear.
    Object.defineProperty(window, "isSecureContext", {
      value: false,
      configurable: true,
    });
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    expect(screen.getByText(/not on https/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/proxmox password/i)).toBeDisabled();

    await user.click(screen.getByLabelText(/i trust this network/i));
    expect(screen.getByLabelText(/proxmox password/i)).toBeEnabled();
  });

  it("reveals the one-time code field when Proxmox asks for a second factor", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    expect(screen.queryByLabelText(/one-time code/i)).not.toBeInTheDocument();

    mockedPost.mockImplementation((path: string) => {
      if (path.endsWith("/fetch-fingerprint")) return Promise.resolve(TRUSTED_CERT);
      return Promise.reject(
        apiError(422, { error: "tfa_required", message: "root@pam has two-factor authentication enabled." }),
      );
    });

    await user.type(screen.getByLabelText(/proxmox password/i), "hunter2");
    await user.click(screen.getByRole("button", { name: /create token/i }));

    // The password stays put so the operator only has to add the code.
    expect(await screen.findByLabelText(/one-time code/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/proxmox password/i)).toHaveValue("hunter2");
  });

  it("explains a token-name collision instead of silently picking another name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    mockedPost.mockImplementation((path: string) => {
      if (path.endsWith("/fetch-fingerprint")) return Promise.resolve(TRUSTED_CERT);
      return Promise.reject(
        apiError(409, {
          error: "token_exists",
          message: "The API token nexara@pve!nexara already exists on this cluster.",
        }),
      );
    });

    await user.type(screen.getByLabelText(/proxmox password/i), "hunter2");
    await user.click(screen.getByRole("button", { name: /create token/i }));

    expect(await screen.findByText(/already exists on this cluster/i)).toBeInTheDocument();
    expect(screen.getByText(/will not quietly create a second one/i)).toBeInTheDocument();
  });

  it("reports what was created on the cluster, without ever showing a secret", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    mockedPost.mockImplementation((path: string) => {
      if (path.endsWith("/fetch-fingerprint")) return Promise.resolve(TRUSTED_CERT);
      return Promise.resolve({
        cluster: { id: "c1", name: "Prod", credential_source: "bootstrap" },
        connectivity: { reachable: true, message: "ok" },
        bootstrap: {
          token_id: "nexara@pve!nexara",
          steps: [
            { step: "user", status: "created", detail: "nexara@pve" },
            { step: "acl", status: "created", detail: "Administrator on /" },
            { step: "token", status: "created", detail: "nexara@pve!nexara" },
            { step: "verify", status: "verified", detail: "authenticated with the new token" },
          ],
        },
      });
    });

    await user.type(screen.getByLabelText(/proxmox password/i), "hunter2");
    await user.click(screen.getByRole("button", { name: /create token/i }));

    expect(await screen.findByText(/credential created/i)).toBeInTheDocument();
    expect(screen.getByText("nexara@pve!nexara")).toBeInTheDocument();
    expect(screen.getByText(/Administrator on \//)).toBeInTheDocument();
    expect(screen.getByText(/never sent to this browser/i)).toBeInTheDocument();

    // Deliberately NOT asserted here: "the password field is gone" and "the
    // password is absent from textContent" are both true on the summary screen
    // no matter what the code does — the form is unmounted, and an input's value
    // is never part of textContent. Credential lifetime is covered by
    // "clears the typed password ..." below, which can actually fail.
  });

  // Modest but real: reopening after a successful mint must not re-present the
  // spent password. It does NOT prove the password left the mutation cache at
  // success time — that is not observable from outside the component, and
  // overclaiming is exactly how the assertion this replaced ended up vacuous.
  it("starts from a clean credential form when reopened after a mint", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    mockedPost.mockImplementation((path: string) => {
      if (path.endsWith("/fetch-fingerprint")) return Promise.resolve(TRUSTED_CERT);
      return Promise.resolve({
        cluster: { id: "c1", name: "Prod", credential_source: "bootstrap" },
        connectivity: { reachable: true, message: "ok" },
        bootstrap: { token_id: "nexara@pve!nexara", steps: [] },
      });
    });

    const field = screen.getByLabelText(/proxmox password/i);
    await user.type(field, "hunter2");
    expect(field).toHaveValue("hunter2");

    await user.click(screen.getByRole("button", { name: /create token/i }));
    await screen.findByText(/credential created/i);

    // Reopening re-mounts the form; the spent password must not reappear.
    await user.click(screen.getByRole("button", { name: /done/i }));
    await user.click(screen.getByRole("button", { name: /add cluster/i }));
    await user.type(screen.getByLabelText(/cluster name/i), "Again");
    await user.type(screen.getByLabelText(/api url/i), "https://pve.example.com:8006");
    await user.click(screen.getByRole("button", { name: /^connect$/i }));
    await screen.findByText(/trusted certificate/i);

    expect(screen.getByLabelText(/proxmox password/i)).toHaveValue("");
  });

  // Regression test for a bug found in a real browser: Chrome autofilled the
  // operator's saved email and password into these fields, and they would have
  // been POSTed to the server as a Proxmox API token. Chrome anchors a
  // saved-login fill on an adjacent type=password input, so "new-password" on
  // the secret is what actually breaks the pairing — "off" alone is ignored.
  it("tells password managers to keep out of the credential fields", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    const password = screen.getByLabelText(/proxmox password/i);
    expect(password).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText(/proxmox username/i)).toHaveAttribute("autocomplete", "off");

    await user.click(screen.getByRole("tab", { name: /i have a token/i }));
    expect(screen.getByLabelText(/api token secret/i)).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText(/api token id/i)).toHaveAttribute("autocomplete", "off");
  });

  it("clears a failed submit when the operator switches credential mode", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddClusterDialog />);
    await openToCredentialStep(user);

    mockedPost.mockImplementation((path: string) => {
      if (path.endsWith("/fetch-fingerprint")) return Promise.resolve(TRUSTED_CERT);
      return Promise.reject(
        apiError(422, { error: "bootstrap_auth_failed", message: "Proxmox rejected that username or password." }),
      );
    });

    await user.type(screen.getByLabelText(/proxmox password/i), "wrong");
    await user.click(screen.getByRole("button", { name: /create token/i }));
    expect(await screen.findByText(/rejected that username or password/i)).toBeInTheDocument();

    // Both bootstrapError and the mutation's own error render, so clearing one
    // is not enough — the message would reappear from the other element.
    await user.click(screen.getByRole("tab", { name: /i have a token/i }));
    await waitFor(() => {
      expect(
        screen.queryByText(/rejected that username or password/i),
      ).not.toBeInTheDocument();
    });
  });
});
