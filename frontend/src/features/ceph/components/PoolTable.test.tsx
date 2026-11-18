import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { PoolTable } from "./PoolTable";
import type { CephPool } from "../types/ceph";
import { unaddressableHint } from "@/lib/api-path";
import { expectOnScreen, renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

function pool(name: string, id: number): CephPool {
  return {
    pool_name: name,
    pool: id,
    size: 3,
    min_size: 2,
    pg_num: 32,
    pg_autoscale_mode: "on",
    crush_rule: 0,
    bytes_used: 0,
    percent_used: 0,
    read_bytes_sec: 0,
    write_bytes_sec: 0,
    read_op_per_sec: 0,
    write_op_per_sec: 0,
  };
}

describe("a Ceph pool Nexara cannot address", () => {
  // Ceph, and pveceph's own pool-name rule, admit "." and "..".
  it("keeps its row, and shows why as text in place of the delete", () => {
    grantPermissions("manage:ceph");
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <PoolTable
          clusterId="cccccccc-0000-0000-0000-000000000005"
          pools={[pool("rbd", 1), pool("..", 2)]}
        />
      </QueryClientProvider>,
    );

    const row = screen.getByText("..").closest("tr");
    if (!(row instanceof HTMLElement)) throw new Error(".. is not in a row");
    // The reason as text in the row, and no delete to try — a disabled
    // button's title never shows. On screen as far as jsdom can tell: see
    // expectOnScreen.
    expectOnScreen(within(row).getByText(reasonFor("..")));
    expect(within(row).queryAllByRole("button")).toEqual([]);
    expect(
      screen.getByRole("button", { name: "Delete pool rbd" }),
    ).toBeEnabled();
  });
});

describe("PoolTable — delete", () => {
  // Deleting a pool takes manage:ceph on the cluster; view:ceph shows the table.
  it.each([
    { perms: ["view:ceph"], can: false },
    { perms: ["view:ceph", "manage:ceph"], can: true },
  ])(
    "offers Delete only to a role that may delete: $perms",
    ({ perms, can }) => {
      grantPermissions(...perms);
      renderWithProviders(
        <PoolTable clusterId="c1" pools={[pool("rbd", 1)]} />,
      );
      expect(screen.getByText("rbd")).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Delete pool rbd" }) !== null,
      ).toBe(can);
    },
  );
});

function reasonFor(name: string): string {
  const reason = unaddressableHint(name);
  if (reason === null) throw new Error(`${name} is addressable`);
  return reason;
}
