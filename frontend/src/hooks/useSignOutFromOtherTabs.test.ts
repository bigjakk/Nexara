import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { confirmSession, USER_KEY } from "@/lib/api-client";
import { SIGNED_OUT_KEY, useAuthStore } from "@/stores/auth-store";
import { ADMIN, VIEWER } from "@/test/fake-server";
import { useSignOutFromOtherTabs } from "./useSignOutFromOtherTabs";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(
    {},
    { confirmSession: vi.fn(() => Promise.resolve()) },
  ),
);

type Auth = ReturnType<typeof useAuthStore.getState>;

// Every test's clock starts here, and the notes are stamped by it.
const NOW = Date.UTC(2026, 0, 1);
const mine = JSON.stringify(ADMIN);
const theirs = JSON.stringify(VIEWER);
const clearAuth = vi.fn<Auth["clearAuth"]>();
const logout = vi.fn<Auth["logout"]>(() => Promise.resolve());

function signedOut(user: string, revoked: boolean, ago = 0) {
  return JSON.stringify({ user, revoked, at: NOW - ago });
}

/** Another tab's write: storage as it is now, and the event it fired here. */
function anotherTab(
  storedNow: string | null,
  key: string | null,
  newValue: string | null,
) {
  if (storedNow === null) localStorage.removeItem(USER_KEY);
  else localStorage.setItem(USER_KEY, storedNow);
  window.dispatchEvent(
    new StorageEvent("storage", { key, newValue, storageArea: localStorage }),
  );
}

function listen() {
  renderHook(() => {
    useSignOutFromOtherTabs();
  });
}

/** What this tab did about it, once the moment it waits to ask has passed. */
function outcome(): string[] {
  vi.advanceTimersByTime(2_000);
  return [
    ...clearAuth.mock.calls.map(([options]) =>
      options?.byUser !== true
        ? "ends as if it expired"
        : options.keepStoredUser === true
          ? "ends, leaving the stored user"
          : "ends",
    ),
    ...vi.mocked(confirmSession).mock.calls.map(() => "asks the server"),
    // Quietly: the tabs have been told once.
    ...logout.mock.calls.map(([options]) =>
      options?.announce === false ? "signs out at the server" : "announces",
    ),
  ];
}

beforeEach(() => {
  vi.useFakeTimers({ now: NOW });
  localStorage.setItem(USER_KEY, mine);
  useAuthStore.setState({
    user: ADMIN,
    isAuthenticated: true,
    isLoggingOut: false,
    sessionBeganAt: 0,
    clearAuth,
    logout,
  });
});

afterEach(() => {
  vi.useRealTimers();
  clearAuth.mockClear();
  logout.mockClear();
  vi.mocked(confirmSession).mockClear();
  localStorage.clear();
});

describe("useSignOutFromOtherTabs", () => {
  it.each<[string, string[], string | null, string | null, string | null]>([
    // Revoked, and the sign-out removes the stored user there: one that names
    // this user here is on its way out, or a newer session's.
    [
      "signs this user out",
      ["ends, leaving the stored user"],
      null,
      SIGNED_OUT_KEY,
      signedOut(ADMIN.id, true),
    ],
    // The server never got it: sent from here, as the user asked there.
    [
      "signs out without reaching the server",
      ["signs out at the server"],
      null,
      SIGNED_OUT_KEY,
      signedOut(ADMIN.id, false),
    ],
    // Heard late, or someone else's cookie is in the jar by now: a revoke could
    // end a session that began after it, so it ends here only — and as its own
    // may be alive still, a stored user naming this user may be one a refresh
    // of it wrote back, and goes with it.
    [
      "signed this user out a while ago",
      ["ends"],
      mine,
      SIGNED_OUT_KEY,
      signedOut(ADMIN.id, false, 60_000),
    ],
    [
      "signs out while the stored user names someone else",
      ["ends"],
      theirs,
      SIGNED_OUT_KEY,
      signedOut(ADMIN.id, false),
    ],
    [
      "signs someone else out",
      [],
      mine,
      SIGNED_OUT_KEY,
      signedOut(VIEWER.id, true),
    ],
    ["signs someone else in", ["ends"], theirs, USER_KEY, theirs],
    // Gone with no sign-out to say why: a refusal there, or a resume that only
    // could not reach the server. Only the server can tell.
    ["removes the stored user", ["asks the server"], null, USER_KEY, null],
    ["clears the whole storage", ["asks the server"], null, null, null],
    // A refresh, here or there, has written it back since: the session answered.
    ["removes it, and it is back by now", [], mine, USER_KEY, null],
    // Or someone else has signed in, and that event is still on its way.
    [
      "removes it, and someone else is in by now",
      ["ends"],
      theirs,
      USER_KEY,
      null,
    ],
    ["rewrites it for the same user", [], mine, USER_KEY, mine],
    ["writes another key", [], mine, "nexara-console-tabs", theirs],
  ])(
    "when another tab %s, this one: %j",
    (_, expected, storedNow, key, newValue) => {
      listen();

      anotherTab(storedNow, key, newValue);

      expect(outcome()).toEqual(expected);
    },
  );

  it.each<[string, number, string[]]>([
    // Before it listened; its resume may have written the stored user back.
    [
      "follows a sign-out made while this page was loading",
      NOW - 5_000,
      ["ends, leaving the stored user"],
    ],
    // From before the page loaded, or before a sign-in on its login page, which
    // brings the console back in this same page.
    [
      "leaves a sign-out from before this tab's session began alone",
      NOW - 1_000,
      [],
    ],
  ])("%s", (_, sessionBeganAt, expected) => {
    useAuthStore.setState({ sessionBeganAt });
    localStorage.setItem(SIGNED_OUT_KEY, signedOut(ADMIN.id, true, 2_000));

    listen();

    expect(outcome()).toEqual(expected);
  });

  it("follows a sign-in by someone else that came before it listened", () => {
    localStorage.setItem(USER_KEY, theirs);

    listen();

    expect(outcome()).toEqual(["ends"]);
  });

  it("leaves a tab that is already signing out alone", () => {
    useAuthStore.setState({ isLoggingOut: true });
    listen();

    anotherTab(null, SIGNED_OUT_KEY, signedOut(ADMIN.id, false));

    expect(outcome()).toEqual([]);
  });

  it("leaves a tab that holds no session alone", () => {
    // A second clearAuth with no token held would remove their record.
    useAuthStore.setState({ user: null, isAuthenticated: false });
    listen();

    anotherTab(theirs, USER_KEY, theirs);

    expect(outcome()).toEqual([]);
  });
});
