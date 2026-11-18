import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { CloudInitPanel } from "./CloudInitPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// A Viewer got the fields and Save, which the config route refuses.
it.each([
  [["manage:vm"], false],
  [["view:vm"], true],
])(
  "with %j shows the cloud-init settings read-only: %s",
  async (permissions, readOnly) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.get).mockResolvedValue({
      ide2: "store01:vm-101-cloudinit,media=cdrom",
      ciuser: "ubuntu",
    });
    renderWithProviders(<CloudInitPanel clusterId="c1" vmId="vm-1" />);

    expect(await screen.findByDisplayValue("ubuntu")).toHaveProperty(
      "disabled",
      readOnly,
    );
    expect(screen.queryByRole("button", { name: "Save" }) === null).toBe(
      readOnly,
    );
  },
);
