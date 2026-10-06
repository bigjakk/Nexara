import { afterEach, beforeEach, expect, vi, type Mock } from "vitest";
import type { AuthResponse, User } from "@/types/api";
import {
  apiClient,
  clearTokens,
  setAuthFailureCallback,
  setAuthRefreshCallback,
} from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { useAuthStore } from "@/stores/auth-store";
import { removeFakeLocks } from "@/test/fake-lock-manager";
import {
  ADMIN,
  authResponse,
  callerOf,
  flush,
  installFakeServer,
  json,
  type FakeServer,
  type Route,
} from "@/test/fake-server";

/**
 * What the api-client and auth-store session tests share: the real modules with
 * only fetch replaced (fake-server.ts), the routes they stub, and the clock,
 * jitter and timers the back-offs run on.
 *
 * `server`, `onFailure` and `onRefresh` are rebuilt for every test: import the
 * binding, never keep a copy of its value. Every test starts from clearTokens(),
 * which latches the module "signed out" (a session ended in it), so a test of
 * what a fresh page does takes a copy of the module of its own (pageLoad).
 */

export const REFRESH = "POST /api/v1/auth/refresh";
export const LOGIN = "POST /api/v1/auth/login";
export const LOGOUT = "POST /api/v1/auth/logout";
export const LOGOUT_ALL = "POST /api/v1/auth/logout-all";
export const TOTP = "POST /api/v1/auth/totp/verify-login";
export const REGISTER = "POST /api/v1/auth/register";
export const X = "GET /api/v1/x";
export const Y = "GET /api/v1/y";
/** The lock the tabs of one browser take turns to refresh under. */
export const LOCK = "nexara:auth-refresh";

/** A token about to expire, so that the next request refreshes it first. */
export const nearExpiry = { expiresIn: 30 };

export let server: FakeServer;
export let onFailure: Mock<() => void>;
export let onRefresh: Mock<(res: AuthResponse) => void>;

let now = 0;
let draw = 0;

/** Moves on the clock the back-off reads (performance.now); needs `clock`. */
export function elapse(ms: number): void {
  now += ms;
}

/** What Math.random answers, which is the jitter of the waits; needs `random`. */
export function pinRandom(value: number): void {
  draw = value;
}

/** The pinned clock's reading, for a test that spies performance.now itself. */
export const clockNow = (): number => now;

interface SandboxOptions {
  /** performance.now reads the clock `elapse` moves, from 0. */
  clock?: boolean;
  /** Math.random answers what `pinRandom` says, from 0. */
  random?: boolean;
  /** setTimeout and clearTimeout are faked from the start (`go`, `pump`). */
  timers?: boolean;
}

function openSandbox({ clock, random, timers }: SandboxOptions) {
  localStorage.clear();
  clearTokens();
  server = installFakeServer();
  onFailure = vi.fn<() => void>();
  onRefresh = vi.fn<(res: AuthResponse) => void>();
  setAuthFailureCallback(onFailure);
  setAuthRefreshCallback(onRefresh);
  now = 0;
  draw = 0;
  if (clock === true)
    vi.spyOn(performance, "now").mockImplementation(() => now);
  if (random === true) vi.spyOn(Math, "random").mockImplementation(() => draw);
  if (timers === true) fakeTimers();
}

function closeSandbox() {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  removeFakeLocks();
  clearTokens();
  localStorage.clear();
}

/** A fake server, mocks for the two session callbacks, and a clean slate. */
export function installApiClientHarness(options: SandboxOptions = {}): void {
  beforeEach(() => {
    openSandbox(options);
  });
  afterEach(closeSandbox);
}

/** The auth store as a page that has signed nobody in finds it. */
export const SIGNED_OUT = {
  user: null,
  permissions: [],
  isAuthenticated: false,
  isLoading: false,
  isInitialized: false,
  totpPending: false,
  totpPendingToken: null,
  isLoggingOut: false,
  signedOutByUser: false,
};

/**
 * The same, around the real auth store, booted as main.tsx boots it (initialize()
 * registers its forced-logout and refresh callbacks) with no session cookie.
 * `reset` empties what a session leaves elsewhere, before and after each test;
 * `act` wraps a sign-in for a file that renders; `boot: false` leaves
 * initialize() to the test. Returns the sign-in the files share.
 */
