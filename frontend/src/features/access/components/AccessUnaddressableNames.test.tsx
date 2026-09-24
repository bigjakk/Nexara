import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { AccessGroupsSection } from "./AccessGroupsSection";
import { AccessRolesSection } from "./AccessRolesSection";
import type { AccessCapabilities } from "../api/access-queries";
import { unaddressableHint } from "@/lib/api-path";
import { expectOnScreen } from "@/test/test-utils";

/**
 * Proxmox's own group and role id formats (verify_groupname and
 * verify_rolename, pve-access-control) admit a name that is exactly "." or
 * "..", and no browser can put either in a request path. The rows stay; their
 * actions are disabled with the reason, and nothing is sent for them.
 */

const CLUSTER = "cccccccc-0000-0000-0000-000000000004";
const ACCESS = `/api/v1/clusters/${CLUSTER}/access`;

const listMock = vi.fn();
const getMock = vi.fn();

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      list: (path: string) => listMock(path) as unknown,
      get: (path: string) => getMock(path) as unknown,
      put: vi.fn(),
      post: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyACL: true,
  canModifyRealms: true,
};

function wrap(node: ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

function reasonFor(name: string): string {
  const reason = unaddressableHint(name);
  if (reason === null) throw new Error(`${name} is addressable`);
  return reason;
}

beforeEach(() => {
  vi.clearAllMocks();
  listMock.mockImplementation((path: string) => {
    if (path === `${ACCESS}/groups`) {
      return Promise.resolve([
        { groupid: "operators", comment: "day shift" },
        { groupid: ".", comment: "made outside Nexara" },
      ]);
    }
    if (path === `${ACCESS}/roles`) {
      return Promise.resolve([
        { roleid: "Operator", privs: "VM.Audit", special: false },
        { roleid: "..", privs: "VM.Audit", special: false },
      ]);
    }
    return Promise.resolve([]);
  });
  getMock.mockResolvedValue({ groupid: "operators", members: [] });
});

/** The table row a piece of visible text sits in. */
function rowOf(text: string): HTMLElement {
  const row = screen.getByText(text).closest("tr");
  if (!(row instanceof HTMLElement)) throw new Error(`${text} is not in a row`);
  return row;
}

describe("an access group Nexara cannot address", () => {
  it("keeps its row, and shows why as text in place of its actions", async () => {
    wrap(
      <AccessGroupsSection clusterId={CLUSTER} capabilities={capabilities} />,
    );

    await screen.findByText("made outside Nexara");
    const row = rowOf("made outside Nexara");
    expectOnScreen(within(row).getByText(reasonFor(".")));
    expect(within(row).queryAllByRole("button")).toEqual([]);
    const ordinary = rowOf("day shift");
    expect(
      within(ordinary).getByRole("button", { name: "Delete operators" }),
    ).toBeEnabled();
    expect(
      within(ordinary).getByRole("button", { name: "Edit operators" }),
    ).toBeEnabled();
  });

  it("explains itself when expanded instead of reading its members", async () => {
    const user = userEvent.setup();
    wrap(
      <AccessGroupsSection clusterId={CLUSTER} capabilities={capabilities} />,
    );

    await user.click(await screen.findByText("made outside Nexara"));
    // Once in its row, once more where its members would be.
    await waitFor(() => {
      expect(screen.getAllByText(reasonFor("."))).toHaveLength(2);
    });

    // Positive control: the ordinary group's members ARE read.
    await user.click(screen.getByText("day shift"));
    await waitFor(() => {
      expect(getMock).toHaveBeenCalled();
    });
    expect(getMock.mock.calls).toEqual([[`${ACCESS}/groups/operators`]]);
  });
});

describe("an access role Nexara cannot address", () => {
  it("keeps its row, and shows why as text in place of its actions", async () => {
    wrap(
      <AccessRolesSection clusterId={CLUSTER} capabilities={capabilities} />,
    );

    await screen.findByText("..");
    const row = rowOf("..");
    expectOnScreen(within(row).getByText(reasonFor("..")));
    expect(within(row).queryAllByRole("button")).toEqual([]);
    const ordinary = rowOf("Operator");
    expect(
      within(ordinary).getByRole("button", { name: "Edit" }),
    ).toBeEnabled();
    expect(
      within(ordinary).getByRole("button", { name: "Delete Operator" }),
    ).toBeEnabled();
  });
});
