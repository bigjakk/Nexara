import { vi } from "vitest";
import { act } from "@testing-library/react";
import { toast } from "sonner";
import { clearTokens, storeTokens } from "@/lib/api-client";
import { ADMIN, VIEWER, authResponse } from "@/test/fake-server";

/**
 * What the tests of a failure that settles late share: the session it was sent
 * in, and the ways that session can stop being the current one before the
 * answer comes. A toast that outlives its session reaches whoever is signed in
 * next, so each of those tests ends the session with one of these between the
 * request and its answer, and pairs that with the same answer in a session that
 * goes on.
 *
 * They are api-client's own moves, the ones its session tests make
 * (lib/api-client.session.test.ts): clearTokens() ends a session, storeTokens()
 * begins one, and a session that is replaced is over whether or not anyone
 * signed out in between.
 */

/** Signs ADMIN in, on a module that has no session or an old one. */
export function signInAsAdmin(): void {
  clearTokens();
  storeTokens(authResponse(ADMIN));
}

/** Leaves nobody signed in, as a test's last act. */
export function signOutForGood(): void {
  clearTokens();
}

/**
 * The ways the session ADMIN signed in at the start of the test can end:
 * a sign-out, another user's session taking its place, and the same user
 * signing in again, which is another session all the same.
 */
export const SESSION_ENDS: [name: string, end: () => void][] = [
  [
    "a sign-out",
    () => {
      clearTokens();
    },
  ],
  [
    "another user signing in",
    () => {
      storeTokens(authResponse(VIEWER));
    },
  ],
  [
    "the same user signing in again",
    () => {
      clearTokens();
      storeTokens(authResponse(ADMIN));
    },
  ],
];

/**
 * Lets whatever is already queued run, timers included, and React draw what it
 * set: for looking at what did NOT happen once a request has settled.
 */
export async function settle(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/**
 * Every toast raised so far on a sonner the test has mocked, of any kind the mock
 * carries (toast itself, success, info, warning, error, message, loading; one it
 * lacks is skipped), as "kind: message": for a test that says a run raised exactly
 * these, or none, whatever the kind — so that "toasts nothing" is not satisfied by
 * a success or a warning.
 */
export function toastsRaised(): string[] {
  const kinds: [kind: string, spy: unknown][] = [
    ["default", toast],
    ["success", toast.success],
    ["info", toast.info],
    ["warning", toast.warning],
    ["error", toast.error],
    ["message", toast.message],
    ["loading", toast.loading],
  ];
  return kinds.flatMap(([kind, spy]) =>
    vi.isMockFunction(spy)
      ? (spy.mock.calls as unknown[][]).map(
          (args) => `${kind}: ${String(args[0])}`,
        )
      : [],
  );
}