export function installAuthStoreHarness({
  store,
  reset = () => undefined,
  act = (run) => run(),
  boot = true,
  ...sandbox
}: SandboxOptions & {
  store: typeof useAuthStore;
  reset?: () => void;
  act?: (run: () => Promise<void>) => Promise<unknown>;
  boot?: boolean;
}) {
  beforeEach(async () => {
    openSandbox(sandbox);
    reset();
    store.setState(SIGNED_OUT);
    if (!boot) return;
    server.routes[REFRESH] = () => json({}, 401);
    await store.getState().initialize();
  });
  afterEach(() => {
    // Mocks first: a test that made localStorage refuse writes would otherwise
    // fail the emptying of the stores that persist.
    closeSandbox();
    reset();
  });

  return {
    signInAs: async (
      user: User,
      {
        permissions = [],
        expiresIn = 3600,
      }: { permissions?: string[]; expiresIn?: number } = {},
    ): Promise<void> => {
      server.routes[LOGIN] = () =>
        json(authResponse(user, { permissions, expiresIn }));
      await act(async () => {
        await store
          .getState()
          .login({ email: user.email, password: "example-password" });
      });
    },
  };
}

/** Fakes setTimeout and clearTimeout alone: promises run as they are. */
export function fakeTimers(): void {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
}

/** Runs the fake timers on by `ms`, and what waits on them with it. */
export const go = (ms = 0) => vi.advanceTimersByTimeAsync(ms);

/** Lets everything that is ready run, without moving the clock. */
export async function pump(): Promise<void> {
  for (let i = 0; i < 5; i++) await go();
}

/** What a request settles as: the error it failed with, or `ok`. */
export function settle(request: Promise<unknown>, ok = "sent") {
  return request.then(
    () => ok,
    (err: unknown) => err,
  );
}

/** `request`'s outcome, or "stalled" if it is still out once everything ready has run. */
export function orStalled(
  request: Promise<unknown>,
  tick: () => Promise<unknown> = flush,
) {
  return Promise.race([request, tick().then(() => "stalled")]);
}

/** Resolves once the SPA has sent `key` `times` times. */
export function untilSent(key: string, times = 1) {
  return vi.waitFor(() => {
    expect(server.times(key)).toBe(times);
  });
}

export const expired = () =>
  json({ error: "unauthorized", message: "expired" }, 401);
export const down = () => json({ error: "x", message: "down" }, 503);
export const session = () => json(authResponse(ADMIN));
export const superseded = () =>
  json(
    {
      error: "refresh_superseded",
      message: "the refresh token was superseded by a newer one",
    },
    409,
  );

/** A 429, with the Retry-After it carries, if any. */
export function tooMany(retryAfter: string | null = null): Route {
  return () =>
    new Response(
      JSON.stringify({
        error: "too_many_requests",
        message: "Too Many Requests",
      }),
      {
        status: 429,
        headers: {
          "Content-Type": "application/json",
          ...(retryAfter === null ? {} : { "Retry-After": retryAfter }),
        },
      },
    );
}

/** The answers in order; the last one repeats. */
export function answers(...list: Route[]): Route {
  let n = 0;
  return (init) => {
    const answer = list[Math.min(n, list.length - 1)];
    n++;
    if (answer === undefined) throw new Error("no answer to give");
    return answer(init);
  };
}

/** X refuses its first `refusals` requests as an expired token, then answers as the caller. */
export function expiresAfter(refusals: number): Route {
  let reads = 0;
  return (init) =>
    ++reads <= refusals ? expired() : json({ owner: callerOf(init) });
}

export type Client = typeof import("@/lib/api-client");

/** A copy of the api-client of its own: its token, epoch and refresh in flight. */
export async function freshClient(): Promise<Client> {
  vi.resetModules();
  return import("@/lib/api-client");
}

/**
 * The api-client as a page load finds it: nobody signed in, and no session
 * ended in it yet, so nothing stops a request trying the refresh cookie. Its
 * classes are its own: recognise its errors by name, or by its own classes.
 */
export async function pageLoad(): Promise<Client> {
  const client = await freshClient();
  client.setAuthFailureCallback(onFailure);
  client.setAuthRefreshCallback(onRefresh);
  return client;
}

/** GET /api/v1/x through `client` (the singleton, or a copy of it). */
export function getX(client: Pick<Client, "apiClient"> = { apiClient }) {
  return client.apiClient.get<{ owner: string }>(apiPath`/api/v1/x`);
}

/** The same read, as it settles. */
export function readX(client?: Pick<Client, "apiClient">) {
  return settle(getX(client));
}
