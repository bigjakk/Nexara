import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { queryClient } from "@/lib/query-client";
import { BackupJobTable } from "./BackupJobTable";
import type { BackupJob } from "../types/backup";

// The app's mutation-error net (lib/query-client.ts) toasts through sonner too,
// so this one mock sees every toast a run can raise.
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const CLUSTER = "c1";
const JOB_ID = "backup-1a2b3c4d";
const RUN_PATH = `POST /api/v1/clusters/${CLUSTER}/backup-jobs/${JOB_ID}/run`;
const UPID_01 = "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:vzdump::user@pam!test:";
const UPID_02 =
  "UPID:pve-02:0000B3C4:00BADA55:66F1A2B4:vzdump:101:user@pam!test:";

/** Every request the table sent, as "METHOD path" — the auth refresh aside. */
let sent: string[] = [];

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Answers every request with `answer`, recording it first. */
function stubFetchWith(answer: () => Promise<Response>) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      // No session in a test: the refresh fails and the request goes out
      // without a token, which the stub does not check.
      if (url === "/api/v1/auth/refresh") {
        return Promise.resolve(new Response("{}", { status: 401 }));
      }
      sent.push(`${init?.method ?? "GET"} ${url}`);
      return answer();
    }),
  );
}

function stubFetch(status: number, body: unknown) {
  stubFetchWith(() => Promise.resolve(jsonResponse(status, body)));
}

function job(over: Partial<BackupJob> = {}): BackupJob {
  return {
    id: JOB_ID,
    type: "vzdump",
    schedule: "sat 02:00",
    storage: "store01",
    mode: "snapshot",
    enabled: 1,
    ...over,
  };
}

// The app's own QueryClient, so a failed run reaches the operator through the
// same mutation-error toast it does in production.
function renderTable(jobs: BackupJob[]) {
  return render(
    <QueryClientProvider client={queryClient}>
      <BackupJobTable jobs={jobs} clusterId={CLUSTER} />
    </QueryClientProvider>,
  );
}

async function openConfirm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Run Now" }));
  return screen.findByRole("alertdialog");
}

