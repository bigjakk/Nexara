import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { QueryStateNotice, QueryFailureNote } from "./QueryStateNotice";
import type { QueryStateLike } from "@/hooks/useSettledQueryError";
import { ApiClientError } from "@/lib/api-client";

/** A query that has settled successfully with no rows, unless overridden. */
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

/** What a 403 from a Proxmox-backed endpoint arrives as. */
function forbidden(message: string): ApiClientError {
  return new ApiClientError(403, { error: "forbidden", message });
}

function notice(query: QueryStateLike) {
  return render(
    <QueryStateNotice
      query={query}
      subject="ZFS pools"
      empty="No ZFS pools found."
    />,
  );
}

describe("QueryStateNotice", () => {
  it("reports a failure instead of an empty list", () => {
    // The whole point: a 403 and a genuinely empty node used to render the
    // same "No ZFS pools found." line.
    const { container } = notice(
      state({
        isSuccess: false,
        isError: true,
        error: forbidden("Permission check failed (/nodes/pve-01, Sys.Audit)"),
        errorUpdatedAt: 1000,
      }),
    );
    expect(container.textContent).toContain("Could not load ZFS pools.");
    expect(container.textContent).toContain("Sys.Audit");
    expect(container.textContent).not.toContain("No ZFS pools found.");
  });

  it("falls back to the bare status when the body carried no message", () => {
    // A proxy-generated 502 has no JSON body and, over HTTP/2, no statusText.
    const { container } = notice(
      state({
        isSuccess: false,
        isError: true,
        error: new ApiClientError(502, { error: "bad_gateway", message: "" }),
        errorUpdatedAt: 1000,
      }),
    );
    expect(container.textContent).toContain("HTTP 502");
  });

  it("keeps the failure on screen through the refetch that clears it", () => {
    // TanStack nulls `error` and resets status to "pending" whenever it
    // refetches a query with no data, so reading isError alone flips the
    // notice back to a skeleton on every poll.
    const error = forbidden("denied");
    const { container, rerender } = render(
      <QueryStateNotice
        query={state({
          isSuccess: false,
          isError: true,
          error,
          errorUpdatedAt: 1000,
        })}
        subject="ZFS pools"
        empty="No ZFS pools found."
      />,
    );
    expect(container.textContent).toContain("Could not load ZFS pools.");

    rerender(
      <QueryStateNotice
        query={state({
          isSuccess: false,
          isLoading: true,
          error: null,
          errorUpdatedAt: 1000,
          fetchStatus: "fetching",
        })}
        subject="ZFS pools"
        empty="No ZFS pools found."
      />,
    );
    expect(container.textContent).toContain("Could not load ZFS pools.");
    expect(container.textContent).toContain("denied");
  });

  it("does not carry a remembered failure over to a different read", () => {
    // Same component instance, new node: the incoming query has never failed,
    // so its errorUpdatedAt is 0 and the remembered error must be dropped.
    const { container, rerender } = render(
      <QueryStateNotice
        query={state({
          isSuccess: false,
          isError: true,
          error: forbidden("denied"),
          errorUpdatedAt: 1000,
        })}
        subject="ZFS pools"
        empty="No ZFS pools found."
      />,
    );
    expect(container.textContent).toContain("denied");

    rerender(
      <QueryStateNotice
        query={state({ isSuccess: false, isLoading: true, errorUpdatedAt: 0 })}
        subject="ZFS pools"
        empty="No ZFS pools found."
      />,
    );
    expect(container.textContent).not.toContain("denied");
  });

  it("shows the empty message only for a read that succeeded", () => {
    const { container } = notice(state());
    expect(container.textContent).toContain("No ZFS pools found.");
  });

  it("renders a skeleton on first load", () => {
    const { container } = notice(
      state({ isSuccess: false, isLoading: true, fetchStatus: "fetching" }),
    );
    // Nothing to read yet, and the skeleton is the only thing drawn.
    expect(container.textContent).toBe("");
    expect(container.querySelector("div.h-24")).not.toBeNull();
  });

  it("says something for a paused retry rather than nothing", () => {
    // isLoading false, isError false, isSuccess false, no data — all at once.
    // Every hand-rolled version of this chain fell through it.
    const { container } = notice(
      state({ isSuccess: false, isPaused: true, fetchStatus: "paused" }),
    );
    expect(container.textContent).toContain("paused");
    expect(container.textContent).not.toContain("No ZFS pools found.");
  });

  it("says something for a query whose enabled gate is off, and offers a retry", () => {
    // A disabled query: pending, idle, no data, no error. This is what a
    // cluster-scoped card sees before a cluster is picked, and the state that
    // the hand-rolled chains rendered as "no items found" or as nothing.
    const { container } = notice(state({ isSuccess: false }));
    expect(container.textContent).toContain("has not read ZFS pools yet");
    expect(container.textContent).not.toContain("No ZFS pools found.");
    expect(container.querySelector("button")).not.toBeNull();
  });

  it("withholds the retry when the failure's retry is paused, and says why", () => {
    // fetchStatus "paused" would otherwise render a permanently disabled
    // "Retrying..." for a retry that is parked until the tab is refocused.
    const { container } = notice(
      state({
        isSuccess: false,
        isError: true,
        error: forbidden("denied"),
        errorUpdatedAt: 1000,
        isPaused: true,
        fetchStatus: "paused",
      }),
    );
    expect(container.textContent).toContain("Could not load ZFS pools.");
    expect(container.textContent).toContain("retry is paused");
    expect(container.querySelector("button")).toBeNull();
  });

  it("offers a retry, and withholds it while a fetch is already in flight", () => {
    const refetch = vi.fn();
    const failed = {
      isSuccess: false,
      isError: true,
      error: forbidden("denied"),
      errorUpdatedAt: 1000,
      refetch,
    };
    const { container, rerender } = render(
      <QueryStateNotice
        query={state(failed)}
        subject="ZFS pools"
        empty="No ZFS pools found."
      />,
    );
    const button = container.querySelector("button");
    expect(button?.disabled).toBe(false);
    button?.click();
    expect(refetch).toHaveBeenCalledTimes(1);

    // Query.fetch() early-returns for any non-idle fetchStatus, so a retry
    // offered mid-fetch would do nothing at all when clicked.
    rerender(
      <QueryStateNotice
        query={state({ ...failed, fetchStatus: "fetching" })}
        subject="ZFS pools"
        empty="No ZFS pools found."
      />,
    );
    expect(container.querySelector("button")?.disabled).toBe(true);
  });
});

