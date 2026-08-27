import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { VeeamJobTable } from "./VeeamJobTable";
import { VeeamSessionTable } from "./VeeamSessionTable";
import type { VeeamJob, VeeamSession } from "../types/backup";

function job(over: Partial<VeeamJob> = {}): VeeamJob {
  return {
    id: "job-1",
    veeam_id: "3953c24f-bbe6-41fc-ae2f-a34e25bfd614",
    name: "Onsite_Daily",
    job_type: "ProxmoxBackupJob",
    workload: "Vm",
    description: "",
    status: "Stopped",
    last_result: "Success",
    last_run: "2026-08-26T22:00:29Z",
    next_run: "2026-08-27T22:00:00Z",
    next_run_policy: "8/27/2026 10:00 PM",
    repository_name: "repo-nas-01",
    objects_count: 3,
    progress_percent: 100,
    bottleneck: "Source",
    duration: "00:18:27",
    processing_rate: "268 MB",
    processed_size: 1046898278400,
    read_size: 274600034304,
    transferred_size: 8739400042,
    cluster_id: null,
    last_seen_at: "2026-08-26T23:00:00Z",
    running_session_id: "",
    running_session_state: "",
    ...over,
  };
}

function session(over: Partial<VeeamSession> = {}): VeeamSession {
  return {
    id: "sess-1",
    veeam_id: "20ff3c43-65c0-414d-ae03-10cf59fd2faa",
    name: "Onsite_Daily",
    state: "Stopped",
    result: "Success",
    result_message: "Success",
    algorithm: "Increment",
    bottleneck: "Source",
    duration: "00:10:00",
    processing_rate: "33.3 MB",
    processed_size: 96636764160,
    read_size: 13931380736,
    transferred_size: 2628327455,
    progress_percent: 100,
    creation_time: "2026-08-26T19:56:00Z",
    end_time: "2026-08-26T20:06:01Z",
    initiated_by: "SYSTEM",
    nexara_initiated: false,
    nexara_stopped: false,
    cluster_id: null,
    ...over,
  };
}

// The expanded session row reads the run's log live from the Veeam server.
// Answered with an empty listing here: that is the shape a stopped run really
// returns, and it keeps these tests off the network.
beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ items: [], total: 0 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
});

describe("VeeamJobTable", () => {
  it("shows job state and result without expanding", () => {
    renderWithProviders(<VeeamJobTable serverId="srv-1" jobs={[job()]} />);

    expect(screen.getByText("Onsite_Daily")).toBeInTheDocument();
    expect(screen.getByText("Success")).toBeInTheDocument();
    expect(screen.getByText("repo-nas-01")).toBeInTheDocument();
  });

  // The single most important labelling rule in the feature. Veeam records a
  // job cancelled through its own API as result "Failed" with isCanceled
  // false and an empty log, so "Failed" alone would be a confident lie.
  it("never labels a failure without acknowledging it may be a cancellation", () => {
    renderWithProviders(
      <VeeamJobTable
        serverId="srv-1"
        jobs={[job({ last_result: "Failed" })]}
      />,
    );

    expect(screen.getByText("Failed or cancelled")).toBeInTheDocument();
    expect(screen.queryByText(/^Failed$/)).not.toBeInTheDocument();
  });

  it("reveals throughput and bottleneck when expanded", async () => {
    const user = userEvent.setup();
    renderWithProviders(<VeeamJobTable serverId="srv-1" jobs={[job()]} />);

    expect(screen.queryByText("00:18:27")).not.toBeInTheDocument();
    await user.click(screen.getByText("Onsite_Daily"));

    expect(screen.getByText("00:18:27")).toBeInTheDocument();
    expect(screen.getByText("268 MB")).toBeInTheDocument();
    // Veeam's own bottleneck analysis, rendered next to Nexara's own metrics.
    expect(screen.getByText("Source")).toBeInTheDocument();
  });

  // "NotDefined" and "N/A" are Veeam's placeholders; rendering them verbatim
  // reads as data when it is the absence of data.
  it("renders Veeam's placeholder values as an em dash", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamJobTable
        serverId="srv-1"
        jobs={[job({ bottleneck: "NotDefined", processing_rate: "N/A" })]}
      />,
    );
    await user.click(screen.getByText("Onsite_Daily"));

    expect(screen.queryByText("NotDefined")).not.toBeInTheDocument();
    expect(screen.queryByText("N/A")).not.toBeInTheDocument();
  });

  it("shows a progress bar only while a job is running", () => {
    const { rerender } = renderWithProviders(
      <VeeamJobTable
        serverId="srv-1"
        jobs={[job({ status: "Running", progress_percent: 40 })]}
      />,
    );
    expect(screen.getByText("Running")).toBeInTheDocument();

    rerender(
      <VeeamJobTable serverId="srv-1" jobs={[job({ status: "Stopped" })]} />,
    );
    expect(screen.queryByText("Running")).not.toBeInTheDocument();
  });

  // progress_percent is written by the inventory pass, the same one that
  // writes status. A run derived from a live session has the PREVIOUS run's
  // number — normally 100 — so drawing a bar for it would show a backup that
  // started seconds ago as finished.
  it("draws no progress bar for a run Veeam has not confirmed yet", () => {
    const { rerender } = renderWithProviders(
      <VeeamJobTable
        serverId="srv-1"
        jobs={[
          job({
            status: "Stopped",
            progress_percent: 100,
            running_session_id: "20ff3c43-65c0-414d-ae03-10cf59fd2faa",
          }),
        ]}
      />,
    );
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.queryByTestId("job-progress")).not.toBeInTheDocument();

    // Once Veeam confirms the run, the number describes it and the bar returns.
    rerender(
      <VeeamJobTable
        serverId="srv-1"
        jobs={[job({ status: "Running", progress_percent: 40 })]}
      />,
    );
    expect(screen.getByTestId("job-progress")).toBeInTheDocument();
  });

  it("says so when a server has no Proxmox jobs", () => {
    renderWithProviders(<VeeamJobTable serverId="srv-1" jobs={[]} />);
    expect(screen.getByText(/No Proxmox backup jobs/i)).toBeInTheDocument();
  });
});

