import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { renderWithProviders } from "@/test/test-utils";
import { createAppQueryClient } from "@/test/app-query-client";
import { signInAsAdmin, signOutForGood } from "@/test/late-toast-sessions";
import { ApiClientError, apiClient } from "@/lib/api-client";
import { NutServerDialog } from "./NutServerDialog";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const privateAddress = () =>
  new ApiClientError(422, {
    error: "private_address_confirm_required",
    message: "private address",
    details: { ip: "10.0.0.5" },
  });

async function fillAndAdd(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Open" }));
  await user.type(screen.getByLabelText("Name"), "nut01");
  await user.type(screen.getByLabelText("Host"), "nas.example.com");
  await user.click(screen.getByRole("button", { name: "Add server" }));
}

describe("NutServerDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    signOutForGood();
  });

  // A NUT server on the LAN is the usual case, so the private-address refusal
  // is a prompt that resends with the confirmation, not a dead end.
  it("asks before saving a private address, then resends with the confirmation", async () => {
    vi.mocked(apiClient.post).mockRejectedValueOnce(privateAddress()).mockResolvedValueOnce({});
    const user = userEvent.setup();
    renderWithProviders(<NutServerDialog trigger={<button type="button">Open</button>} />);

    await fillAndAdd(user);
    await user.click(await screen.findByRole("button", { name: /continue with private address/i }));

    expect(vi.mocked(apiClient.post)).toHaveBeenLastCalledWith(
      expect.anything(),
      expect.objectContaining({ name: "nut01", host: "nas.example.com", allow_private_address: true }),
    );
  });

  // On the app's own client, whose net toasts a failed mutation: the dialog
  // shows a refusal itself, so it must not be toasted too, and a save that
  // fails after the dialog closed is toasted once, naming the server.
  it("reports a failure once: inline while open, as a toast once closed", async () => {
    signInAsAdmin();
    let fail: (e: unknown) => void = () => undefined;
    vi.mocked(apiClient.post)
      .mockRejectedValueOnce(privateAddress())
      .mockReturnValueOnce(new Promise((_, reject) => { fail = reject; }));
    const user = userEvent.setup();
    renderWithProviders(<NutServerDialog trigger={<button type="button">Open</button>} />, {
      client: createAppQueryClient(),
    });

    await fillAndAdd(user);
    await user.click(await screen.findByRole("button", { name: /continue with private address/i }));
    expect(toast.error).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    fail(new ApiClientError(502, { error: "nut_unreachable", message: "Could not read from the NUT server" }));
    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledTimes(1);
    });
    expect(toast.error).toHaveBeenCalledWith(
      "Adding NUT server nut01 failed: Could not read from the NUT server",
    );
  });
});
