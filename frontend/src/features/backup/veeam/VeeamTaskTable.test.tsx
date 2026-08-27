import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { VeeamTaskTable } from "./VeeamTaskTable";
import type { VeeamTaskSession } from "../types/backup";

function task(over: Partial<VeeamTaskSession> = {}): VeeamTaskSession {
  return {
    id: "task-1",
    name: "ad01.ad.crjlab.net",
    state: "Stopped",
    result: "Success",
    result_message: "",
    algorithm: "Increment",
    duration: "00:04:11",
    processed_size: 0,
    transferred_size: 1048576,
    creation_time: "2026-08-26T23:00:31Z",
    end_time: "2026-08-26T23:04:42Z",
    cluster_id: null,
    vmid: null,
    ...over,
  };
}

function stubFetch(items: VeeamTaskSession[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ items, total: items.length }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
}

beforeEach(() => {
  stubFetch([]);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("VeeamTaskTable", () => {
  it("names which guest failed, and why", async () => {
    stubFetch([
      task({ id: "t1", name: "docker01.ad.crjlab.net", result: "Success" }),
      task({
        id: "t2",
        name: "ad01.ad.crjlab.net",
        result: "Failed",
        result_message:
          "Failed to prepare disks for backup: QEMU guest agent is not running",
      }),
    ]);

    renderWithProviders(
      <VeeamTaskTable
        serverId="srv-1"
        sessionVeeamId="20ff3c43-65c0-414d-ae03-10cf59fd2faa"
        enabled
        running={false}
      />,
    );

    // The whole point: a run reports one result, and its guests each have
    // their own.
    expect(await screen.findByText("Success")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    // The per-guest reason, which the run's own result never carries.
    expect(
      screen.getByText(/QEMU guest agent is not running/),
    ).toBeInTheDocument();
  });

  // An empty list is TWO different facts, and rendering them the same way
  // tells an operator the wrong one.
  it("distinguishes a run with no detail yet from one with none at all", async () => {
    stubFetch([]);
    const { rerender } = renderWithProviders(
      <VeeamTaskTable
        serverId="srv-1"
        sessionVeeamId="20ff3c43-65c0-414d-ae03-10cf59fd2faa"
        enabled
        running
      />,
    );
    expect(
      await screen.findByText(/fills in as the run progresses/i),
    ).toBeInTheDocument();

    rerender(
      <VeeamTaskTable
        serverId="srv-1"
        sessionVeeamId="20ff3c43-65c0-414d-ae03-10cf59fd2faa"
        enabled
        running={false}
      />,
    );
    expect(
      await screen.findByText(/kept no per-guest detail/i),
    ).toBeInTheDocument();
  });

  // A job that has never run has no session to open, which is a different
  // thing again from a run that reported nothing.
  it("says so when the job has never run", () => {
    renderWithProviders(
      <VeeamTaskTable
        serverId="srv-1"
        sessionVeeamId=""
        enabled
        running={false}
      />,
    );
    expect(screen.getByText(/has not run yet/i)).toBeInTheDocument();
  });

  // A task session carries no uuid, so an unresolved or ambiguous name is left
  // bare. Its absence is information — that guest is one Nexara cannot place.
  it("shows a VMID only for a guest it could place", async () => {
    stubFetch([
      task({
        id: "t1",
        name: "ad01.ad.crjlab.net",
        vmid: 113,
        cluster_id: "c1",
      }),
      task({ id: "t2", name: "orphan.ad.crjlab.net" }),
    ]);

    renderWithProviders(
      <VeeamTaskTable
        serverId="srv-1"
        sessionVeeamId="20ff3c43-65c0-414d-ae03-10cf59fd2faa"
        enabled
        running={false}
      />,
    );

    expect(await screen.findByText("113")).toBeInTheDocument();
    expect(screen.getByText("orphan.ad.crjlab.net")).toBeInTheDocument();
  });

  // Each call costs a fresh logon against the Veeam server, so a collapsed row
  // must fetch nothing at all.
  it("fetches nothing until the row is expanded", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    renderWithProviders(
      <VeeamTaskTable
        serverId="srv-1"
        sessionVeeamId="20ff3c43-65c0-414d-ae03-10cf59fd2faa"
        enabled={false}
        running={false}
      />,
    );

    expect(fetchMock).not.toHaveBeenCalled();
  });
});
