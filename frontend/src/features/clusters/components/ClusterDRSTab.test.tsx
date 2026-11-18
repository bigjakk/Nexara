import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { grantPermissions } from "@/test/permissions";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { ClusterDRSTab } from "./ClusterDRSTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

/** What each read answers: the DRS config and one Nexara rule. */
const READS: Record<string, unknown> = {
  "/api/v1/clusters/c1/drs/config": {
    mode: "advisory",
    weights: { cpu: 0.5, memory: 0.5 },
    imbalance_threshold: 0.25,
    eval_interval_seconds: 300,
    boot_holdoff_minutes: 10,
    include_containers: false,
    exclude_veeam_workers: true,
  },
  "/api/v1/clusters/c1/drs/rules": [
    {
      id: "r1",
      cluster_id: "c1",
      rule_type: "affinity",
      vm_ids: [101, 102],
      node_names: [],
      enabled: true,
      source: "manual",
      created_at: "",
      updated_at: "",
    },
  ],
  "/api/v1/clusters/c1/drs/ha-rules": [],
};

beforeEach(() => {
  vi.clearAllMocks();
  const read = (path: string) =>
    path in READS
      ? Promise.resolve(READS[path])
      : Promise.reject(new Error(`no read for ${path}`));
  vi.mocked(apiClient.get).mockImplementation(read);
  vi.mocked(apiClient.list).mockImplementation(read as never);
});

// Running an evaluation, creating a rule and deleting one all take manage:drs.
describe("ClusterDRSTab", () => {
  it.each([
    { granted: [] as string[], offered: false },
    { granted: ["manage:drs"], offered: true },
  ])(
    "offers Run evaluation, Add rule and Delete with $granted: $offered",
    async ({ granted, offered }) => {
      grantPermissions(...granted);
      const user = setupUser();
      renderWithProviders(<ClusterDRSTab clusterId="c1" />);

      await screen.findByText("DRS configuration");
      expect(
        screen.queryByRole("button", { name: "Run evaluation" }) !== null,
      ).toBe(offered);

      await user.click(screen.getByRole("button", { name: "Rules" }));
      await screen.findByText("101, 102");
      expect(screen.queryByRole("button", { name: "Add rule" }) !== null).toBe(
        offered,
      );
      expect(
        screen.queryByRole("button", {
          name: "Delete affinity rule for 101, 102",
        }) !== null,
      ).toBe(offered);
    },
  );
});
