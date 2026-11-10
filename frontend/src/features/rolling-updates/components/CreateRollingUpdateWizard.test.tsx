import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { CreateRollingUpdateWizard } from "./CreateRollingUpdateWizard";
import { renderWithProviders } from "@/test/test-utils";

const CLUSTER = "cccccccc-0000-0000-0000-000000000001";
const PREFLIGHT_PATH = `/api/v1/clusters/${CLUSTER}/rolling-updates/preflight-ha`;

const postMock = vi.fn();

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock({
    list: (path: string) =>
      Promise.resolve(
        path === `/api/v1/clusters/${CLUSTER}/nodes`
          ? [
              { id: "n1", name: "pve-01", status: "online" },
              { id: "n2", name: "pve-02", status: "online" },
            ]
          : [],
      ),
    get: () => Promise.resolve(null),
    post: (path: string, body: unknown) => postMock(path, body) as unknown,
  }),
);

beforeEach(() => {
  postMock.mockReset();
  postMock.mockResolvedValue({ conflicts: [], has_errors: false });
});

async function openWithBothNodes(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "New Rolling Update" }));
  await user.click(await screen.findByRole("button", { name: "Select all" }));
}

describe("CreateRollingUpdateWizard", () => {
  // The pre-flight's capacity check depends on how many nodes are drained at
  // once, so Parallelism is set before it runs, not on the step after it.
  it("runs the pre-flight with the parallelism the job will use", async () => {
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<CreateRollingUpdateWizard clusterId={CLUSTER} />);
    await openWithBothNodes(user);

    fireEvent.change(screen.getByLabelText(/Parallelism/), {
      target: { value: "2" },
    });
    await user.click(screen.getByRole("button", { name: "Next" }));

    await waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(PREFLIGHT_PATH, {
        nodes: ["pve-01", "pve-02"],
        parallelism: 2,
      });
    });
  });

  it("still has Reboot after update off when reopened after a close", async () => {
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<CreateRollingUpdateWizard clusterId={CLUSTER} />);
    await openWithBothNodes(user);
    await user.keyboard("{Escape}");

    await openWithBothNodes(user);
    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(await screen.findByRole("button", { name: "Next" }));

    expect(
      await screen.findByRole("switch", { name: "Reboot after update" }),
    ).not.toBeChecked();
  });
});
