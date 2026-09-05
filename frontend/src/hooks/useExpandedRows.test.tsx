import { describe, it, expect } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useExpandedRows } from "./useExpandedRows";

describe("useExpandedRows", () => {
  it("opens and closes a row, and holds several open at once", () => {
    const { result } = renderHook(() => useExpandedRows("srv-1"));
    expect([...result.current.expanded]).toEqual([]);

    act(() => {
      result.current.toggle("a");
    });
    act(() => {
      result.current.toggle("b");
    });
    expect([...result.current.expanded].sort()).toEqual(["a", "b"]);

    act(() => {
      result.current.toggle("a");
    });
    expect([...result.current.expanded]).toEqual(["b"]);
  });

  // Used to show a connection-test result in a row the operator did not open.
  // Unlike toggle, calling it on an already-open row must leave it open.
  it("expands a row whether or not it is already open", () => {
    const { result } = renderHook(() => useExpandedRows("srv-1"));
    act(() => {
      result.current.expand("a");
    });
    act(() => {
      result.current.expand("a");
    });
    expect([...result.current.expanded]).toEqual(["a"]);
  });

  // The reason the hook takes a scope at all. Row ids belong to one Veeam
  // server; carrying the set across a switch reopens rows the operator closed
  // on the way out, and the set only ever grows.
  it("drops the set when the scope changes", () => {
    const { result, rerender } = renderHook(
      ({ scope }: { scope: string }) => useExpandedRows(scope),
      { initialProps: { scope: "srv-1" } },
    );
    act(() => {
      result.current.toggle("a");
    });
    expect([...result.current.expanded]).toEqual(["a"]);

    rerender({ scope: "srv-2" });
    expect([...result.current.expanded]).toEqual([]);
  });

  // The server list is not scoped to anything — its rows ARE the servers — so
  // it passes no scope and must never have its set cleared underneath it.
  it("never drops the set when there is no scope", () => {
    const { result, rerender } = renderHook(() => useExpandedRows());
    act(() => {
      result.current.toggle("a");
    });
    rerender();
    rerender();
    expect([...result.current.expanded]).toEqual(["a"]);
  });
});
