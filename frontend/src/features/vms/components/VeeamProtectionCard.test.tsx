import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { VeeamProtectionCard } from "./VeeamProtectionCard";
import type { VeeamGuestProtection } from "@/features/backup/types/backup";

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: { get: vi.fn() },
  };
});

const mockedGet = vi.mocked(apiClient.get);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const VM = "d0000000-0000-4000-8000-000000000001";

function protection(
  over: Partial<VeeamGuestProtection> = {},
): VeeamGuestProtection {
  return {
    protected: true,
    latest_restore_point: "2026-08-27T02:00:00Z",
    malware_status: "Clean",
    object_count: 2,
    restore_point_count: 29,
    restore_point_bytes: 53687091200,
    match_method: "smbios",
    last_run_failed: false,
    restore_points: [],
    ...over,
  };
}

describe("VeeamProtectionCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders nothing when Veeam has never seen the guest", async () => {
    mockedGet.mockResolvedValue(
      protection({ object_count: 0, protected: false }),
    );
    const { container } = renderWithProviders(
      <VeeamProtectionCard clusterId={CLUSTER} vmId={VM} />,
    );

    // Not "unprotected" — Veeam simply has nothing to say. Whether the guest
    // SHOULD be backed up is the coverage report's question, and implying a
    // verdict here would put an empty card on every guest in a PBS-only
    // estate.
    await waitFor(() => {
      expect(container).toBeEmptyDOMElement();
    });
  });

  it("renders nothing for a viewer without Veeam access", async () => {
    mockedGet.mockRejectedValue(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Insufficient permissions",
      }),
    );
    const { container } = renderWithProviders(
      <VeeamProtectionCard clusterId={CLUSTER} vmId={VM} />,
    );

    await waitFor(() => {
      expect(container).toBeEmptyDOMElement();
    });
  });

  it("flags a name match as the guess it is", async () => {
    mockedGet.mockResolvedValue(protection({ match_method: "name" }));
    renderWithProviders(<VeeamProtectionCard clusterId={CLUSTER} vmId={VM} />);

    // A rebuilt host reuses its name, so the backup behind a name match may be
    // of the machine it replaced. Presenting it identically to a verified
    // match is the single most misleading thing this card could do.
    expect(await screen.findByText(/name match/i)).toBeInTheDocument();
  });

  it("does not flag a verified SMBIOS match", async () => {
    mockedGet.mockResolvedValue(protection({ match_method: "smbios" }));
    renderWithProviders(<VeeamProtectionCard clusterId={CLUSTER} vmId={VM} />);

    expect(await screen.findByText(/verified match/i)).toBeInTheDocument();
    expect(screen.queryByText(/name match/i)).not.toBeInTheDocument();
  });

  it("distinguishes 'knows the guest, holds nothing' from protected", async () => {
    mockedGet.mockResolvedValue(
      protection({
        protected: false,
        restore_point_count: 0,
        latest_restore_point: null,
      }),
    );
    renderWithProviders(<VeeamProtectionCard clusterId={CLUSTER} vmId={VM} />);

    // The worst state there is, and the one most easily mistaken for healthy:
    // the backup object still exists, so a naive check sees a guest Veeam
    // "covers".
    // Two matches by design — the badge and the explanation beneath it — and
    // both matter: the badge is what a scanning eye catches, the sentence is
    // what tells an operator the backup object still exists.
    expect(await screen.findByText("No restore points")).toBeInTheDocument();
    expect(screen.getByText(/every one has been pruned/i)).toBeInTheDocument();
  });

  it("surfaces a malware verdict on the newest point", async () => {
    mockedGet.mockResolvedValue(protection({ malware_status: "Suspicious" }));
    renderWithProviders(<VeeamProtectionCard clusterId={CLUSTER} vmId={VM} />);

    expect(await screen.findByText("Suspicious")).toBeInTheDocument();
  });
});
