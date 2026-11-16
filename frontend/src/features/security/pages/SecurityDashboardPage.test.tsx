import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
import { makeCluster } from "@/test/cluster.fixtures";
import { SecurityDashboardPage } from "./SecurityDashboardPage";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("SecurityDashboardPage — posture", () => {
  // A failed posture read used to leave the panel's place empty, saying nothing.
  it("says the posture could not be loaded, with a retry", async () => {
    // The posture is left unstubbed, so its read fails.
    stubApi({
      "/api/v1/clusters": { items: [makeCluster({ id: "c1" })], total: 1 },
    });
    renderWithProviders(<SecurityDashboardPage />);

    const line = await screen.findByText(
      "Could not load the security posture.",
    );
    const notice = line.closest<HTMLElement>('[role="status"]');
    expect(notice).not.toBeNull();
    expect(
      within(notice as HTMLElement).getByRole("button", { name: "Retry" }),
    ).toBeInTheDocument();
  });
});
