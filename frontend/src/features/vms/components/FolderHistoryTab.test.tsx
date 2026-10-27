import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { FolderHistoryTab } from "./FolderHistoryTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

describe("FolderHistoryTab", () => {
  // The folder's route from the kind's default window, with a Guest column
  // linking each guest the page knows — one that moved away included.
  it("reads the folder's History and links each guest", async () => {
    mockedPage.mockResolvedValue({
      items: [
        makeHistoryEntry({
          id: "audit:1",
          resource_type: "vm_folder_membership",
          action: "assign",
          vmid: 101,
          subject_vmid: 0,
          details: JSON.stringify({
            vmid: 101,
            folder_id: "f2",
            from_folder_id: "f1",
          }),
        }),
        makeHistoryEntry({
          id: "audit:2",
          vmid: 102,
          subject_vmid: 102,
          action: "config_update",
        }),
      ],
      total: 2,
    });
    renderWithProviders(
      <FolderHistoryTab
        clusterId="c1"
        folderId="f1"
        folderNames={
          new Map([
            ["f1", "folder01"],
            ["f2", "folder02"],
          ])
        }
        guestLinks={
          new Map([[101, { name: "linux01", path: "/inventory/qemu/c1/v1" }]])
        }
      />,
    );

    expect(await screen.findByText("folder01 → folder02")).toBeInTheDocument();
    const url = new URL(String(mockedPage.mock.calls[0]?.[0]), "http://x");
    expect(url.pathname).toBe("/api/v1/clusters/c1/vm-folders/f1/history");
    expect(url.searchParams.get("start_time")).not.toBeNull();
    expect(screen.getByRole("link", { name: "linux01" })).toHaveAttribute(
      "href",
      "/inventory/qemu/c1/v1",
    );
    expect(screen.getByText("#102")).toBeInTheDocument();
  });
});
