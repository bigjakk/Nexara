import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

import { ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { createWrapper } from "@/test/test-utils";
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
import { useUpdateVirtioWinMirror } from "./virtio-win-queries";

/**
 * Saving the virtio-win source toasts a failure from the hook's own onError,
 * which stands the app's global net down for it (the two confirm-required
 * answers are prompts, not failures). TanStack runs that onError whenever the
 * answer lands, so a save answered after its session ended — a sign-out, an
 * expiry, another user signing in — must raise no toast: a toast raised after
 * the session ended is shown to whoever is signed in by then, with the server's
 * words in it. A confirm prompt raises none in either case.
 *
 * Every case runs with the real api-client and a session, and is paired with
 * the control that the same answer is reported when the session goes on.
 */

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const SAVE = "PUT /api/v1/virtio-win/mirror";
const REFUSED = "The mirror at mirror.example.com could not be reached.";

let server: FakeServer;
let held: ReturnType<typeof deferred<Response>>;

/**
 * The save submitted and held. `outcome` settles as how it ended; it is handed
 * back inside an object, since an async function returning a promise would wait
 * for it, and nothing answers it until the test says so.
 */
async function submitted() {
  const qc = createAppQueryClient();
  const { result } = renderHook(() => useUpdateVirtioWinMirror(), {
    wrapper: createWrapper({ client: qc, router: false }),
  });
  let outcome!: Promise<"succeeded" | "failed">;
  await act(async () => {
    outcome = result.current
      .mutateAsync({ base_url: "https://mirror.example.com/virtio-win" })
      .then(
        () => "succeeded" as const,
        () => "failed" as const,
      );
    await Promise.resolve();
  });
  // Sent in the session that is current now: what ends it comes after.
  await waitFor(() => {
    expect(server.times(SAVE)).toBe(1);
  });
  return { outcome };
}

function unreachable(): Response {
  return json({ error: "bad_gateway", message: REFUSED }, 502);
}

function confirmRequired(): Response {
  return json(
    {
      error: "insecure_source_confirm_required",
      message: "This source uses plain HTTP.",
    },
    422,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  server = installFakeServer();
  held = deferred<Response>();
  server.routes[SAVE] = () => held.promise;
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

describe("a virtio-win source save that is answered after its session ended", () => {
  it("control: reports a failure in the server's words when the session goes on", async () => {
    const { outcome } = await submitted();

    held.resolve(unreachable());
    expect(await outcome).toBe("failed");
    await settle();

    expect(toastsRaised()).toEqual([`error: ${REFUSED}`]);
  });

  it.each(SESSION_ENDS)(
    "raises no toast for a failure after %s",
    async (_, end) => {
      const { outcome } = await submitted();

      end();
      held.resolve(unreachable());
      expect(await outcome).toBe("failed");
      await settle();

      expect(toastsRaised()).toEqual([]);
    },
  );

  it("raises none for a confirm prompt, which is the card's to show", async () => {
    const { outcome } = await submitted();

    held.resolve(confirmRequired());
    expect(await outcome).toBe("failed");
    await settle();

    expect(toastsRaised()).toEqual([]);
  });
});

// TanStack types the context an onError is given as possibly absent, though it is
// always there once the hook's onMutate has run. A save whose session cannot be
// told is treated as one that has ended, so that is shown by calling the handler
// as a mutation that skipped onMutate would.
describe("the handler of a virtio-win source save, called by hand", () => {
  /** The mutation a save that went through left in the cache. */
  async function settledMutation() {
    const qc = createAppQueryClient();
    const { result } = renderHook(() => useUpdateVirtioWinMirror(), {
      wrapper: createWrapper({ client: qc, router: false }),
    });
    held.resolve(json({ base_url: "", effective_url: "", upstream_url: "" }));
    await act(async () => {
      await result.current.mutateAsync({ base_url: "" });
    });
    const mutation = qc.getMutationCache().getAll()[0];
    if (!mutation) throw new Error("the save left no mutation behind");
    return { qc, mutation };
  }

  const refused = () =>
    new ApiClientError(502, { error: "bad_gateway", message: REFUSED });

  it("control: reports a failure while the session it is told of goes on", async () => {
    const { qc, mutation } = await settledMutation();

    await mutation.options.onError?.(refused(), { base_url: "" }, () => false, {
      client: qc,
      meta: undefined,
    });

    expect(toastsRaised()).toEqual([`error: ${REFUSED}`]);
  });

  it("reports nothing once the session it is told of has ended", async () => {
    const { qc, mutation } = await settledMutation();

    await mutation.options.onError?.(refused(), { base_url: "" }, () => true, {
      client: qc,
      meta: undefined,
    });

    expect(toastsRaised()).toEqual([]);
  });

  it("reports nothing for a save whose session it is not told of", async () => {
    const { qc, mutation } = await settledMutation();

    await mutation.options.onError?.(refused(), { base_url: "" }, undefined, {
      client: qc,
      meta: undefined,
    });

    expect(toastsRaised()).toEqual([]);
  });
});