describe("VeeamSessionTable", () => {
  it("distinguishes a Nexara-initiated stop from an unexplained failure", () => {
    const { rerender } = renderWithProviders(
      <VeeamSessionTable
        serverId="srv-1"
        sessions={[session({ result: "Failed", nexara_stopped: true })]}
      />,
    );
    // Nexara recorded the stop itself, so it can say so precisely.
    expect(screen.getByText("Stopped from Nexara")).toBeInTheDocument();

    rerender(
      <VeeamSessionTable
        serverId="srv-1"
        sessions={[session({ result: "Failed", nexara_stopped: false })]}
      />,
    );
    // It cannot for a stop made from the Veeam console.
    expect(screen.getByText("Failed or cancelled")).toBeInTheDocument();
  });

  // The reason nexara_initiated and nexara_stopped are two columns and not
  // one. A run Nexara STARTED can fail for a completely real reason — a full
  // repository, an unreachable worker — and labelling that "Stopped from
  // Nexara" would tell an operator to ignore a genuine backup failure.
  it("does not excuse a failure just because Nexara started the run", () => {
    renderWithProviders(
      <VeeamSessionTable
        serverId="srv-1"
        sessions={[
          session({
            result: "Failed",
            nexara_initiated: true,
            nexara_stopped: false,
          }),
        ]}
      />,
    );

    expect(screen.queryByText("Stopped from Nexara")).not.toBeInTheDocument();
    expect(screen.getByText("Failed or cancelled")).toBeInTheDocument();
  });

  it("explains the ambiguity in the expanded row, but only when it applies", async () => {
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <VeeamSessionTable
        serverId="srv-1"
        sessions={[session({ result: "Failed" })]}
      />,
    );
    await user.click(screen.getByText("Onsite_Daily"));
    expect(
      screen.getByText(/same way it records a genuine failure/i),
    ).toBeInTheDocument();

    rerender(
      <VeeamSessionTable
        serverId="srv-1"
        sessions={[session({ result: "Success" })]}
      />,
    );
    expect(
      screen.queryByText(/same way it records a genuine failure/i),
    ).not.toBeInTheDocument();
  });

  it("shows the backup mode and transferred size", () => {
    renderWithProviders(
      <VeeamSessionTable serverId="srv-1" sessions={[session()]} />,
    );
    expect(screen.getByText("Increment")).toBeInTheDocument();
    expect(screen.getByText("00:10:00")).toBeInTheDocument();
  });
});
