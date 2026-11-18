import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { PoolCreateDialog } from "./PoolCreateDialog";

describe("PoolCreateDialog", () => {
  // Creating a pool takes manage:ceph on the cluster; view:ceph shows the tab.
  it.each([
    { perms: ["view:ceph"], can: false },
    { perms: ["view:ceph", "manage:ceph"], can: true },
  ])(
    "offers Create pool only to a role that may create one: $perms",
    ({ perms, can }) => {
      grantPermissions(...perms);
      renderWithProviders(<PoolCreateDialog clusterId="c1" osdCount={3} />);
      expect(
        screen.queryByRole("button", { name: "Create pool" }) !== null,
      ).toBe(can);
    },
  );
});
