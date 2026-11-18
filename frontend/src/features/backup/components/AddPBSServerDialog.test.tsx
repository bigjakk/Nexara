import { beforeEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { AddPBSServerDialog } from "./AddPBSServerDialog";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

beforeEach(() => {
  vi.mocked(apiClient.list).mockResolvedValue([
    { id: "c1", name: "cluster01" },
  ]);
});

// Adding a server takes manage:pbs, and its first step (the certificate) an
// install-wide manage on cluster, pbs or veeam; a standalone server, the
// "None" cluster, takes manage:pbs install-wide.
it.each<[string, string[], string[], boolean, boolean]>([
  ["manage:pbs install-wide", ["manage:pbs"], ["manage:pbs"], true, true],
  [
    "manage:pbs on a cluster, manage:cluster install-wide",
    ["manage:pbs", "manage:cluster"],
    ["manage:cluster"],
    true,
    false,
  ],
  ["manage:pbs on a cluster only", ["manage:pbs"], [], false, false],
  ["manage:veeam alone", ["manage:veeam"], ["manage:veeam"], false, false],
])(
  "for a role holding %s",
  async (_, permissions, globalPermissions, offered, standalone) => {
    useAuthStore.setState({ user: null, permissions, globalPermissions });
    const user = setupUser();
    renderWithProviders(<AddPBSServerDialog />);

    const add = screen.queryByRole("button", { name: "Add PBS server" });
    expect(add !== null).toBe(offered);
    if (add === null) return;

    await user.click(add);
    expect(
      await screen.findByRole("option", { name: "cluster01" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "None" }) !== null).toBe(
      standalone,
    );
  },
);
