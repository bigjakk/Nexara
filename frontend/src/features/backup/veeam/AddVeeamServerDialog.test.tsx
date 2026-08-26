import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { AddVeeamServerDialog } from "./AddVeeamServerDialog";

vi.mock("@/lib/api-client", () => ({
  apiClient: { post: vi.fn() },
}));

const mockedPost = vi.mocked(apiClient.post);

const SELF_SIGNED = {
  fingerprint: "AA:BB:CC:DD",
  self_signed: true,
};

async function openAndFill(url: string) {
  const user = userEvent.setup();
  renderWithProviders(<AddVeeamServerDialog />);
  await user.click(screen.getByRole("button", { name: /add veeam server/i }));

  await user.type(screen.getByLabelText(/server name/i), "Veeam Primary");
  await user.type(screen.getByLabelText(/rest api url/i), url);
  await user.type(screen.getByLabelText(/^username$/i), "administrator");
  await user.type(screen.getByLabelText(/^password$/i), "correct-horse");
  return user;
}

describe("AddVeeamServerDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("states the version and edition requirements before anything is typed", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AddVeeamServerDialog />);
    await user.click(screen.getByRole("button", { name: /add veeam server/i }));

    expect(screen.getByText(/13\.1 or newer/i)).toBeInTheDocument();
    expect(screen.getByText(/Enterprise Plus/i)).toBeInTheDocument();
  });

  it("requires the operator to confirm a self-signed fingerprint", async () => {
    mockedPost.mockResolvedValue(SELF_SIGNED);
    const user = await openAndFill("https://vbr.example.com:9419");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    expect(
      await screen.findByText("Self-Signed Certificate"),
    ).toBeInTheDocument();
    expect(screen.getByText("AA:BB:CC:DD")).toBeInTheDocument();

    const add = screen.getByRole("button", { name: /add server/i });
    expect(add).toBeDisabled();

    await user.click(
      screen.getByLabelText(/I have verified this fingerprint/i),
    );
    expect(add).toBeEnabled();
  });

  it("accepts a CA-signed certificate without a confirmation checkbox", async () => {
    mockedPost.mockResolvedValue({ fingerprint: "11:22", self_signed: false });
    const user = await openAndFill("https://vbr.example.com:9419");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    expect(await screen.findByText("Trusted Certificate")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /add server/i })).toBeEnabled();
    expect(
      screen.queryByLabelText(/I have verified this fingerprint/i),
    ).not.toBeInTheDocument();
  });

  // A fingerprint belongs to the host it was fetched from. If the address is
  // edited while the fetch is in flight, showing the result would ask the
  // operator to attest to a certificate for a host they are no longer naming.
  it("discards a fingerprint whose address was edited mid-fetch", async () => {
    let resolveFetch: ((value: typeof SELF_SIGNED) => void) | undefined;
    mockedPost.mockReturnValue(
      new Promise<typeof SELF_SIGNED>((resolve) => {
        resolveFetch = resolve;
      }),
    );

    const user = await openAndFill("https://vbr.example.com:9419");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    // Still on the form while the fetch is pending — change the address.
    await user.type(screen.getByLabelText(/rest api url/i), "9");

    resolveFetch?.(SELF_SIGNED);

    // Wait on something the resolution itself changes, so the assertions below
    // cannot race the flush: the submit button leaves its "Connecting…" state
    // in the same update that would have set the fingerprint. Were the stale
    // pin accepted we would be on the certificate step by now, and there would
    // be no Continue button to find.
    expect(
      await screen.findByRole("button", { name: /continue/i }),
    ).toBeInTheDocument();

    expect(screen.queryByText("AA:BB:CC:DD")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Self-Signed Certificate"),
    ).not.toBeInTheDocument();
  });
});