beforeEach(() => {
  sent = [];
  stubFetch(200, {
    tasks: [],
    skipped: [],
    errors: [],
    unconfirmed: [],
    stops_running_backups: false,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  queryClient.clear();
});

describe("BackupJobTable — Run now", () => {
  // The Proxmox GUI asks "Start the selected backup job now?" before a run,
  // and so does this. The assertion that matters is that NOTHING is sent until
  // the operator agrees.
  it("asks first, and sends nothing until confirmed", async () => {
    const user = userEvent.setup();
    renderTable([job()]);

    const dialog = await openConfirm(user);
    expect(
      within(dialog).getByText(`Run backup job ${JOB_ID} now?`),
    ).toBeInTheDocument();
    expect(sent).toEqual([]);

    await user.click(within(dialog).getByRole("button", { name: "Run now" }));
    await waitFor(() => {
      expect(sent).toEqual([RUN_PATH]);
    });
  });

  it("sends nothing when cancelled", async () => {
    const user = userEvent.setup();
    renderTable([job()]);

    const dialog = await openConfirm(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(sent).toEqual([]);
  });

  // A second click while the first run is in flight would start a second run
  // on top of the first: the button stays disabled until the answer is in.
  it("will not run the job again while a run is pending", async () => {
    let release: () => void = () => undefined;
    stubFetchWith(
      () =>
        new Promise<Response>((resolve) => {
          release = () => {
            resolve(
              jsonResponse(200, {
                tasks: [{ node: "pve-01", upid: UPID_01 }],
                skipped: [],
                errors: [],
                unconfirmed: [],
                stops_running_backups: false,
              }),
            );
          };
        }),
    );
    const user = userEvent.setup();
    renderTable([job()]);

    const dialog = await openConfirm(user);
    await user.click(within(dialog).getByRole("button", { name: "Run now" }));
    await waitFor(() => {
      expect(sent).toEqual([RUN_PATH]);
    });

    const button = screen.getByRole("button", { name: "Run Now" });
    expect(button).toBeDisabled();
    await user.click(button);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(sent).toEqual([RUN_PATH]);

    release();
    await waitFor(() => {
      expect(button).toBeEnabled();
    });
    expect(sent).toEqual([RUN_PATH]);
  });

  it.each([
    {
      name: "a job pinned to a node, in stop mode",
      job: job({ node: "pve-02", mode: "stop" }),
      says: [
        "on node pve-02",
        "This job's mode is stop, which shuts running guests down for their backups.",
      ],
      not: ["on every online node", "suspend"],
    },
    {
      name: "an unpinned job, in suspend mode",
      job: job({ mode: "suspend" }),
      says: [
        "on every online node",
        "This job's mode is suspend, which suspends guests for their backups.",
      ],
      not: ["on node ", "mode is stop"],
    },
    {
      name: "an unpinned job, in snapshot mode",
      job: job(),
      says: ["on every online node", "older backups may be pruned"],
      not: ["on node ", "mode is"],
    },
  ])("says where $name runs and what it interrupts", async (tc) => {
    const user = userEvent.setup();
    renderTable([tc.job]);

    const dialog = await openConfirm(user);
    const text = dialog.textContent;
    for (const s of tc.says) expect(text).toContain(s);
    for (const s of tc.not) expect(text).not.toContain(s);
  });

  // A 200 can carry certain failures and unconfirmed nodes beside the tasks
  // that started: each outcome gets its own toast, a node that failed is never
  // folded into the success, and a node that did not confirm is never called a
  // failure — a backup may be running there.
  it.each<{
    name: string;
    body: unknown;
    success?: unknown[];
    warning?: unknown[];
    error?: unknown[];
  }>([
    {
      name: "tasks beside failed, unconfirmed and idle nodes",
      body: {
        tasks: [
          { node: "pve-01", upid: UPID_01 },
          { node: "pve-02", upid: UPID_02 },
        ],
        skipped: ["pve-03"],
        errors: [
          {
            node: "pve-04",
            message: "node is not online (Proxmox reports it as offline)",
          },
        ],
        unconfirmed: [
          { node: "pve-05", message: "Failed to connect to Proxmox" },
          {
            node: "pve-06",
            message: "Proxmox answered with status 596 and no message",
          },
        ],
        stops_running_backups: false,
      },
      success: [
        `Backup job ${JOB_ID} started 2 backup tasks: pve-01, pve-02.`,
        { description: "Nothing to back up on pve-03." },
      ],
      error: [
        `Backup job ${JOB_ID} could not start on 1 node: pve-04: node is not online (Proxmox reports it as offline)`,
      ],
      warning: [
        `Backup job ${JOB_ID} did not confirm a start on 2 nodes — check the task list before running it again: pve-05: Failed to connect to Proxmox; pve-06: Proxmox answered with status 596 and no message`,
      ],
    },
    {
      name: "one task and nothing else",
      body: {
        tasks: [{ node: "pve-01", upid: UPID_01 }],
        skipped: [],
        errors: [],
        unconfirmed: [],
        stops_running_backups: false,
      },
      success: [`Backup job ${JOB_ID} started 1 backup task: pve-01.`],
    },
    {
      // With stop set, every node that answered had any running backup
      // stopped first — the idle one included.
      name: "a task with stop set",
      body: {
        tasks: [{ node: "pve-02", upid: UPID_02 }],
        skipped: ["pve-01"],
        errors: [],
        unconfirmed: [],
        stops_running_backups: true,
      },
      success: [
        `Backup job ${JOB_ID} started 1 backup task: pve-02.`,
        {
          description:
            "Nothing to back up on pve-01. The job has stop set: any backup already running on pve-01, pve-02 was stopped first.",
        },
      ],
    },
    {
      // A refusal can come after vzdump's stop step — the storage permission
      // check does — so with stop set the failure must not read as "nothing
      // happened" either.
      name: "a refusal with stop set",
      body: {
        tasks: [{ node: "pve-01", upid: UPID_01 }],
        skipped: [],
        errors: [{ node: "pve-02", message: "Proxmox API: permission denied" }],
        unconfirmed: [],
        stops_running_backups: true,
      },
      success: [
        `Backup job ${JOB_ID} started 1 backup task: pve-01.`,
        {
          description:
            "The job has stop set: any backup already running on pve-01 was stopped first.",
        },
      ],
      error: [
        `Backup job ${JOB_ID} could not start on 1 node: pve-02: Proxmox API: permission denied — with stop set, vzdump may also have stopped a backup already running on a node it refused afterwards.`,
      ],
    },
    {
      // Nothing started and nothing failed: an empty task list must not read
      // as a click that was dropped.
      name: "nothing to back up anywhere",
      body: {
        tasks: [],
        skipped: ["pve-01", "pve-02"],
        errors: [],
        unconfirmed: [],
        stops_running_backups: false,
      },
      warning: [
        `Backup job ${JOB_ID} found nothing to back up on pve-01, pve-02.`,
      ],
    },
    {
      // ...and with stop set, it must not read as if nothing happened.
      name: "nothing to back up, with stop set",
      body: {
        tasks: [],
        skipped: ["pve-01", "pve-02"],
        errors: [],
        unconfirmed: [],
        stops_running_backups: true,
      },
      warning: [
        `Backup job ${JOB_ID} found none of its guests on pve-01, pve-02, but has stop set: any backup already running there was stopped.`,
      ],
    },
    {
      // A list the answer left out, or sent as null, reads as empty: reading
      // it must not throw and turn a running backup into an error toast.
      name: "an answer missing its lists",
      body: { tasks: [{ node: "pve-01", upid: UPID_01 }] },
      success: [`Backup job ${JOB_ID} started 1 backup task: pve-01.`],
    },
    {
      name: "an answer with null lists",
      body: {
        tasks: [{ node: "pve-01", upid: UPID_01 }],
        skipped: null,
        errors: null,
        unconfirmed: null,
        stops_running_backups: null,
      },
      success: [`Backup job ${JOB_ID} started 1 backup task: pve-01.`],
    },
  ])("reports $name", async (tc) => {
    stubFetch(200, tc.body);
    const user = userEvent.setup();
    renderTable([job()]);

    const dialog = await openConfirm(user);
    await user.click(within(dialog).getByRole("button", { name: "Run now" }));

    await waitFor(() => {
      expect(sent).toEqual([RUN_PATH]);
    });
    // Wait for the run to settle — the button spins until it does — so the
    // absent toasts below are absent because they were not raised, not
    // because the answer had not arrived yet.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Run Now" })).toBeEnabled();
    });
    for (const [level, want] of [
      ["success", tc.success],
      ["warning", tc.warning],
      ["error", tc.error],
    ] as const) {
      const fn = vi.mocked(toast[level]);
      if (want === undefined) {
        expect(fn, `toast.${level}`).not.toHaveBeenCalled();
      } else {
        expect(fn, `toast.${level}`).toHaveBeenCalledTimes(1);
        expect(fn, `toast.${level}`).toHaveBeenCalledWith(...want);
      }
    }
  });

  // No task confirmed and a node failed: the server answers 502 naming every
  // node, and the operator sees exactly that — through the app's
  // mutation-error toast, since the hook defines no onError of its own.
  it("shows why a run that confirmed no task failed", async () => {
    const message = `Backup job ${JOB_ID} did not confirm that any backup started — check the task list before running it again. Unconfirmed on pve-01: Failed to connect to Proxmox.`;
    stubFetch(502, { error: "bad_gateway", message });
    const user = userEvent.setup();
    renderTable([job()]);

    const dialog = await openConfirm(user);
    await user.click(within(dialog).getByRole("button", { name: "Run now" }));

    await waitFor(() => {
      expect(vi.mocked(toast.error)).toHaveBeenCalledWith(message);
    });
    expect(vi.mocked(toast.error)).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.success)).not.toHaveBeenCalled();
    expect(vi.mocked(toast.warning)).not.toHaveBeenCalled();
  });
});
