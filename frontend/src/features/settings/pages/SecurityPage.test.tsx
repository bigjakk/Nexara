import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { SecurityPage } from "./SecurityPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);

const SETUP = "/api/v1/auth/totp/setup";

/** The page's reads, for an account that signs in as authSource says. */
function serve(authSource: "local" | "ldap" | "oidc") {
  mockedGet.mockImplementation((path) =>
    Promise.resolve(
      String(path) === "/api/v1/auth/me"
        ? {
            id: "a0000000-0000-4000-8000-000000000001",
            email: "alice@example.com",
            display_name: "Alice",
            role: "user",
            auth_source: authSource,
            totp_enabled: false,
            created_at: "2026-01-01T00:00:00Z",
          }
        : { enabled: false, recovery_codes_remaining: 0 },
    ),
  );
  mockedList.mockResolvedValue([]);
}

const secret = { secret: "JBSWY3DPEHPK3PXP", otpauth_url: "otpauth://totp/x" };

// Enable 2FA asks the owner to prove it is them before the secret is shown:
// the current password for a local account, which a wrong one leaves the dialog
// open to retry, the directory password
// for an LDAP account, and for an SSO account a round trip through its identity
// provider, after which the setup continues by itself.
describe("SecurityPage: beginning a 2FA setup", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("sends the current password, and keeps the dialog open while it is wrong", async () => {
    serve("local");
    mockedPost
      .mockRejectedValueOnce(
        new ApiClientError(403, {
          error: "forbidden",
          message: "Current password is incorrect",
        }),
      )
      .mockResolvedValueOnce(secret);
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<SecurityPage />);

    await user.click(await screen.findByRole("button", { name: "Enable 2FA" }));
    const password = await screen.findByLabelText("Current password");
    await user.type(password, "wrong-guess");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      await screen.findByText("Current password is incorrect"),
    ).toBeInTheDocument();
    expect(mockedPost).toHaveBeenLastCalledWith(SETUP, {
      current_password: "wrong-guess",
    });

    await user.clear(password);
    await user.type(password, "right-password");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      await screen.findByText(/Step 1: Scan the QR code/),
    ).toBeInTheDocument();
    expect(mockedPost).toHaveBeenLastCalledWith(SETUP, {
      current_password: "right-password",
    });
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
  });

  it("asks for nothing until the account is known", async () => {
    serve("local");
    const known = mockedGet.getMockImplementation();
    mockedGet.mockImplementation((path) =>
      String(path) === "/api/v1/auth/me"
        ? new Promise(() => {})
        : (known?.(path) ?? Promise.resolve({})),
    );
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<SecurityPage />);

    await user.click(await screen.findByRole("button", { name: "Enable 2FA" }));
    expect(
      await screen.findByText("Loading your account…"),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("drops an answer that arrives after the dialog was closed", async () => {
    serve("local");
    let answer: (value: unknown) => void = () => {};
    mockedPost.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<SecurityPage />);

    await user.click(await screen.findByRole("button", { name: "Enable 2FA" }));
    await user.type(
      await screen.findByLabelText("Current password"),
      "right-password",
    );
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await act(async () => {
      answer(secret);
      await Promise.resolve();
    });

    expect(
      screen.queryByText(/Step 1: Scan the QR code/),
    ).not.toBeInTheDocument();
  });

  it("asks an LDAP account for its directory password", async () => {
    serve("ldap");
    mockedPost.mockResolvedValueOnce(secret);
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<SecurityPage />);

    await user.click(await screen.findByRole("button", { name: "Enable 2FA" }));
    await user.type(
      await screen.findByLabelText("Directory password"),
      "directory-password",
    );
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      await screen.findByText(/Step 1: Scan the QR code/),
    ).toBeInTheDocument();
    expect(mockedPost).toHaveBeenCalledWith(SETUP, {
      current_password: "directory-password",
    });
  });

  it("sends an SSO account to its identity provider", async () => {
    serve("oidc");
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    mockedPost.mockResolvedValueOnce({
      redirect_url: "https://idp.example.com/auth",
    });
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<SecurityPage />);

    await user.click(await screen.findByRole("button", { name: "Enable 2FA" }));
    await user.click(
      await screen.findByRole("button", {
        name: "Confirm with your identity provider",
      }),
    );
    expect(mockedPost).toHaveBeenCalledWith(
      "/api/v1/auth/totp/setup/sso-step-up",
      {},
    );
    await vi.waitFor(() => {
      expect(assign).toHaveBeenCalledWith("https://idp.example.com/auth");
    });
    vi.unstubAllGlobals();
  });

  it.each([
    ["confirmed", "?sso_step_up=confirmed", /Step 1: Scan the QR code/, 1],
    [
      "refused",
      "?sso_step_up_error=Nothing%20was%20confirmed.",
      /Nothing was confirmed\./,
      0,
    ],
  ])(
    "picks the setup up where it was when the provider has %s",
    async (_, query, shown, setups) => {
      serve("oidc");
      mockedPost.mockResolvedValueOnce(secret);
      renderWithProviders(<SecurityPage />, {
        router: { initialEntries: ["/settings/security" + query] },
      });

      expect(await screen.findByText(shown)).toBeInTheDocument();
      expect(mockedPost).toHaveBeenCalledTimes(setups);
    },
  );
});
