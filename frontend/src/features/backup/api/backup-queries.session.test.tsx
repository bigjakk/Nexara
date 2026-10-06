import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { QueryClient } from "@tanstack/react-query";

import { createAppQueryClient } from "@/test/app-query-client";
import { createWrapper } from "@/test/test-utils";
import type { BackupJobRunResult } from "../types/backup";
import {
  deferred,
  installFakeServer,
  json,
  type FakeServer,
} from "@/test/fake-server";
import {
  SESSION_ENDS,
  settle,
  signInAsAdmin,
  signOutForGood,
  toastsRaised,
} from "@/test/late-toast-sessions";
import { useRunBackupJob } from "./backup-queries";

/**
 * Run now answers with toasts that name the job and the nodes it ran on, raised
 * by the hook's own onSuccess — which TanStack runs whenever the answer lands,
 * after the page that asked has gone and a sign-out with it. A toast raised
 * after the session ended is shown to whoever is signed in by then, so a run
 * answered after its session ended must raise none, or the next user is shown
 * the previous one's job and nodes. A run that fails is the global net's to
 * report, and keeps to the same rule (lib/query-client.ts).
 *
 * Every case runs on the app's own kind of client (test/app-query-client.ts),
 * with the real api-client and a session, and is paired with the control that
 * the same answer toasts when the session goes on.
 */

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const CLUSTER = "cluster01";
const JOB = "backup-job01";
const RUN = `POST /api/v1/clusters/${CLUSTER}/backup-jobs/${JOB}/run`;
const STARTED = `Backup job ${JOB} started 1 backup task: pve-01.`;
const UPID = "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:vzdump::admin@pve:";

let server: FakeServer;
let held: ReturnType<typeof deferred<Response>>;

function runStarted(): Response {
  return json({
    tasks: [{ node: "pve-01", upid: UPID }],
    skipped: [],
    errors: [],
    unconfirmed: [],
    stops_running_backups: false,
  });
}

/** The hook on the app's kind of client, with the run submitted and held. */
async function submitted(): Promise<{
  qc: QueryClient;
  outcome: Promise<"succeeded" | "failed">;
}> {
  const qc = createAppQueryClient();
  const { result } = renderHook(() => useRunBackupJob(), {
    wrapper: createWrapper({ client: qc, router: false }),
  });
  let outcome!: Promise<"succeeded" | "failed">;
  await act(async () => {
    outcome = result.current
      .mutateAsync({ clusterId: CLUSTER, jobId: JOB })
      .then(
        () => "succeeded" as const,
        () => "failed" as const,
      );
    await Promise.resolve();
  });
  // Sent, in the session that is current now: what ends it comes after.
  await waitFor(() => {
    expect(server.times(RUN)).toBe(1);
  });
  return { qc, outcome };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  server = installFakeServer();
  held = deferred<Response>();
  server.routes[RUN] = () => held.promise;
  // No refresh is expected: ADMIN is signed in with a token that has an hour to
  // run. Should one be asked for all the same, it fails as a dead session's does,
  // and is not answered as if it were a session.
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  signInAsAdmin();
});

afterEach(() => {
  vi.unstubAllGlobals();
  signOutForGood();
  localStorage.clear();
});

describe("a backup job run that is answered after its session ended", () => {
  it("control: reports what started, naming the job and the node, when the session goes on", async () => {
    const { outcome } = await submitted();

    held.resolve(runStarted());
    expect(await outcome).toBe("succeeded");
    await settle();

    expect(toastsRaised()).toEqual([`success: ${STARTED}`]);
  });

  it.each(SESSION_ENDS)("raises no toast after %s", async (_, end) => {
    const { outcome } = await submitted();

    end();
    held.resolve(runStarted());
    // The run did start and was answered: only the telling is dropped.
    expect(await outcome).toBe("succeeded");
    await settle();

    expect(toastsRaised()).toEqual([]);
  });

  it("control: reports a run that failed through the app's own net when the session goes on", async () => {
    const { outcome } = await submitted();

    held.resolve(
      json(
        { error: "bad_gateway", message: `pve-01: could not start ${JOB}` },
        502,
      ),
    );
    expect(await outcome).toBe("failed");
    await settle();

    expect(toastsRaised()).toEqual([`error: pve-01: could not start ${JOB}`]);
  });

  it.each(SESSION_ENDS)(
    "raises no toast for a run that failed after %s",
    async (_, end) => {
      const { outcome } = await submitted();

      end();
      held.resolve(
        json(
          { error: "bad_gateway", message: `pve-01: could not start ${JOB}` },
          502,
        ),
      );
      expect(await outcome).toBe("failed");
      await settle();

      expect(toastsRaised()).toEqual([]);
    },
  );
});

// TanStack types the context an onSuccess is given as always there, and it is
// once the hook's onMutate has run. A run whose session cannot be told is treated
// as one that has ended, as the other hook-level sites treat one, so the handler
// is called here as a mutation that skipped onMutate would call it.
describe("the handler of a backup run, called by hand", () => {
  const variables = { clusterId: CLUSTER, jobId: JOB };
  const started: BackupJobRunResult = {
    tasks: [{ node: "pve-01", upid: UPID }],
    skipped: [],
    errors: [],
    unconfirmed: [],
    stops_running_backups: false,
  };

  /** The mutation a run that went through left in the cache, its own toast cleared. */
  async function settledMutation() {
    const qc = createAppQueryClient();
    const { result } = renderHook(() => useRunBackupJob(), {
      wrapper: createWrapper({ client: qc, router: false }),
    });
    held.resolve(runStarted());
    await act(async () => {
      await result.current.mutateAsync(variables);
    });
    const mutation = qc.getMutationCache().getAll()[0];
    if (!mutation) throw new Error("the run left no mutation behind");
    // What the run itself reported is not what is counted below.
    vi.clearAllMocks();
    return { qc, mutation };
  }

  it("control: reports a run while the session it is told of goes on", async () => {
    const { qc, mutation } = await settledMutation();

    await mutation.options.onSuccess?.(started, variables, () => false, {
      client: qc,
      meta: undefined,
    });

    expect(toastsRaised()).toEqual([`success: ${STARTED}`]);
  });

  it("reports nothing once the session it is told of has ended", async () => {
    const { qc, mutation } = await settledMutation();

    await mutation.options.onSuccess?.(started, variables, () => true, {
      client: qc,
      meta: undefined,
    });

    expect(toastsRaised()).toEqual([]);
  });

  it("reports nothing for a run whose session it is not told of", async () => {
    const { qc, mutation } = await settledMutation();

    await mutation.options.onSuccess?.(started, variables, undefined, {
      client: qc,
      meta: undefined,
    });

    expect(toastsRaised()).toEqual([]);
  });
});
