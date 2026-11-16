import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { HealthIssue } from "@/types/api";
import { AcceptClusterName } from "./AcceptClusterName";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const CLUSTER = "cccccccc-0000-0000-0000-000000000011";
// Worded as handlers/health.go words it.
const ISSUE: HealthIssue = {
  type: "cluster_name_changed",
  severity: "warn",
  scope: "cluster",
  target: "",
  summary: "Cluster name changed",
  detail: `The API URL now reaches a cluster named cluster02, not cluster01, so Nexara removes no node from this cluster. If the cluster was renamed or this entry was moved to it on purpose, accept the new name (POST /api/v1/clusters/${CLUSTER}/accept-cluster-name). Otherwise point the API URL back at cluster01.`,
};

beforeEach(() => {
  vi.clearAllMocks();
  useAuthStore.setState({ permissions: ["manage:cluster"] });
});

describe("AcceptClusterName", () => {
  // The only way to accept a new cluster name was the API. It asks first,
  // naming both clusters and the nodes it removes, and sends the name shown;
  // a 409 (another name reported since) is said in the dialog.
  it.each([
    { answer: () => Promise.resolve({}), stays: null },
    {
      answer: () =>
        Promise.reject(
          new ApiClientError(409, {
            error: "conflict",
            message: "This cluster does not report that name now.",
          }),
        ),
      stays: "This cluster does not report that name now.",
    },
  ])("accepts cluster02 once confirmed ($stays)", async ({ answer, stays }) => {
    vi.mocked(apiClient.post).mockImplementation(answer);
    const user = userEvent.setup();
    renderWithProviders(
      <AcceptClusterName clusterId={CLUSTER} issue={ISSUE} />,
    );

    await user.click(screen.getByRole("button", { name: "Accept new name" }));
    const dialog = screen.getByRole("dialog", {
      name: "Accept the cluster name cluster02?",
    });
    expect(dialog).toHaveTextContent(
      "Nexara then removes the nodes of cluster01 that cluster02 does not list",
    );
    expect(apiClient.post).not.toHaveBeenCalled();

    await user.click(
      within(dialog).getByRole("button", { name: "Accept new name" }),
    );

    expect(apiClient.post).toHaveBeenCalledWith(
      `/api/v1/clusters/${CLUSTER}/accept-cluster-name`,
      { cluster_name: "cluster02" },
    );
    if (stays === null) {
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).toBeNull();
      });
    } else {
      expect(await within(dialog).findByRole("alert")).toHaveTextContent(stays);
    }
  });

  it("is not offered without manage:cluster", () => {
    useAuthStore.setState({ permissions: ["view:cluster"] });
    renderWithProviders(
      <AcceptClusterName clusterId={CLUSTER} issue={ISSUE} />,
    );
    expect(screen.queryByRole("button")).toBeNull();
  });
});
