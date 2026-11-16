import { describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { nutServer } from "../power.fixtures";
import { NutServerTable } from "./NutServerTable";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

describe("NutServerTable", () => {
  // A success the poller stopped repeating keeps its time and no error: the
  // server's `connected`, not last_poll_at, decides Connected.
  it("says Connected only for a server whose last poll is current", () => {
    renderWithProviders(
      <NutServerTable
        servers={[nutServer(), nutServer({ id: "s2", name: "nut02", connected: false })]}
        canManage={false}
        canDelete={false}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    const row = (name: string) => screen.getByText(name).closest("tr") as HTMLElement;
    expect(within(row("nut01")).getByText("Connected")).toBeInTheDocument();
    expect(within(row("nut02")).getByText("Not polled lately")).toBeInTheDocument();
    expect(within(row("nut02")).queryByText("Connected")).not.toBeInTheDocument();
  });
});
