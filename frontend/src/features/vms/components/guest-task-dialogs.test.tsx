import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { CloneDialog } from "./CloneDialog";
import { CloneToTemplateDialog } from "./CloneToTemplateDialog";
import { ConvertToTemplateDialog } from "./ConvertToTemplateDialog";
import { DeployTemplateDialog } from "./DeployTemplateDialog";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const UPID = "UPID:pve-01:00000001:00000002:00000003:qmclone:101:x:";
const common = { clusterId: "c1", kind: "vm" as const };
type Open = (onOpenChange: (open: boolean) => void) => ReactElement;

// Each closed when its task ended, failed or not, taking Proxmox's reason with
// it; a failed clone or convert read as done. They share the banner's rule.
describe.each([
  ["a failed task", "clone failed: locked"],
  ["a task that succeeds", "OK"],
])("the clone and template dialogs, on %s", (_outcome, exitStatus) => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it.each<[string, Open, (user: UserEvent) => Promise<void>]>([
    [
      "Clone",
      (o) => (
        <CloneDialog
          open
          onOpenChange={o}
          {...common}
          resourceId="vm-1"
          sourceName="linux01"
        />
      ),
      async (user) => {
        await user.click(await screen.findByRole("button", { name: "Clone" }));
      },
    ],
    [
      "Clone to template",
      (o) => (
        <CloneToTemplateDialog
          open
          onOpenChange={o}
          {...common}
          resourceId="vm-1"
          sourceName="linux01"
        />
      ),
      async (user) => {
        await user.click(
          await screen.findByRole("button", { name: "Clone to template" }),
        );
      },
    ],
    [
      "Deploy",
      (o) => (
        <DeployTemplateDialog
          open
          onOpenChange={o}
          {...common}
          vmId="vm-1"
          templateName="linux01"
        />
      ),
      async (user) => {
        await user.click(await screen.findByRole("button", { name: "Deploy" }));
      },
    ],
    [
      "Convert to template",
      (o) => (
        <ConvertToTemplateDialog
          open
          onOpenChange={o}
          {...common}
          resourceId="vm-1"
          resourceName="linux01"
        />
      ),
      async (user) => {
        await user.type(screen.getByPlaceholderText("linux01"), "linux01");
        await user.click(
          screen.getByRole("button", { name: "Convert to template" }),
        );
      },
    ],
  ])("%s stays open only on a failure", async (_name, open, submit) => {
    vi.mocked(apiClient.list).mockResolvedValue([]);
    vi.mocked(apiClient.get).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path).includes("/tasks/")
          ? {
              upid: UPID,
              status: "stopped",
              exit_status: exitStatus,
            }
          : [],
      ),
    );
    vi.mocked(apiClient.post).mockResolvedValue({ upid: UPID, status: "OK" });
    const onOpenChange = vi.fn<(open: boolean) => void>();
    renderWithProviders(open(onOpenChange));
    await submit(userEvent.setup());

    if (exitStatus === "OK") {
      await waitFor(() => {
        expect(onOpenChange).toHaveBeenCalledWith(false);
      });
      return;
    }
    expect(
      await screen.findByText(`Task failed: ${exitStatus}`),
    ).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });
});
