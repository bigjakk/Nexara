import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { CreateCTDialog } from "./CreateCTDialog";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const NODE = {
  id: "node-01",
  cluster_id: "cluster01",
  name: "pve-01",
  status: "online",
  cpu_count: 8,
  mem_total: 8589934592,
};

describe("CreateCTDialog", () => {
  // The step list is a second way forward. It must not get past a step that
  // Next would hold: a container cannot be reviewed, or created, with no template.
  it("lets the step list go no further than Next would", async () => {
    const answer = (path: unknown) =>
      Promise.resolve(String(path).endsWith("/nodes") ? [NODE] : []);
    vi.mocked(apiClient.list).mockImplementation(answer);
    vi.mocked(apiClient.get).mockImplementation(answer);
    const user = userEvent.setup();
    renderWithProviders(
      <CreateCTDialog
        open
        onOpenChange={() => undefined}
        clusterId="cluster01"
      />,
    );

    // General fills itself in: the next free VMID and the only node.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
    });
    expect(screen.getByRole("button", { name: "Root disk" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Review" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Template" }));
    expect(screen.getByRole("button", { name: "Root disk" })).toBeDisabled();
    await user.type(
      screen.getByLabelText("Template path"),
      "local:vztmpl/debian-12.tar.zst",
    );

    // Every later step is valid as it opens, so the list now reaches the end.
    await user.click(screen.getByRole("button", { name: "Review" }));
    expect(
      screen.getByRole("button", { name: "Create container" }),
    ).toBeEnabled();
  });
});
