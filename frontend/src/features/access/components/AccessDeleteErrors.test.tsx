import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { renderOnAppClient } from "@/test/save-outcome-kit";
import type { AccessCapabilities } from "../api/access-queries";
import { AccessGroupsSection } from "./AccessGroupsSection";
import { AccessRolesSection } from "./AccessRolesSection";

/**
 * A failed group or role delete reaches the operator through the app's global
 * error toast (lib/query-client.ts) and through nothing else: the confirmation
 * closes whatever the outcome, and neither section has anywhere to show a
 * delete's error. useDeleteAccessGroup and useDeleteAccessRole therefore keep
 * that toast, where the other access hooks opt out of it (errorsHandledLocally)
 * because their component shows the failure itself. Opting one of these out
 * too would make its failure silent, which is worse than reporting it twice.
 *
 * These run on the app's own kind of client (test/app-query-client.ts), whose
 * mutation cache is the one that raises the toast. renderWithProviders' client
 * has none: nothing toasts on it, whatever a hook does.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

// The app's mutation-error net toasts through sonner, so this one mock sees
// every toast a run can raise.
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedList = vi.mocked(apiClient.list);
const mockedDelete = vi.mocked(apiClient.delete);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000006";
const ACCESS = `/api/v1/clusters/${CLUSTER}/access`;
const GROUP = "operators";
const ROLE = "Operator";
const DENIED = "Proxmox API permission denied";

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

function denied(): ApiClientError {
  return new ApiClientError(403, { error: "forbidden", message: DENIED });
}

beforeEach(() => {
  vi.resetAllMocks();
  mockedList.mockImplementation((path: string) => {
    if (path === `${ACCESS}/groups`) {
      return Promise.resolve([{ groupid: GROUP, comment: "day shift" }]);
    }
    if (path === `${ACCESS}/roles`) {
      return Promise.resolve([
        { roleid: ROLE, privs: "VM.Audit", special: false },
      ]);
    }
    return Promise.resolve([]);
  });
  mockedDelete.mockResolvedValue(undefined);
});

describe("a failed group delete", () => {
  it("is reported by the global toast, and by nothing else", async () => {
    const user = userEvent.setup();
    mockedDelete.mockRejectedValueOnce(denied());
    renderOnAppClient(
      <AccessGroupsSection clusterId={CLUSTER} capabilities={capabilities} />,
    );

    await user.click(
      await screen.findByRole("button", { name: `Delete ${GROUP}` }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: `Delete group ${GROUP}?`,
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Group" }));

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(DENIED);
    });
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    // The confirmation closes on any outcome, so the toast is all the operator
    // is left with. A section that shows the failure itself should have its
    // hook opt out of the toast, and this test say so. The text is matched
    // loosely and any alert counts, so an error worded in the section's own way
    // does not slip past.
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).toBeNull();
    });
    expect(
      screen.queryByText(/permission denied/i),
      "the section shows the failure itself: give useDeleteAccessGroup errorsHandledLocally",
    ).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(mockedDelete.mock.calls).toEqual([[`${ACCESS}/groups/${GROUP}`]]);
  });
});

describe("a failed role delete", () => {
  it("is reported by the global toast, and by nothing else", async () => {
    const user = userEvent.setup();
    mockedDelete.mockRejectedValueOnce(denied());
    renderOnAppClient(
      <AccessRolesSection clusterId={CLUSTER} capabilities={capabilities} />,
    );

    await user.click(
      await screen.findByRole("button", { name: `Delete ${ROLE}` }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: `Delete role ${ROLE}?`,
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Role" }));

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(DENIED);
    });
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).toBeNull();
    });
    expect(
      screen.queryByText(/permission denied/i),
      "the section shows the failure itself: give useDeleteAccessRole errorsHandledLocally",
    ).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(mockedDelete.mock.calls).toEqual([[`${ACCESS}/roles/${ROLE}`]]);
  });
});
