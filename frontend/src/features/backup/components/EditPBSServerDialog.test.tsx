import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { EditPBSServerDialog } from "./EditPBSServerDialog";
import type { PBSServer } from "../types/backup";

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    put: vi.fn(),
    get: vi.fn().mockResolvedValue({ items: [], total: 0 }),
  },
}));

const mockedPut = vi.mocked(apiClient.put);

const SERVER = {
  id: "pbs-1",
  name: "PBS Primary",
  api_url: "https://pbs.example.com:8007",
  token_id: "nexara@pbs!token",
  tls_fingerprint: "AA:BB:CC:DD",
  cluster_id: null,
} as unknown as PBSServer;

// mock.calls[0] is possibly-undefined under strict mode; fail loudly rather
// than letting an assertion pass vacuously against undefined.
function sentBody(): Record<string, unknown> {
  const call = mockedPut.mock.calls[0];
  if (call === undefined) {
    throw new Error("expected a PUT to have been made");
  }
  return call[1] as Record<string, unknown>;
}

function render() {
  renderWithProviders(
    <EditPBSServerDialog server={SERVER} open onOpenChange={() => undefined} />,
  );
  return userEvent.setup();
}

describe("EditPBSServerDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedPut.mockResolvedValue(SERVER);
  });

  it("keeps the token secret optional while the address is unchanged", async () => {
    const user = render();

    const secret = screen.getByLabelText(/api token secret/i);
    expect(secret).not.toBeRequired();

    await user.clear(screen.getByLabelText(/server name/i));
    await user.type(screen.getByLabelText(/server name/i), "PBS Renamed");
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(sentBody()).not.toHaveProperty("token_secret");
  });

  // The stored token is only ever sent to the address it was saved for. A
  // caller who may edit the URL but has never seen the secret must not be able
  // to re-point the row at a host they control and have the collector deliver
  // the credential there — so the field becomes mandatory, and the dialog says
  // why rather than failing an unexplained validation.
  it("requires the token secret once the address moves, and explains why", async () => {
    const user = render();

    const apiUrl = screen.getByLabelText(/api url/i);
    await user.clear(apiUrl);
    await user.type(apiUrl, "https://attacker.example.net:8007");

    expect(screen.getByLabelText(/api token secret/i)).toBeRequired();
    expect(
      screen.getByText(/only ever sent to the address it was saved for/i),
    ).toBeInTheDocument();
  });

  it("submits the re-entered secret alongside the new address", async () => {
    const user = render();

    const apiUrl = screen.getByLabelText(/api url/i);
    await user.clear(apiUrl);
    await user.type(apiUrl, "https://pbs2.example.com:8007");
    await user.type(
      screen.getByLabelText(/api token secret/i),
      "re-typed-secret",
    );
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(sentBody()).toMatchObject({
      api_url: "https://pbs2.example.com:8007",
      token_secret: "re-typed-secret",
    });
  });

  // Same origin, different listener: the client concatenates its API path onto
  // the stored address, so a path or query change routes the credential
  // somewhere new without the host ever changing.
  it.each([
    ["a path", "https://pbs.example.com:8007/attacker-path"],
    ["a query string", "https://pbs.example.com:8007?x="],
  ])("treats %s on the same host as an address change", async (_label, url) => {
    const user = render();

    const apiUrl = screen.getByLabelText(/api url/i);
    await user.clear(apiUrl);
    await user.type(apiUrl, url);

    expect(screen.getByLabelText(/api token secret/i)).toBeRequired();
  });
});
