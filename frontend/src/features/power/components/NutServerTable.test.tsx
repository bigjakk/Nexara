import { describe, expect, it, vi } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { deferred } from "@/test/fake-server";
import { renderWithProviders } from "@/test/test-utils";
import { nutServer } from "../power.fixtures";
import { NutServerTable } from "./NutServerTable";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

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

describe("NutServerTable Test connection", () => {
  // mutate()'s own onError is not run once the row has gone: a failure that
  // lands after the page was left was reported nowhere.
  it("toasts a failure that lands after the table has gone, once", async () => {
    const answer = deferred<never>();
    vi.mocked(apiClient.post).mockReturnValueOnce(answer.promise);
    const { unmount } = renderWithProviders(
      <NutServerTable servers={[nutServer()]} canManage canDelete={false} onEdit={vi.fn()} onDelete={vi.fn()} />,
    );
    await userEvent.click(screen.getByTitle("Test connection"));
    unmount();

    await act(async () => {
      answer.reject(new ApiClientError(502, { error: "bad_gateway", message: "connection refused" }));
      await Promise.resolve();
    });

    expect(vi.mocked(toast.error).mock.calls).toEqual([["Testing NUT server nut01 failed: connection refused"]]);
  });
});
