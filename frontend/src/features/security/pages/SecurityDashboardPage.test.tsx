import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { useLocation } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { makeCluster } from "@/test/cluster.fixtures";
import { makeRollingUpdateJob } from "@/test/rolling-update.fixtures";
import { SecurityDashboardPage } from "./SecurityDashboardPage";

afterEach(() => {
  vi.unstubAllGlobals();
});

/** The page with the router's current search string beside it. */
function PageWithLocation() {
  const { search } = useLocation();
  return (
    <>
      <output data-testid="search">{search}</output>
      <SecurityDashboardPage />
    </>
  );
}

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

describe("SecurityDashboardPage — job links", () => {
  const clusters = {
    items: [
      makeCluster({ id: "c1", name: "cluster01" }),
      makeCluster({ id: "c2", name: "cluster02" }),
    ],
    total: 2,
  };

  // The cluster rides in the URL beside the job: a reload of a job on the
  // second cluster used to ask the first for it and show "Job not found".
  it("opens a job on the second cluster from its link", async () => {
    stubApi({
      "/api/v1/clusters": clusters,
      "/api/v1/clusters/c2/rolling-updates/j1": makeRollingUpdateJob({
        cluster_id: "c2",
        status: "running",
      }),
      "/api/v1/clusters/c2/rolling-updates/j1/nodes": listOf([]),
    });
    renderWithProviders(<SecurityDashboardPage />, {
      router: {
        initialEntries: ["/security?tab=rolling-updates&cluster=c2&job=j1"],
      },
    });

    expect(
      await screen.findByRole("heading", { name: "Rolling update" }),
    ).toBeInTheDocument();
    expect(screen.getByText("cluster02")).toBeInTheDocument();
  });

  // A link saved before the fix names no cluster; when the default cluster
  // does not have the job, it lands on that cluster's jobs list.
  it("opens the jobs list for an old link the default cluster cannot find", async () => {
    stubApi({ "/api/v1/clusters": clusters });
    renderWithProviders(<SecurityDashboardPage />, {
      router: { initialEntries: ["/security?tab=rolling-updates&job=j1"] },
    });

    expect(await screen.findByText("Rolling update jobs")).toBeInTheDocument();
    expect(screen.queryByText("Job not found")).not.toBeInTheDocument();
  });

  // An old link's job, once found, is pinned to its cluster in the URL, so a
  // failed poll later does not throw the page back to the jobs list.
  it("keeps an old link's found job through a failed poll", async () => {
    const job = makeRollingUpdateJob({ status: "running" });
    stubApi({
      "/api/v1/clusters": clusters,
      "/api/v1/clusters/c1/rolling-updates/j1": job,
      "/api/v1/clusters/c1/rolling-updates/j1/nodes": listOf([]),
    });
    const { queryClient } = renderWithProviders(<PageWithLocation />, {
      router: { initialEntries: ["/security?tab=rolling-updates&job=j1"] },
    });
    await screen.findByRole("heading", { name: "Rolling update" });
    await waitFor(() => {
      expect(screen.getByTestId("search")).toHaveTextContent("cluster=c1");
    });

    stubApi({ "/api/v1/clusters": clusters }); // the job read now fails
    await queryClient.refetchQueries({ queryKey: ["rolling-update-job"] });
    expect(
      screen.getByRole("heading", { name: "Rolling update" }),
    ).toBeInTheDocument();
  });
});
