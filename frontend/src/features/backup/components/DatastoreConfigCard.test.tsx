import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { DatastoreConfigCard } from "./DatastoreConfigCard";
import type { PBSPruneJob } from "../types/backup";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), list: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);

const STORE = "Test-Backup-Datastore";

/** The datastore's own config, as PBS >= 2.2 reports it: no prune keys at all. */
const configWithoutPrune = { name: STORE, path: "/mnt/backup", "gc-schedule": "daily" };

function job(over: Partial<PBSPruneJob> = {}): PBSPruneJob {
  return { id: "j1", store: STORE, schedule: "daily", ...over };
}

describe("DatastoreConfigCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the prune job's schedule, retention and runs", async () => {
    mockedGet.mockResolvedValue(configWithoutPrune);
    mockedList.mockResolvedValue([
      job({ "keep-daily": 14, "last-run-endtime": 1787683680, "last-run-state": "OK", "next-run": 1787727600 }),
    ]);

    renderWithProviders(<DatastoreConfigCard pbsId="pbs-1" store={STORE} />);

    expect(await screen.findByText(/daily \(prune job\)/i)).toBeInTheDocument();
    expect(screen.getByText(/Keep Daily: 14/)).toBeInTheDocument();
    expect(screen.getByText(/Last Prune:/)).toBeInTheDocument();
    expect(screen.getByText(/Next Prune:/)).toBeInTheDocument();
    expect(screen.queryByText(/not set/i)).not.toBeInTheDocument();
  });

  it("ignores jobs belonging to another datastore", async () => {
    mockedGet.mockResolvedValue(configWithoutPrune);
    mockedList.mockResolvedValue([job({ store: "Other-Datastore" })]);

    renderWithProviders(<DatastoreConfigCard pbsId="pbs-1" store={STORE} />);
    expect(await screen.findByText(/not configured/i)).toBeInTheDocument();
  });
});
