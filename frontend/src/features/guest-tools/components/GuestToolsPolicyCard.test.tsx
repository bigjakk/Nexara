import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { apiClient } from "@/lib/api-client";
import type { GuestToolsConfig } from "../types/guest-tools";
import { GuestToolsPolicyCard } from "./GuestToolsPolicyCard";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const noStorage: GuestToolsConfig = {
  cluster_id: "c1",
  mode: "report",
  target_version: "",
  snapshot_before: false,
  max_concurrent: 5,
  effective_version: "",
  iso_storage: "",
};

describe("GuestToolsPolicyCard", () => {
  // The ISO's storage is set on the virtio-win card, which takes
  // manage:storage; a role without it is not told to set one.
  it.each([
    { perms: ["view:guest_tools"], can: false },
    { perms: ["view:guest_tools", "manage:storage"], can: true },
  ])(
    "asks for a target storage only of a role that may set one: $perms",
    async ({ perms, can }) => {
      grantPermissions(...perms);
      vi.mocked(apiClient.get).mockResolvedValue(noStorage);
      vi.mocked(apiClient.list).mockResolvedValue([]);
      renderWithProviders(<GuestToolsPolicyCard clusterId="c1" />);

      expect(
        await screen.findByText(/Detection still works/),
      ).toBeInTheDocument();
      expect(screen.queryByText(/Set a target storage/) !== null).toBe(can);
    },
  );
});
