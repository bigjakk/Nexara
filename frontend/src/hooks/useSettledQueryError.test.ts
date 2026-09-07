import { describe, it, expect } from "vitest";
import { renderHook } from "@testing-library/react";
import {
  useSettledQueryError,
  type QueryStateLike,
} from "./useSettledQueryError";
import { ApiClientError } from "@/lib/api-client";

function state(over: Partial<QueryStateLike> = {}): QueryStateLike {
  return {
    isLoading: false,
    isError: false,
    isSuccess: true,
    isPaused: false,
    error: null,
    errorUpdatedAt: 0,
    fetchStatus: "idle",
    refetch: () => undefined,
    ...over,
  };
}

const denied = new ApiClientError(403, { error: "forbidden", message: "no" });

/** A query that has failed and holds no data to fall back on. */
function failed(errorUpdatedAt: number, error: Error = denied) {
  return state({ isSuccess: false, isError: true, error, errorUpdatedAt });
}

/** The refetch that follows: TanStack nulls `error`, keeps `errorUpdatedAt`. */
function refetchingAfter(errorUpdatedAt: number) {
  return state({
    isSuccess: false,
    isLoading: true,
    errorUpdatedAt,
    fetchStatus: "fetching",
  });
}

describe("useSettledQueryError", () => {
  it("remembers the failure across the refetch that clears it", () => {
    const { result, rerender } = renderHook(
      ({ q }: { q: QueryStateLike }) => useSettledQueryError(q),
      { initialProps: { q: failed(1000) } },
    );
    expect(result.current).toBe(denied);

    rerender({ q: refetchingAfter(1000) });
    expect(result.current).toBe(denied);
  });

  it("forgets the failure once the query succeeds, and keeps forgetting it", () => {
    const { result, rerender } = renderHook(
      ({ q }: { q: QueryStateLike }) => useSettledQueryError(q),
      { initialProps: { q: failed(1000) } },
    );
    // successState() clears `error` but leaves errorUpdatedAt at the old
    // failure's timestamp, so the ref has to be cleared here rather than left
    // for the timestamp check to catch. Without the third step this passes
    // even if the isSuccess branch is deleted as redundant, and a stale error
    // resurfaces on the first refetch after a success.
    rerender({ q: state({ errorUpdatedAt: 1000 }) });
    expect(result.current).toBeNull();

    rerender({ q: refetchingAfter(1000) });
    expect(result.current).toBeNull();
  });

  it("replaces a remembered failure with a newer one", () => {
    const later = new ApiClientError(502, {
      error: "bad_gateway",
      message: "cluster unreachable",
    });
    const { result, rerender } = renderHook(
      ({ q }: { q: QueryStateLike }) => useSettledQueryError(q),
      { initialProps: { q: failed(1000) } },
    );
    rerender({ q: refetchingAfter(1000) });
    rerender({ q: failed(2000, later) });
    rerender({ q: refetchingAfter(2000) });
    expect(result.current).toBe(later);
  });

  it("drops a failure that belongs to a different read", () => {
    // The component was handed a new node without remounting; the incoming
    // query has never failed, so its errorUpdatedAt is 0.
    const { result, rerender } = renderHook(
      ({ q }: { q: QueryStateLike }) => useSettledQueryError(q),
      { initialProps: { q: failed(1000) } },
    );
    rerender({ q: refetchingAfter(0) });
    expect(result.current).toBeNull();
  });

  it("confines a remembered failure to the identity it was seen under", () => {
    // Same timestamp, different object: without the identity key this would
    // report pve-02 as failing on pve-01's error.
    const { result, rerender } = renderHook(
      ({ q, id }: { q: QueryStateLike; id: string }) =>
        useSettledQueryError(q, id),
      { initialProps: { q: failed(1000), id: "pve-01" } },
    );
    expect(result.current).toBe(denied);

    rerender({ q: refetchingAfter(1000), id: "pve-02" });
    expect(result.current).toBeNull();
  });

  it("survives a query result that omits errorUpdatedAt entirely", () => {
    // Hand-built fakes in tests do this, and `undefined === undefined` must
    // not match a ref holding null.
    const bare = { ...state({ isSuccess: false, isLoading: true }) };
    delete (bare as Partial<QueryStateLike>).errorUpdatedAt;
    const { result } = renderHook(() => useSettledQueryError(bare));
    expect(result.current).toBeNull();
  });
});
