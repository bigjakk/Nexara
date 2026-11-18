import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { VeeamServer } from "../types/backup";
import { VeeamServersPanel } from "./VeeamServersPanel";
import { server } from "./veeam.fixtures";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const VIEW = ["view:veeam"];
const MANAGE = ["view:veeam", "manage:veeam", "delete:veeam"];

// The panel on these registered servers, for a role with these grants (the
// global ones install-wide).
function mountPanel(
  servers: VeeamServer[],
  permissions: string[],
  globalPermissions: string[],
) {
  useAuthStore.setState({ user: null, permissions, globalPermissions });
  vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
    Promise.resolve(String(path) === "/api/v1/veeam-servers" ? servers : []),
  );
  renderWithProviders(<VeeamServersPanel />);
}

beforeEach(() => {
  vi.clearAllMocks();
});

// The registry's writes are install-wide checks: Edit takes manage:veeam,
// Delete server delete:veeam, and adding one manage:veeam.
describe("VeeamServersPanel", () => {
  it.each<[string, string[], string[], boolean]>([
    ["a viewer", VIEW, VIEW, false],
    ["a cluster's manager", MANAGE, VIEW, false],
    ["an install-wide manager", MANAGE, MANAGE, true],
  ])(
    "offers the server's Edit and Delete to %s by grant",
    async (_, permissions, globalPermissions, offered) => {
      mountPanel([server()], permissions, globalPermissions);

      expect(await screen.findByText("Veeam Primary")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Edit" }) !== null).toBe(
        offered,
      );
      expect(
        screen.queryByRole("button", {
          name: "More actions for Veeam Primary",
        }) !== null,
      ).toBe(offered);
    },
  );

  it.each<[string, string[], RegExp, RegExp]>([
    ["may add one", MANAGE, /^Connect a Veeam/, /has been added yet/],
    ["may not", VIEW, /has been added yet/, /^Connect a Veeam/],
  ])(
    "asks for a server only when the role %s",
    async (_, permissions, shown, hidden) => {
      mountPanel([], permissions, permissions);

      expect(await screen.findByText(shown)).toBeInTheDocument();
      expect(screen.queryByText(hidden)).toBeNull();
    },
  );
});
