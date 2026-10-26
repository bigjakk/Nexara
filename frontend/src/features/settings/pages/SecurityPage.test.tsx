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
function serve(authSource: "local" | "ldap") {
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
// the current password for an account that has one, which a wrong one leaves the
// dialog open to retry, and nothing for an LDAP or SSO account, which the server
// holds to a recent sign-in instead.
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
    const password = await screen.findByLabelText("Current Password");
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
    expect(screen.queryByLabelText("Current Password")).not.toBeInTheDocument();
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
    expect(screen.queryByLabelText("Current Password")).not.toBeInTheDocument();
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
      await screen.findByLabelText("Current Password"),
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

  it("asks an LDAP or SSO account for no password", async () => {
    serve("ldap");
    mockedPost.mockResolvedValueOnce(secret);
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<SecurityPage />);

    await user.click(await screen.findByRole("button", { name: "Enable 2FA" }));
    expect(
      await screen.findByText(/sign-in from the last 10 minutes/),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Current Password")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      await screen.findByText(/Step 1: Scan the QR code/),
    ).toBeInTheDocument();
    expect(mockedPost).toHaveBeenCalledWith(SETUP, {});
  });
});
