import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { createTestQueryClient, createWrapper } from "@/test/test-utils";

import { useDeleteDRSRule } from "./drs-queries";

const CLUSTER = "aaaaaaaa-0000-0000-0000-000000000001";
const RULE = "bbbbbbbb-0000-0000-0000-000000000002";

const deleteMock = vi.fn();

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    delete: (path: string) => deleteMock(path) as unknown,
  },
}));

// The server answers 404 when the rule is already gone — deleted from another
// tab, or by another operator — or belongs to another cluster. The list that
// offered it is what is stale, so the hook refetches it whichever way the
// delete ends; refetching only on success left the dead row on screen for up
// to the five-minute stale time.
describe("useDeleteDRSRule", () => {
  beforeEach(() => {
    deleteMock.mockReset();
  });

  for (const outcome of ["succeeds", "fails with a 404"] as const) {
    it(`refetches the cluster's rule list when the delete ${outcome}`, async () => {
      if (outcome === "succeeds") {
        deleteMock.mockResolvedValue({ status: "ok" });
      } else {
        deleteMock.mockRejectedValue(new Error("DRS rule not found"));
      }
      const client = createTestQueryClient({ keepCache: true });
      const invalidate = vi.spyOn(client, "invalidateQueries");
      const { result } = renderHook(() => useDeleteDRSRule(CLUSTER), {
        wrapper: createWrapper({ client, router: false }),
      });

      act(() => {
        result.current.mutate(RULE);
      });
      await waitFor(() => {
        expect(result.current.isIdle).toBe(false);
        expect(result.current.isPending).toBe(false);
      });

      expect(deleteMock).toHaveBeenCalledWith(
        `/api/v1/clusters/${CLUSTER}/drs/rules/${RULE}`,
      );
      expect(invalidate).toHaveBeenCalledWith({
        queryKey: ["drs", "rules", CLUSTER],
      });
    });
  }
});
