import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { PoolTable } from "./PoolTable";
import type { CephPool } from "../types/ceph";
import { unaddressableHint } from "@/lib/api-path";
import { expectOnScreen } from "@/test/test-utils";

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      list: vi.fn(),
      get: vi.fn(),
      put: vi.fn(),
      post: vi.fn(),
      delete: vi.fn(),
    },
  };
});

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

function reasonFor(name: string): string {
  const reason = unaddressableHint(name);
  if (reason === null) throw new Error(`${name} is addressable`);
  return reason;
}
