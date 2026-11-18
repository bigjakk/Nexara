import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import type { ApplianceTemplate } from "../types/storage";
import { ApplianceBrowserDialog } from "./ApplianceBrowserDialog";
import { OCIPullDialog } from "./OCIPullDialog";
import { UploadDialog } from "./UploadDialog";
import { URLDownloadDialog } from "./URLDownloadDialog";

const UPID = vi.hoisted(
  () => "UPID:pve-01:00000001:00000002:00000003:download:x:x:",
);

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);
// The upload goes over an XMLHttpRequest, which is not what this file tests.
vi.mock("../api/storage-queries", async (importOriginal) => {
  const { useMutation } = await import("@tanstack/react-query");
  return {
    ...(await importOriginal<typeof import("../api/storage-queries")>()),
    useUploadFile: () =>
      useMutation({
        mutationFn: () => Promise.resolve({ upid: UPID, status: "OK" }),
      }),
  };
});

const store = { clusterId: "c1", storageId: "store01" };
const REASON = "download failed: 404 Not Found";

function taskEnds(exitStatus: string) {
  vi.mocked(apiClient.get).mockResolvedValue({
    upid: UPID,
    status: "stopped",
    exit_status: exitStatus,
  });
  vi.mocked(apiClient.post).mockResolvedValue({ upid: UPID, status: "OK" });
}

afterEach(() => {
  vi.clearAllMocks();
});

// Each closed when its task ended, failed or not, taking Proxmox's reason with
// it; a dialog that kept the failed task as its running one could not close.
describe.each([
  ["a failed task", REASON],
  ["a task that succeeds", "OK"],
])("the storage dialogs, on %s", (_outcome, exitStatus) => {
  it.each<
    [string, React.ReactElement, (user: UserEvent) => Promise<void>, string]
  >([
    [
      "Download from URL",
      <URLDownloadDialog key="url" {...store} supportedContent="iso" />,
      async (user) => {
        await user.type(screen.getByLabelText("Filename"), "installer.iso");
        await user.type(
          screen.getByLabelText("URL"),
          "https://example.com/installer.iso",
        );
      },
      "Download",
    ],
    [
      "Pull OCI image",
      <OCIPullDialog key="oci" {...store} />,
      async (user) => {
        await user.type(
          screen.getByLabelText("Image reference"),
          "docker.io/library/nginx:latest",
        );
      },
      "Pull image",
    ],
    [
      "Upload",
      <UploadDialog key="upload" {...store} supportedContent="iso" />,
      async (user) => {
        await user.upload(
          screen.getByLabelText("File"),
          new File(["x"], "installer.iso"),
        );
      },
      "Upload",
    ],
  ])(
    "%s stays open only on a failure",
    async (trigger, dialog, fill, submit) => {
      taskEnds(exitStatus);
      const user = userEvent.setup();
      renderWithProviders(dialog);
      await user.click(screen.getByRole("button", { name: trigger }));
      await fill(user);
      const form = within(screen.getByRole("dialog"));
      await user.click(form.getByRole("button", { name: submit }));

      if (exitStatus === "OK") {
        await waitFor(() => {
          expect(screen.queryByRole("dialog")).toBeNull();
        });
        return;
      }
      // The reason stays, over a form that can be sent again.
      expect(await form.findByText(`Task failed: ${REASON}`)).toBeVisible();
      expect(await form.findByRole("button", { name: submit })).toBeEnabled();
      expect(form.getByText(`Task failed: ${REASON}`)).toBeVisible();
    },
  );
});

// The appliance browser stays open after a download; a failed one's banner
// went with its task.
it("the appliance browser keeps a failed download on its banner", async () => {
  taskEnds(REASON);
  const appliance: ApplianceTemplate = {
    template: "debian-12-standard_12.7-1_amd64.tar.zst",
    os: "debian-12",
    type: "lxc",
    version: "12.7-1",
    section: "system",
    package: "debian-12-standard",
    description: "",
    headline: "Debian 12 (standard)",
  };
  vi.mocked(apiClient.list).mockResolvedValue([appliance]);
  const user = userEvent.setup();
  renderWithProviders(
    <ApplianceBrowserDialog
      {...store}
      storageName="store01"
      open
      onOpenChange={vi.fn()}
    />,
  );
  await user.click(await screen.findByRole("button", { name: "Download" }));

  // The row is free for the next download once the task has ended.
  await waitFor(() => {
    expect(screen.getByRole("button", { name: "Download" })).toBeEnabled();
  });
  expect(screen.getByText(`Task failed: ${REASON}`)).toBeInTheDocument();
});
