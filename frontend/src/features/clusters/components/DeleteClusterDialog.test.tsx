import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { DeleteClusterDialog } from "./DeleteClusterDialog";
import type { ClusterResponse } from "@/types/api";

const deleteMock = vi.fn();

// Only the transport is replaced; the path useDeleteCluster hands it is built
// by the real apiPath.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      ...actual.apiClient,
      delete: (...args: unknown[]) => deleteMock(...args) as unknown,
    },
  };
});

// The revoke option is gated on GLOBAL manage:cluster, matching the server.
let granted: string[] = [];
vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: () => ({
    hasPermission: (action: string, resource: string) =>
      granted.includes(`${action}:${resource}`),
  }),
}));

beforeEach(() => {
  granted = ["manage:cluster", "delete:cluster"];
  deleteMock.mockReset();
  deleteMock.mockResolvedValue(undefined);
});

function cluster(over: Partial<ClusterResponse> = {}): ClusterResponse {
  return {
    id: "c1",
    name: "Prod Cluster",
    api_url: "https://pve.example.com:8006",
    token_id: "root@pam!nexara",
    tls_fingerprint: "",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    pve_version: "9.2",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    credential_source: "manual",
    ...over,
  };
}

const REVOKE = /also delete the proxmox user and api token/i;

describe("DeleteClusterDialog", () => {
  it("does not offer to revoke a credential the operator supplied", () => {
    // The pasted-in token may be shared with other tooling, and the server
    // refuses to revoke it regardless — so offering the choice would be a
    // checkbox that silently does nothing.
    renderWithProviders(
      <DeleteClusterDialog
        cluster={cluster({ credential_source: "manual" })}
        open
        onOpenChange={() => {}}
      />,
    );
    expect(screen.queryByLabelText(REVOKE)).not.toBeInTheDocument();
  });

  it("offers revocation, unticked, for a credential Nexara minted", () => {
    renderWithProviders(
      <DeleteClusterDialog
        cluster={cluster({ credential_source: "bootstrap" })}
        open
        onOpenChange={() => {}}
      />,
    );
    const box = screen.getByLabelText(REVOKE);
    expect(box).toBeInTheDocument();
    // Off by default: deleting a cluster from Nexara must not be read as
    // consent to mutate a live hypervisor's access control.
    expect(box).toHaveAttribute("data-state", "unchecked");
  });

  // The server requires global manage:cluster for revocation, while the delete
  // itself needs only delete:cluster — which can be scoped to one cluster.
  // Showing the box to someone the server will 403 turns a working delete into
  // a failed one.
  it("hides revocation from a caller who only holds delete:cluster", () => {
    granted = ["delete:cluster"];
    renderWithProviders(
      <DeleteClusterDialog
        cluster={cluster({ credential_source: "bootstrap" })}
        open
        onOpenChange={() => {}}
      />,
    );
    expect(screen.queryByLabelText(REVOKE)).not.toBeInTheDocument();
  });

  // Only the name exactly: a prefix of it, or the name in another case, is
  // not it. The server would refuse either (ClusterHandler.Delete); the
  // dialog must not send one to find out.
  it.each(["Prod", "prod cluster", "Prod Cluster "])(
    "keeps delete blocked, and sends nothing, for %j",
    async (typed) => {
      const user = userEvent.setup();
      renderWithProviders(
        <DeleteClusterDialog
          cluster={cluster({ credential_source: "bootstrap" })}
          open
          onOpenChange={() => {}}
        />,
      );
      await user.type(screen.getByPlaceholderText("Prod Cluster"), typed);
      const button = screen.getByRole("button", { name: /delete cluster/i });
      expect(button).toBeDisabled();
      await user.click(button);
      expect(deleteMock).not.toHaveBeenCalled();
    },
  );

  it("keeps delete blocked until the cluster name is typed", () => {
    renderWithProviders(
      <DeleteClusterDialog
        cluster={cluster({ credential_source: "bootstrap" })}
        open
        onOpenChange={() => {}}
      />,
    );
    expect(
      screen.getByRole("button", { name: /delete cluster/i }),
    ).toBeDisabled();
  });

  // The server deletes a cluster only when confirm is its current name,
  // exactly (ClusterHandler.Delete). The dialog already makes the operator
  // type it; what they typed is what goes out, encoded as a query value.
  it.each([
    [false, "/api/v1/clusters/c1?confirm=Prod+Cluster"],
    [true, "/api/v1/clusters/c1?confirm=Prod+Cluster&revoke_pve_credentials=1"],
  ])("sends the typed name as confirm (revoking: %s)", async (revoke, want) => {
    const user = userEvent.setup();
    renderWithProviders(
      <DeleteClusterDialog
        cluster={cluster({ credential_source: "bootstrap" })}
        open
        onOpenChange={() => {}}
      />,
    );
    const button = screen.getByRole("button", { name: /delete cluster/i });
    await user.type(
      screen.getByPlaceholderText("Prod Cluster"),
      "Prod Cluster",
    );
    if (revoke) {
      await user.click(screen.getByLabelText(REVOKE));
    }
    await user.click(button);
    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalled();
    });
    expect(deleteMock.mock.calls).toEqual([[want]]);
  });
});