describe("QueryFailureNote", () => {
  function note(query: QueryStateLike) {
    return render(<QueryFailureNote query={query} subject="HA rules" />);
  }

  it("stays out of the way until the read has actually failed", () => {
    // Loading and empty belong to whatever the caller is drawing next to it.
    expect(
      note(state({ isSuccess: false, isLoading: true })).container,
    ).toBeEmptyDOMElement();
    expect(note(state()).container).toBeEmptyDOMElement();
    expect(note(state({ isSuccess: false })).container).toBeEmptyDOMElement();
  });

  it("names the failure and offers a retry", () => {
    const refetch = vi.fn();
    const { container } = note(
      state({
        isSuccess: false,
        isError: true,
        error: forbidden("Permission check failed (/nodes/pve-01, Sys.Audit)"),
        errorUpdatedAt: 1000,
        refetch,
      }),
    );
    expect(container.textContent).toContain(
      "Could not load HA rules: Permission check failed",
    );
    container.querySelector("button")?.click();
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("keeps the failure through the refetch that clears it", () => {
    const { container, rerender } = render(
      <QueryFailureNote
        query={state({
          isSuccess: false,
          isError: true,
          error: forbidden("denied"),
          errorUpdatedAt: 1000,
        })}
        subject="HA rules"
      />,
    );
    rerender(
      <QueryFailureNote
        query={state({
          isSuccess: false,
          isLoading: true,
          errorUpdatedAt: 1000,
          fetchStatus: "fetching",
        })}
        subject="HA rules"
      />,
    );
    expect(container.textContent).toContain("denied");
  });
});
