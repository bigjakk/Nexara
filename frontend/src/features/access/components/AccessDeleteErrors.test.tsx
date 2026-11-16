import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { DENIED, renderOnAppClient } from "@/test/save-outcome-kit";
import type { AccessCapabilities } from "../api/access-queries";
import { AccessGroupsSection } from "./AccessGroupsSection";
import { AccessRolesSection } from "./AccessRolesSection";

/**
 * A failed group or role delete reaches the operator through one toast and
 * nothing else: the confirmation closes whatever the outcome, and neither
 * section has anywhere to show a delete's error. Their hooks opt out of the
 * global toast (a 409 opens the self-credential override instead), so the
 * section raises it; opting out without that would make the failure silent.
 *
 * These run on the app's own kind of client (test/app-query-client.ts), whose
 * mutation cache is the one that would raise a second, global toast.
 * renderWithProviders' client has none.
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
    const { qc } = renderOnAppClient(
      <AccessGroupsSection clusterId={CLUSTER} capabilities={capabilities} />,
    );
    // The cluster page's read: the toast names the cluster (there can be two).
    act(() => {
      qc.setQueryData(["clusters", CLUSTER], { name: "cluster01" });
    });

    await user.click(
      await screen.findByRole("button", { name: `Delete ${GROUP}` }),
    );
    const ask = await screen.findByRole("alertdialog", {
      name: `Delete group ${GROUP}?`,
    });
    await user.click(within(ask).getByRole("button", { name: "Delete Group" }));

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Deleting group ${GROUP} in cluster01 failed: ${DENIED}`,
      );
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
      "the section shows the failure inline as well as in the toast",
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
      expect(mockedToastError).toHaveBeenCalledWith(
        `Deleting role ${ROLE} failed: ${DENIED}`,
      );
    });
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).toBeNull();
    });
    expect(
      screen.queryByText(/permission denied/i),
      "the section shows the failure inline as well as in the toast",
    ).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(mockedDelete.mock.calls).toEqual([[`${ACCESS}/roles/${ROLE}`]]);
  });
});
