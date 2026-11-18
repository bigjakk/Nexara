import { expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { ServerCrash } from "lucide-react";
import { renderWithProviders } from "@/test/test-utils";
import { useAuthStore } from "@/stores/auth-store";
import { NoClustersEmptyState } from "./NoClustersEmptyState";

// Six pages told a Viewer to add a cluster, over a button the server refuses:
// only a holder of manage:cluster install-wide is asked to add one.
it.each([
  [["manage:cluster"], ["manage:cluster"], true],
  [["manage:cluster"], [], false],
])(
  "with %j, install-wide %j, asks to add a cluster: %s",
  (permissions, globalPermissions, asked) => {
    useAuthStore.setState({ user: null, permissions, globalPermissions });
    renderWithProviders(
      <NoClustersEmptyState icon={ServerCrash} description="Add one here." />,
    );
    expect(screen.queryByText("Add one here.") !== null).toBe(asked);
    expect(
      screen.queryByRole("button", { name: /add cluster/i }) !== null,
    ).toBe(asked);
    expect(
      screen.queryByText(/No Proxmox cluster has been added yet/) !== null,
    ).toBe(!asked);
  },
);
