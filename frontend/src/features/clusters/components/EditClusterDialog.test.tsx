import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { EditClusterDialog } from "./EditClusterDialog";
import type { ClusterResponse } from "@/types/api";

vi.mock("@/lib/api-client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api-client")>(
    "@/lib/api-client",
  );
  return {
    ...actual,
    apiClient: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  };
});

const mockedPut = vi.mocked(apiClient.put);
const mockedGet = vi.mocked(apiClient.get);

const CLUSTER = {
  id: "cluster-1",
  name: "Ceph",
  api_url: "https://pve.example.com:8006",
  token_id: "nexara@pve!token",
  tls_fingerprint: "AA:BB:CC:DD",
} as unknown as ClusterResponse;

function sshResetRefusal() {
  return new ApiClientError(422, {
    error: "ssh_trust_reset_confirm_required",
    message:
      "Node addresses are learned from this API, so the stored SSH credential and every pinned host key were entrusted to machines this cluster is leaving.",
    details: { clears_ssh_credential: true, clears_pinned_hosts: 2 },
  });
}

function render() {
  renderWithProviders(
    <EditClusterDialog cluster={CLUSTER} open onOpenChange={() => undefined} />,
  );
  return userEvent.setup();
}

async function moveAddressAndSave(user: ReturnType<typeof userEvent.setup>) {
  const apiUrl = screen.getByLabelText(/api url/i);
  await user.clear(apiUrl);
  await user.type(apiUrl, "https://pve2.example.com:8006");
  await user.type(screen.getByLabelText(/token secret/i), "re-typed-secret");
  await user.click(screen.getByRole("button", { name: /^save$/i }));
}

describe("EditClusterDialog SSH trust reset", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // The SSH credentials lookup needs manage:ssh_credentials. Default to the
    // caller NOT having it, which is the case that used to dead-end.
    mockedGet.mockRejectedValue(
      new ApiClientError(403, { error: "forbidden", message: "denied" }),
    );
  });

  // The confirmation is driven by the server's refusal, not by a query the
  // caller may not be permitted to make. Driving it from the query left a
  // manage:cluster holder without manage:ssh_credentials staring at a 422 with
  // no control anywhere that could satisfy it — address changes were
  // impossible from the UI for that role.
  it("offers the confirm even when the SSH credentials query is forbidden", async () => {
    mockedPut.mockRejectedValueOnce(sshResetRefusal());
    const user = render();

    await moveAddressAndSave(user);

    expect(
      await screen.findByRole("button", { name: /clear ssh trust and save/i }),
    ).toBeInTheDocument();
  });

  it("re-submits with the acknowledgement once confirmed", async () => {
    mockedPut.mockRejectedValueOnce(sshResetRefusal());
    const user = render();

    await moveAddressAndSave(user);

    const first = mockedPut.mock.calls[0];
    if (first === undefined) throw new Error("expected a first PUT");
    expect(first[1]).not.toHaveProperty("acknowledge_ssh_trust_reset");

    mockedPut.mockResolvedValueOnce({ cluster: CLUSTER });
    await user.click(
      await screen.findByRole("button", { name: /clear ssh trust and save/i }),
    );

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    const second = mockedPut.mock.calls[1];
    if (second === undefined) throw new Error("expected a second PUT");
    expect(second[1]).toMatchObject({
      api_url: "https://pve2.example.com:8006",
      acknowledge_ssh_trust_reset: true,
    });
  });

  // Attaching the acknowledgement pre-emptively would make the backend gate
  // decorative — the operator would never see what they were accepting.
  it("never sends the acknowledgement unprompted", async () => {
    mockedPut.mockResolvedValueOnce({ cluster: CLUSTER });
    const user = render();

    await moveAddressAndSave(user);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    const call = mockedPut.mock.calls[0];
    if (call === undefined) throw new Error("expected a PUT");
    expect(call[1]).not.toHaveProperty("acknowledge_ssh_trust_reset");
  });

  it("backs out of the confirm without saving", async () => {
    mockedPut.mockRejectedValueOnce(sshResetRefusal());
    const user = render();

    await moveAddressAndSave(user);
    const confirmButton = await screen.findByRole("button", {
      name: /clear ssh trust and save/i,
    });

    // The Cancel inside the warning panel, not the dialog's own footer button.
    const panel = screen.getByTestId("confirm-required-warning");
    expect(panel).toContainElement(confirmButton);
    await user.click(within(panel).getByRole("button", { name: /^cancel$/i }));

    expect(
      screen.queryByRole("button", { name: /clear ssh trust and save/i }),
    ).not.toBeInTheDocument();
    // Still the single refused attempt — backing out must not re-submit.
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  // The confirm panel is mounted while the acknowledged re-submit is in flight,
  // and the error banner is gated on the panel being absent. A non-gate failure
  // on that second attempt therefore had nowhere to go — and that failure can
  // be a 500 raised AFTER the SSH credential was already deleted, which is the
  // one outcome the operator most needs to be told about.
  it("surfaces a non-gate failure on the acknowledged re-submit", async () => {
    mockedPut.mockRejectedValueOnce(sshResetRefusal());
    const user = render();

    await moveAddressAndSave(user);
    const confirmButton = await screen.findByRole("button", {
      name: /clear ssh trust and save/i,
    });

    mockedPut.mockRejectedValueOnce(
      new ApiClientError(500, {
        error: "internal",
        message: "Failed to clear cluster SSH credentials",
      }),
    );
    await user.click(confirmButton);

    expect(
      await screen.findByText(/failed to clear cluster ssh credentials/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId("confirm-required-warning"),
    ).not.toBeInTheDocument();
  });
});
