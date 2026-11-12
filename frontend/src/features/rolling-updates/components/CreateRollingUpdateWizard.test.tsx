import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { CreateRollingUpdateWizard } from "./CreateRollingUpdateWizard";
import { ApiClientError } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";

const CLUSTER = "cccccccc-0000-0000-0000-000000000001";
const PREFLIGHT_PATH = `/api/v1/clusters/${CLUSTER}/rolling-updates/preflight-ha`;
const CREATE_PATH = `/api/v1/clusters/${CLUSTER}/rolling-updates`;
const SSH_PATH = `/api/v1/clusters/${CLUSTER}/ssh-credentials`;

const getMock = vi.fn();
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
    get: (path: string) => getMock(path) as unknown,
    post: (path: string, body: unknown) => postMock(path, body) as unknown,
  }),
);

beforeEach(() => {
  getMock.mockReset();
  getMock.mockResolvedValue(null);
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

  // The automated upgrade is on by default, but the create is refused for a
  // cluster with no SSH credential. One the caller may not read (an Operator)
  // is unknown rather than absent, so it stays on and the create decides.
  it.each([
    {
      ssh: "configured",
      read: () =>
        Promise.resolve({ cluster_id: CLUSTER, username: "root", port: 22 }),
      automated: true,
    },
    {
      ssh: "not configured",
      read: () => Promise.resolve(null),
      automated: false,
    },
    {
      ssh: "unreadable",
      read: () =>
        Promise.reject(
          new ApiClientError(403, { error: "forbidden", message: "" }),
        ),
      automated: true,
    },
  ])(
    "creates the job with auto_upgrade $automated when SSH is $ssh",
    async ({ read, automated }) => {
      getMock.mockImplementation((path: string) =>
        path === SSH_PATH ? read() : Promise.resolve(null),
      );
      const user = userEvent.setup({ delay: null });
      renderWithProviders(<CreateRollingUpdateWizard clusterId={CLUSTER} />);
      await openWithBothNodes(user);
      await user.click(screen.getByRole("button", { name: "Next" }));
      await user.click(await screen.findByRole("button", { name: "Next" }));

      const toggle = await screen.findByRole("switch", {
        name: "Automated upgrade (SSH)",
      });
      await waitFor(() => {
        expect(toggle).toHaveAttribute("aria-checked", String(automated));
      });
      // Usable exactly when it defaults on: a refused Operator must turn it off.
      expect(toggle).toHaveProperty("disabled", !automated);
      await user.click(
        screen.getByRole("switch", { name: "Start immediately" }),
      );
      await user.click(screen.getByRole("button", { name: "Create Job" }));

      await waitFor(() => {
        expect(postMock).toHaveBeenCalledWith(
          CREATE_PATH,
          expect.objectContaining({ auto_upgrade: automated }),
        );
      });
    },
  );
});
