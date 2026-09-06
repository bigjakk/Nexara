import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { ClusterCertificateBanner } from "./ClusterCertificateBanner";
import type { ClusterResponse, HealthIssue } from "@/types/api";

// The dialog drags in the whole cluster-edit form (fingerprint fetch, SSRF
// gate, mutations). The banner's contract is only that it renders the warning
// and opens something, so stub it.
vi.mock("./EditClusterDialog", () => ({
  EditClusterDialog: ({ open }: { open: boolean }) =>
    open ? <div data-testid="edit-dialog" /> : null,
}));

const mockMutate = vi.fn();
vi.mock("../api/cluster-queries", () => ({
  useVerifyClusterCertificate: () => ({
    mutate: mockMutate,
    isPending: false,
  }),
}));

const FINGERPRINT_ISSUE: HealthIssue = {
  type: "tls_fingerprint_changed",
  severity: "err",
  scope: "cluster",
  target: "",
  summary: "TLS certificate changed",
  // Mirrors what buildAllClusterIssues actually emits, so a change to the
  // server's wording shows up here rather than silently drifting.
  detail:
    "pve-01 is presenting a different certificate than the one pinned for " +
    "this cluster, so live Proxmox operations fail. It now presents " +
    "0c6a3da6d6e538eaf7c88848 — confirm that on the node before accepting it.",
};

function makeCluster(issues: HealthIssue[]): ClusterResponse {
  return {
    id: "cluster-1",
    name: "cluster01",
    api_url: "https://192.0.2.10:8006/",
    token_id: "root@pam!nexara",
    tls_fingerprint: "c9f24e425dfad9b0",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    pve_version: "9.2.11",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    credential_source: "manual",
    issues,
  };
}

describe("ClusterCertificateBanner", () => {
  beforeEach(() => {
    mockMutate.mockReset();
  });

  it("renders nothing when the cluster has no issues at all", () => {
    const { container } = renderWithProviders(
      <ClusterCertificateBanner cluster={makeCluster([])} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("stays silent for unrelated issues", () => {
    // The banner is specifically about the certificate; every other health
    // signal already has its own surface.
    const cluster = makeCluster([
      {
        type: "node_offline",
        severity: "err",
        scope: "node",
        target: "pve-02",
        summary: "Node offline",
        detail: "pve-02 is offline",
      },
    ]);
    const { container } = renderWithProviders(
      <ClusterCertificateBanner cluster={cluster} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the server's summary and detail, including the new fingerprint", () => {
    renderWithProviders(
      <ClusterCertificateBanner cluster={makeCluster([FINGERPRINT_ISSUE])} />,
    );

    expect(screen.getByText("TLS certificate changed")).toBeInTheDocument();
    // The fingerprint has to reach the operator — verifying it out-of-band is
    // the entire point of the flow.
    expect(screen.getByText(/0c6a3da6d6e538eaf7c88848/)).toBeInTheDocument();
  });

  it("explains why nothing else looks broken", () => {
    // Without this line the operator has no reason to connect a blank tab to a
    // certificate, because sync keeps succeeding via the other members.
    renderWithProviders(
      <ClusterCertificateBanner cluster={makeCluster([FINGERPRINT_ISSUE])} />,
    );
    expect(screen.getByText(/other members/i)).toBeInTheDocument();
  });

  it("never tells the server which certificate to trust", async () => {
    // The security property. The banner displays a fingerprint, so the
    // tempting shortcut is to post it back — which would let anything that can
    // influence what the banner renders choose what gets pinned. Instead it
    // asks the server to decide, and the server corroborates against a live
    // handshake and the cluster's own report before pinning anything.
    const { default: userEvent } = await import("@testing-library/user-event");
    const user = userEvent.setup();

    renderWithProviders(
      <ClusterCertificateBanner cluster={makeCluster([FINGERPRINT_ISSUE])} />,
    );
    await user.click(
      screen.getByRole("button", { name: /verify certificate/i }),
    );

    expect(mockMutate).toHaveBeenCalledTimes(1);
    // mutate(variables, callbacks) — the first argument is what would carry a
    // fingerprint if anyone ever added one.
    expect(mockMutate.mock.calls[0]?.[0]).toBeUndefined();
  });

  it("keeps a manual route for the cases the server refuses", async () => {
    // The server declines to resolve a disagreement, and declines to guess
    // when it has nothing to corroborate against, so the operator still needs
    // a way in.
    const { default: userEvent } = await import("@testing-library/user-event");
    const user = userEvent.setup();

    renderWithProviders(
      <ClusterCertificateBanner cluster={makeCluster([FINGERPRINT_ISSUE])} />,
    );
    expect(screen.queryByTestId("edit-dialog")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /edit manually/i }));
    expect(screen.getByTestId("edit-dialog")).toBeInTheDocument();
  });
});
