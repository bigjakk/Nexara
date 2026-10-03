import { expect, vi } from "vitest";
import type { ReactElement } from "react";
import { act, render, waitFor } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { ApiClientError, clearTokens, storeTokens } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { ADMIN, VIEWER, authResponse, sleep } from "@/test/fake-server";

/**
 * What the tests of a save that settles after its dialog is gone share
 * (hooks/useSaveOutcome.ts, and every dialog and page that uses it).
 *
 * A test file that uses this mocks `@/lib/api-client` WITH the real module
 * spread in — `{ ...actual, apiClient: {...} }` — so that the session
 * (clearTokens, storeTokens, sessionScope) is the real one and only the
 * transport is stubbed, and mocks `sonner`, whose `toast.error` is where a
 * late failure ends up.
 */

/** The server's refusal, as a 403 carries it. */
export const DENIED = "Proxmox API permission denied";

export function denied(): ApiClientError {
  return new ApiClientError(403, { error: "forbidden", message: DENIED });
}

/**
 * Lets whatever is already queued run, one timer tick, inside act so that React
 * draws what it set: for looking at what did NOT happen once a request has
 * settled. Not test/fake-server's flush, which waits two ticks outside act, for
 * tests that run the real auth store. One tick is enough for what a settled
 * promise does in a microtask (a handler, a toast), and not for what TanStack
 * tells its observers from a timer of its own: wait for that with a query.
 */
export async function flushInAct(): Promise<void> {
  await act(async () => {
    await sleep(0);
  });
}

/**
 * How long, in ms, the test of a whole page waits for something to show. The page
 * renders cold in the first test of its file, and under load that takes longer
 * than the second that testing-library waits by default, and vi.waitFor too: a
 * full run on a busy machine caught the first test of such a file with the page
 * still showing its loading skeleton. A file that renders a whole page gives
 * testing-library this with `configure({ asyncUtilTimeout: PATIENCE_MS })`, which
 * is that file's alone, and waits with `waitUntil` where it would use vi.waitFor.
 * The kit itself keeps the defaults, for the files that render a section or a
 * dialog. A wait that is really stuck still ends the test, in 5 s instead of 1.
 */
export const PATIENCE_MS = 5000;

/** vi.waitFor, with the patience of `PATIENCE_MS`. */
export function waitUntil(check: () => void): Promise<void> {
  return vi.waitFor(check, { timeout: PATIENCE_MS });
}

/**
 * Waits for the one mutation a test has started to settle as a success, and
 * then for whatever its success queued: a refresh of the lists it changed, and
 * anything a handler did about it. For looking at what did NOT happen.
 */
export async function waitForSuccess(qc: QueryClient): Promise<void> {
  await waitFor(() => {
    expect(
      qc
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state.status),
    ).toEqual(["success"]);
  });
  await flushInAct();
}

/**
 * Renders on the app's own kind of client (test/app-query-client.ts), the only
 * one whose mutation cache raises the global error toast, so that "no toast"
 * means something. Returns the client, to read what its mutations did. A router
 * for a page that links.
 */
export function renderOnAppClient(
  ui: ReactElement,
  { router = false }: { router?: boolean } = {},
): { qc: QueryClient } {
  const qc = createAppQueryClient();
  const tree = <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
  render(router ? <MemoryRouter>{tree}</MemoryRouter> : tree);
  return { qc };
}

/**
 * Moves focus to `target` as a keyboard user does, with Tab. A pointer cannot
 * reach what a modal covers, but Tab can leave a modal whose focus was lost (the
 * button that had it was disabled by the request it sent) and walk to the
 * controls behind it, and Enter on one of them can open another dialog in place
 * of the one that is up: with nothing between the two closed. It is how a test
 * reaches that. It fails if Tab never arrives, so that a swap that did not
 * happen cannot be mistaken for one that did.
 */
export async function tabTo(
  user: UserEvent,
  target: HTMLElement,
): Promise<void> {
  for (let i = 0; i < 40; i++) {
    if (document.activeElement === target) return;
    await user.tab();
  }
  if (document.activeElement !== target) {
    throw new Error("Tab never reached the control it was sent to");
  }
}

/** Someone is signed in: the session the saves of a test are sent in. */
export function signIn(): void {
  storeTokens(authResponse(ADMIN));
}

/**
 * The two ways the session a save was sent in stops being the current one: the
 * user signs out, and somebody else signs in on the same browser — the case
 * where a toast would put the previous user's object names in the next user's
 * Toaster.
 */
export const SESSION_ENDINGS: [name: string, end: () => void][] = [
  [
    "a sign-out",
    () => {
      act(() => {
        clearTokens();
      });
    },
  ],
  [
    "someone else signing in",
    () => {
      act(() => {
        clearTokens();
        storeTokens(authResponse(VIEWER));
      });
    },
  ],
];

/** Puts the session back to nobody's, for the next test. */
export function signOutForGood(): void {
  clearTokens();
  localStorage.clear();
}
