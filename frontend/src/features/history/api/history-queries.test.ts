import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { createWrapper } from "@/test/test-utils";
import type { HistoryTarget } from "../types/history";
import { useHistory } from "./history-queries";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

function probe(id: string, clusterId: string | null): HistoryTarget {
  return {
    clusterId,
    path: (query) => apiPath`/api/v1/probes/${id}/history?${query}`,
  };
}

describe("useHistory", () => {
  // Keyed on the route: two objects of one cluster never share a page, and a
  // global kind's History is read with no cluster at all.
  it("keeps each target's own page", async () => {
    mockedPage.mockReset();
    mockedPage.mockImplementation((path) =>
      Promise.resolve({
        items: [makeHistoryEntry({ id: String(path) })],
        total: 1,
      }),
    );
    const wrapper = createWrapper();
    const params = {
      page: 0,
      category: "",
      source: "" as const,
      timeWindow: "" as const,
      enabled: true,
    };
    const hooks = ["a", "b", "g"].map((id) =>
      renderHook(
        () =>
          useHistory({
            ...params,
            target: probe(id, id === "g" ? null : "c1"),
          }),
        { wrapper },
      ),
    );
    for (const [i, id] of ["a", "b", "g"].entries()) {
      await waitFor(() => {
        expect(hooks[i]?.result.current.data?.items[0]?.id).toBe(
          `/api/v1/probes/${id}/history?limit=50&offset=0`,
        );
      });
    }
  });
});
