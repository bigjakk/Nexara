import { expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { ApiClientError, apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { ADMIN } from "@/test/fake-server";
import { createAppQueryClient } from "@/test/app-query-client";
import { renderWithProviders } from "@/test/test-utils";
import { ClusterCertificateBanner } from "./ClusterCertificateBanner";
import type { ClusterResponse } from "@/types/api";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());
vi.mock("./EditClusterDialog", () => ({ EditClusterDialog: () => null }));

// A refused verify is reported once, by the app's error toast: a toast of the
// banner's own was raised beside it, as the net cannot see mutate()'s onError.
it("reports a refused verify once", async () => {
  useAuthStore.setState({ user: ADMIN, isAuthenticated: true });
  vi.mocked(apiClient.post).mockRejectedValue(
    new ApiClientError(409, { error: "conflict", message: "They disagree" }),
  );
  const cluster = {
    id: "cluster-1",
    issues: [{ type: "tls_fingerprint_changed", summary: "", detail: "" }],
  } as unknown as ClusterResponse;
  renderWithProviders(<ClusterCertificateBanner cluster={cluster} />, {
    client: createAppQueryClient(),
  });

  await userEvent.click(screen.getByRole("button", { name: /verify/i }));

  await waitFor(() => {
    expect(toast.error).toHaveBeenCalledWith("They disagree");
  });
  expect(toast.error).toHaveBeenCalledTimes(1);
});
