import type { ApiError, AuthResponse } from "@/types/api";
import {
  apiPath,
  assertApiPath,
  PathSegmentError,
  type ApiPath,
} from "@/lib/api-path";

// Cached user metadata for instant render after a hard refresh. Not security
// sensitive — the JWT is the actual auth gate; this is just so the SPA can
// show "Welcome, alice" before /auth/refresh resolves. Shared by every tab of
// the browser, as the refresh cookie is, so it also names whose session that
// cookie belongs to (auth-store's logout() reads it for that, and clearTokens()
// leaves another user's alone).
const USER_KEY = "nexara_user";

// Legacy localStorage keys used by builds before the cookie migration. We
// purge any value at these keys on storeTokens / clearTokens so existing
// users do not carry their pre-upgrade access/refresh tokens around in
// JS-reachable storage indefinitely.
const LEGACY_TOKEN_KEYS = [
  "access_token",
  "refresh_token",
  "expires_at",
  "user",
] as const;

function purgeLegacyTokenKeys() {
  for (const key of LEGACY_TOKEN_KEYS) {
    localStorage.removeItem(key);
  }
}

// Access token lives in this module's closure only — never written to
// localStorage or any DOM-reachable storage. The HttpOnly refresh cookie set
// by the server is the persistent auth artefact across reloads.
let accessTokenInMemory: string | null = null;
let accessTokenExpiresAt = 0;
// Whose token that is, so a refresh that answers for someone else is known for
// what it is: another session (see refreshTokens).
let accessTokenUserId: string | null = null;

// One-shot legacy cleanup on module load — covers the SPA boot path before
// any login/refresh runs.
purgeLegacyTokenKeys();

let onAuthFailure: (() => void) | null = null;
let onAuthRefresh: ((res: AuthResponse) => void) | null = null;
let refreshPromise: Promise<AuthResponse> | null = null;
// What kind the refresh in flight is: a courtesy, which another tab holding the
// lock skips (withRefreshLock), or one that is needed, which waits. Who may join
// it depends on which (refreshAhead, refreshNeeded). Only read while
// refreshPromise is set, and set with it (startRefresh).
let refreshIsCourtesy = false;

// A refresh that failed without the server refusing the session, and what it
// failed with, until the moment (performance.now()) the next one may be sent.
// Belongs to the session it was recorded for, like refreshPromise: a session
// beginning or ending drops it (storeTokens, clearTokens). See refreshTokens.
let refreshBackoff: { until: number; failure: unknown } | null = null;

// Which session the token above, and the refresh in flight, belong to. It
// changes when a session begins (storeTokens, which a refresh answering for
// someone else also does) and when one ends (clearTokens), and not when a
// refresh rotates the tokens of the session it belongs to.
//
// Nothing else ties an answer to the session that asked for it. A refresh
// started before Sign out would land after it and sign the user back in, or
// land after the next user has signed in and replace their token and identity
// with the previous one's. So whatever starts under one epoch and finishes
// under another belongs to a session that has ended, and must not touch the
// current one: it fails with a StaleSessionError instead.
let sessionEpoch = 0;

/**
 * The epoch of the session now current (see sessionEpoch). Work that outlives
 * a component — a loop of requests — takes it before it starts and stops once
 * it has changed, rather than carry on as whoever is signed in next.
 */
export function currentSessionEpoch(): number {
  return sessionEpoch;
}

/**
 * `ended()` is false until the session now current ends or is replaced, and
 * true from then on. Work that outlives a component — a loop of requests, a
 * wait between two of them — takes one before it starts and stops once it says
 * so, rather than carry on as whoever is signed in next:
 *
 *   const ended = sessionScope();
 *   for (const item of items) {
 *     if (ended()) break;
 *     await apiClient.post(...);
 *   }
 */
export function sessionScope(): () => boolean {
  const session = sessionEpoch;
  return () => sessionEpoch !== session;
}

// True from the moment a session ended here (clearTokens) until the next one
// begins (storeTokens): nobody is signed in, and only a sign-in may change that.
// The refresh cookie outlives a session the server could not be told about (the
// logout request failed), and a request that finds nobody signed in would
// otherwise use it to resume that very session: its refresh starts after the
// sign-out, so it is nobody's stale answer and nothing above would drop it.
// The module starts unlatched, and auth-store initialize() decides what a page
// starts as: with no stored user it latches (clearTokens), so only a sign-in
// begins a session on it; with one it resumes through resumeSession, which
// sends the refresh itself and does not ask for a token, so is not affected
// either way, and storeTokens takes it from there. What is left unlatched is
// the module before initialize() has run.
let signedOut = false;

/**
 * Something that belonged to a session that has since ended or been replaced:
 * a refresh answered after Sign out, a request whose token was being refreshed
 * for the session that ended. It says nothing about the session now current,
 * so nothing that receives it ends that session.
 */
export class StaleSessionError extends Error {
  constructor() {
    super("The session this belonged to has ended");
    this.name = "StaleSessionError";
  }
}

export function setAuthFailureCallback(cb: () => void) {
  onAuthFailure = cb;
}

/**
 * Registers a callback invoked on every successful background refresh
 * (Finding A11). The auth-store uses this to re-hydrate user + permissions
 * from the refresh response so a permission rotation propagates to the SPA
 * within one access-token lifetime, without forcing a logout.
 */
export function setAuthRefreshCallback(cb: (res: AuthResponse) => void) {
  onAuthRefresh = cb;
}

function writeTokens(res: AuthResponse) {
  accessTokenInMemory = res.access_token;
  accessTokenExpiresAt = res.expires_at;
  accessTokenUserId = res.user.id;
  // The cached user only seeds the render after a reload (see USER_KEY), and a
  // full quota refuses it. That must not fail the session this is the start
  // of: the token above is already in use, and a throw here would leave it
  // there for a session whose caller never got to apply it.
  try {
    localStorage.setItem(USER_KEY, JSON.stringify(res.user));
  } catch {
    // The reload resumes off the cookie, or asks for a sign-in.
  }
  purgeLegacyTokenKeys();
}

/**
 * A session begins: a sign-in, the SSO callback, TOTP, registration, a resume
 * at boot. Everything started under the previous one — a refresh in flight, a
 * request — is that one's from here on (see sessionEpoch), and a caller that
 * wants a refresh no longer joins that one's, nor waits out the back-off that
 * its failure began: a session that has just signed in is never made to wait
 * for the previous one's refresh to be tried again. A refresh rotating the
 * tokens of the session it belongs to does not come through here
 * (writeTokens).
 */
export function storeTokens(res: AuthResponse) {
  sessionEpoch++;
  signedOut = false;
  refreshPromise = null;
  refreshBackoff = null;
  writeTokens(res);
}

export function clearTokens() {
  // Whose token this tab holds, taken before it is dropped: it is what tells the
  // record that goes, below, from the one that stays.
  const mine = accessTokenUserId;
  sessionEpoch++;
  signedOut = true;
  // Nothing refreshes until a session begins (signedOut), and storeTokens
  // drops these too; they are dropped here as well so that a session that
  // ended never has a refresh to join or a back-off to wait out, whatever
  // else changes.
  refreshPromise = null;
  refreshBackoff = null;
  accessTokenInMemory = null;
  accessTokenExpiresAt = 0;
  accessTokenUserId = null;
  // The session ends, and its stored user goes with it: a reload would find a
  // user to resume and a refresh to send, and a session that is over has
  // neither. But the record is the browser's, not this tab's — every tab reads
  // it, and a sign-in or a rotation of the cookie writes it — and when it names
  // someone other than the user whose token this tab holds, it is another tab's
  // live session that it names: that tab's user signed in on the cookie after
  // this tab's own was orphaned. Removing it would send a reload of THAT tab to
  // the login page, with no refresh sent, though its cookie is good, and only
  // that tab's next refresh (writeTokens) would put it back. Leaving it costs
  // nothing: a record that outlives its session is removed by the next resume
  // the server refuses.
  // A tab that holds no token has no session of its own to tell the record from:
  // it is the boot's (nothing resumed, or a resume that failed) or stale, and
  // goes as it always did. So does one that names no one — a corrupt value is
  // no session's.
  if (mine === null || !storedUserIsAnotherUsers(mine)) {
    localStorage.removeItem(USER_KEY);
  }
  purgeLegacyTokenKeys();
}

/** Whether the stored user is a user, and not the one whose token `mine` is. */
function storedUserIsAnotherUsers(mine: string): boolean {
  const id: unknown = getStoredUser()?.id;
  return typeof id === "string" && id !== mine;
}

export function getStoredUser() {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as AuthResponse["user"];
  } catch {
    return null;
  }
}

/**
 * The server answered /auth/refresh with 401 or 403: the refresh token is
 * stale, revoked or not there, or the user is gone, disabled or changed role.
 * The server answers 401 for all of them and never 403 itself; a proxy or a
 * WAF in front of it might, and its 403 is taken for a refusal too — the one
 * choice here that fails closed. The session is over, and refreshTokens ended
 * it before throwing this. Private: the callers of a refresh turn it into
 * "no session" (see tokenFromRefresh and request).
 */
class RefreshRefusedError extends Error {
  constructor() {
    super("The server refused the session's refresh token");
    this.name = "RefreshRefusedError";
  }
}

/**
 * A courtesy refresh (see tokenFromRefresh) that was not made, and nothing was
 * sent: it found the lock held by another tab, or it found a refresh that is
 * needed already in flight and was left to it (refreshAhead). Private. It is how
 * the attempt, which every caller in the tab shares, tells the ones that joined
 * it: a courtesy caller carries on with the token it holds, and one that needs a
 * refresh makes its own (refreshNeeded).
 */
class RefreshSkippedError extends Error {
  constructor() {
    super("The refresh was skipped: another tab holds the lock");
    this.name = "RefreshSkippedError";
  }
}

/**
 * A refresh that was answered, and not with a session or a refusal: rate
 * limited, a server or proxy failing, an answer that is no session. It says
 * nothing about the session (see refreshTokens), and its message says what a
 * request that waited on it is shown: the session could not be renewed, and
 * what the server answered.
 *
 * Deliberately not an ApiClientError. That is the error of the request a
 * caller made, and callers act on its status — a 404 is "already gone", a 409
 * "someone else changed it", a 500 or 502 from Ceph "not available" — which
 * the status of a refresh they never made must not trigger. This is a plain
 * Error, so no `instanceof ApiClientError` branch matches it; the status stays
 * on it for diagnosis. retryUnlessClientError retries it once, as it does any
 * error that is not an ApiClientError, and describeError (lib/api-error.ts)
 * shows its message.
 */
export class RefreshFailedError extends Error {
  readonly status: number;
  /** What the answer asked the client to wait, in ms (Retry-After); 0 when it asked nothing. */
  readonly retryAfterMs: number;

  constructor(status: number, reason: string, waitMs = 0) {
    super(`The session could not be renewed (${reason})`);
    this.name = "RefreshFailedError";
    this.status = status;
    this.retryAfterMs = waitMs;
  }
}

// What a refresh answered 200 with something that is no session fails with.
const NOT_A_SESSION = "the server's answer was not a session";

// How long a refresh that failed without the server refusing the session
// (refreshTokens) holds the next one off: 5 s and up to 1 s more, or what the
// server asked for in a Retry-After, but never less than the one or more than
// the other bound.
//
// 5 s. The server allows an address 30 refreshes a minute (internal/api/
// middleware.go), and without TRUSTED_PROXIES everyone behind a proxy is one
// address. A tab that tried again from every request that needed a token could
// spend that allowance alone, and be what makes everyone else's refresh fail;
// trying every 5 s costs it at most 12 of the 30 — 24 if every one of them is
// answered "superseded" and so sent twice (postRefresh), which the server
// answers only to a refresh that lost a race to another's of the last few
// seconds, not to one in a loop. It is short enough that a server that
// restarted is used again within seconds of being back, and flat rather than
// growing, so that however long an outage lasts, the wait outlives it by at
// most that. A Retry-After shorter than this (0 included) does not shorten it:
// the floor is what stops a server that says "now" from being asked in a loop.
//
// 0–1 s of jitter on that floor. Every tab and every user behind one address
// meets the same failure at the same moment, and would all try again at the
// same moment, and be the next burst; up to a second spreads them. A
// Retry-After is the server's own figure and is followed as given.
//
// 60 s, the cap on a Retry-After. The server's own limiter has a one-minute
// window, so none it sends for this path is longer; a longer one comes from
// something in front of it, and must not hold requests off for more than a
// minute: one more attempt is a cheap thing to be wrong about.
const REFRESH_BACKOFF_MS = 5_000;
const REFRESH_BACKOFF_JITTER_MS = 1_000;
const REFRESH_BACKOFF_MAX_MS = 60_000;

// How far past its expiry, by this browser's clock, a token is still sent when
// the refresh that was to replace it cannot be made (tokenFromRefresh).
//
// The expiry is the server's, and the clock that judges it is this browser's.
// One that runs ahead makes a token that is good look expired by as much as it
// runs ahead, so a token has to be sent while it merely looks expired, or a
// clock that is wrong fails every request for as long as the refresh does. The
// clocks that are wrong by a minute or a few are the ones of unsynchronised
// lab VMs and laptops, and are the ones this allowance is for; 5 minutes is a
// third of the 15-minute access token (ACCESS_TOKEN_TTL), so a token that has
// outlived its own expiry by more than that is expired on any clock that is
// anywhere near right. Past it the request fails at once with the refresh's
// own failure, instead of being sent to meet a 401 each time.
const EXPIRED_TOKEN_SKEW_S = 300;

// The lock the tabs of one browser take turns to refresh under
// (withRefreshLock), and how long a tab waits for it.
//
// 15 s. A refresh normally takes well under a second, so the wait only has to
// outlast a slow one: a tab holding the lock through a slow link or a loaded
// server is worth waiting for. A refresh that hangs (a server that accepted
// the connection and went quiet; fetch has no timeout of its own) is not,
// and must cost the other tabs one wait and not their session. The wait is
// for a refresh that is NEEDED — no token held, or the one held is past its
// expiry. A refresh made ahead of a token that is still good is not waited
// for at all (ifAvailable, below).
const REFRESH_LOCK = "nexara:auth-refresh";
const REFRESH_LOCK_WAIT_MS = 15_000;

// How long a refresh answered "refresh_superseded" waits before it is asked
// once more (postRefresh): 250 ms and up to 250 ms more.
//
// The answer means another tab's refresh won the race for the cookie, and the
// winner's Set-Cookie is on its way to the jar the retry reads. 250 ms is long
// enough for a response that was in flight at the same moment to land, and a
// retry made any sooner would carry the same spent cookie and lose again;
// 500 ms at the most is short enough that the request waiting on all this is
// held for half a second, not for the 5 s of a back-off. The spread is so that
// tabs that lost together — restored at browser start, say — do not ask again
// together and lose to one another a second time.
const REFRESH_SUPERSEDED_RETRY_MS = 250;
const REFRESH_SUPERSEDED_RETRY_SPREAD_MS = 250;

// How the boot resume asks again when it could not look
// (resumeSessionPatiently): 4 attempts in all, with 1 s, 2 s and 4 s between
// them, up to 1 s of jitter added to each, or a Retry-After if it is longer, and
// no wait more than 8 s.
//
// What it is up against is brief — a superseded race is over in a second or
// two; a server that is restarting is back in seconds, or not for a long time —
// and someone is watching a spinner with nothing to say what it is waiting for.
// Before this the first failure ended the session; a few seconds of patience
// trade a spinner that lasts a little longer for a session that is not lost.
// 1 s, 2 s and 4 s keep the first retry quick, for the race, and give a
// restarting server room. They total 7 s and at most 3 s of jitter, so about 10
// s, and 24 s at the very worst, if each wait is a Retry-After at its cap. The
// jitter is so that tabs that lost together do not ask again together.
//
// Beyond the last attempt the session ends, as it did at the first failure
// before this: initialize() ends it (clearTokens, which takes nexara_user with
// it), the page goes to the login page, and the person signs in again. A reload
// does not resume it, whatever the cookie would still have said, because the
// user it would resume has gone from localStorage. Keeping the stored user for a
// reload to try again would be a change of its own.
//
// Those are the WAITS. Each attempt is also a refresh with a duration of its
// own, and under the cross-tab lock it can wait for the lock — up to
// REFRESH_LOCK_WAIT_MS, 15 s, behind another tab whose refresh hangs — so a
// resume that meets one at every attempt can stand for a minute longer than
// the figures above. That is a hung server, not a restarting one, and it is
// what the single attempt that came before it did too.
//
// 8 s, the cap on a Retry-After here, where the refresh back-off caps at 60 s
// (REFRESH_BACKOFF_MAX_MS): a 429's Retry-After is up to a minute, the window
// of the limiter, and a spinner that stands that long is worse than a session
// that ends and is signed in again. Past the cap the attempt is made anyway. The
// price is that a tab starved of the 30 refreshes a minute by 16 or more tabs
// resuming at once can still run out of attempts inside that window, and ends
// its session as it did before.
const RESUME_ATTEMPTS = 4;
const RESUME_RETRY_MS = 1_000;
const RESUME_RETRY_JITTER_MS = 1_000;
const RESUME_RETRY_MAX_MS = 8_000;

/**
 * How long a response asked the client to wait, in milliseconds, in either form
 * a Retry-After takes (RFC 9110 10.2.3: seconds, or an HTTP-date); 0 when it
 * sent none, or one that cannot be read. A date that has already passed comes
 * out negative: no wait, like a 0, and below the floor like any short one.
 */
function retryAfterMs(res: Response): number {
  const raw = res.headers.get("Retry-After")?.trim() ?? "";
  // Digits first: Date.parse reads "42" as a year.
  if (/^\d+$/.test(raw)) return Number(raw) * 1000;
  const at = Date.parse(raw);
  return Number.isNaN(at) ? 0 : at - Date.now();
}

/**
 * Whether a parsed refresh answer is a session as far as the SPA uses one: a
 * user with an id, an access token that is not empty, an expiry that is a
 * finite number and a list of permissions — or none sent, or null, which is
 * taken as an empty list (readSession). The server sends all four
 * (handlers.authResponse), and loadPerms answers [] and never null. Nothing it
 * sends for a refresh lacks one of the first three, so an answer that does is
 * not the server's: a proxy's JSON, an object with a user and nothing else.
 * Stored as a session it would leave the SPA holding `undefined` as its token.
 *
 * Permissions are the one that may be missing, and what is missing becomes
 * nothing: should a nil slice ever reach the wire as null, a refresh refused
 * for it would fail for everyone, for good, with nobody signed out and every
 * request saying the session could not be renewed — a soft outage nothing
 * clears. An empty list is the cautious reading, and fails closed: the pages
 * offer nothing until the next answer says otherwise. Anything else that is
 * not a list is still refused: it is not what the server sends, and its pages
 * call .includes on it.
 *
 * Every read is null-safe and none needs a type check of its own to be safe:
 * `null` is the one value JSON can hold that has no properties to read. Nor
 * does the expiry need to be told to be a number: Number.isFinite answers false
 * for anything that is not one, without turning it into one.
 */
function isAuthResponse(value: unknown): value is Omit<
  AuthResponse,
  "permissions"
> & {
  permissions?: string[] | null;
} {
  const answer = value as Record<string, unknown> | null;
  const userId = (answer?.["user"] as { id?: unknown } | null | undefined)?.id;
  const token = answer?.["access_token"];
  const expiresAt = answer?.["expires_at"];
  const permissions = answer?.["permissions"];
  return (
    typeof userId === "string" &&
    typeof token === "string" &&
    token !== "" &&
    Number.isFinite(expiresAt) &&
    (permissions == null || Array.isArray(permissions))
  );
}

/** The session a successful refresh answered with; null when it is none. */
async function readSession(res: Response): Promise<AuthResponse | null> {
  let answer: unknown;
  try {
    answer = await res.json();
  } catch {
    return null; // not JSON at all: a proxy's page
  }
  if (!isAuthResponse(answer)) return null;
  return { ...answer, permissions: answer.permissions ?? [] };
}

/**
 * The failure of a refresh answered with an error status other than a refusal:
 * the status, and the server's own words for it when it sent any (the status
 * text when it did not, and neither over HTTP/2 for a proxy's bare 502), and
 * what it asked the client to wait, which whoever asks again honours.
 */
async function refreshFailureFrom(res: Response): Promise<RefreshFailedError> {
  const said = (await errorFromResponse(res)).message;
  return new RefreshFailedError(
    res.status,
    `HTTP ${String(res.status)}${said !== "" ? `: ${said}` : ""}`,
    retryAfterMs(res),
  );
}

/**
 * Whether a refresh was answered 409 "refresh_superseded": the cookie it
 * carried was the immediate predecessor of one rotated moments ago — by
 * another tab of this browser, which won the race — and the server left it in
 * place, because the jar holds, or is about to hold, the winner's newer one.
 * Nothing is wrong with the session. Any other 409 is an ordinary failure.
 *
 * The body is read from a clone: when the answer is not this one, whoever has
 * it still reads it whole.
 */
async function isSuperseded(res: Response): Promise<boolean> {
  if (res.status !== 409) return false;
  try {
    const body: unknown = await res.clone().json();
    return (body as { error?: unknown } | null)?.error === "refresh_superseded";
  } catch {
    return false;
  }
}

/**
 * POST /auth/refresh: the one place the SPA sends it, for refreshTokens and for
 * the boot resume (resumeSession) alike. Body is empty — the HttpOnly cookie
 * carries the refresh token, and since v1.9.x that is the only delivery path
 * the server offers. auth: false, because this IS the refresh: resolving an
 * access token for it would start another one.
 *
 * An answer of "refresh_superseded" (isSuperseded) is asked again, once, after
 * a short jittered wait (REFRESH_SUPERSEDED_RETRY_MS) so that the winner's
 * cookie has reached the jar. The second answer is returned as it is, whatever
 * it is: superseded again, it is a 409 like any other and fails the refresh
 * without ending the session; refused, it ends it; good, it is the session.
 * Whoever calls this is already holding the refresh lock, where there is one
 * (withRefreshLock), so no other tab asks in between.
 *
 * `epoch` is the session this is for: if it ended during the wait, nothing is
 * sent.
 */
async function postRefresh(epoch: number): Promise<Response> {
  const send = () =>
    apiFetch(
      apiPath`/api/v1/auth/refresh`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: "{}",
      },
      { auth: false },
    );
  const first = await send();
  if (!(await isSuperseded(first))) return first;
  await new Promise<void>((resolve) => {
    setTimeout(
      resolve,
      REFRESH_SUPERSEDED_RETRY_MS +
        Math.random() * REFRESH_SUPERSEDED_RETRY_SPREAD_MS,
    );
  });
  if (epoch !== sessionEpoch) throw new StaleSessionError();
  return send();
}

/**
 * Runs `work`, a refresh of the session `epoch`, holding the lock that every
 * tab of this browser refreshes under, so that two tabs do not send the same
 * refresh cookie at the same instant. A refresh token is single-use — the
 * server rotates it on every refresh — and the tabs share the cookie: two
 * refreshing together send the same one, and one of them is told it is spent,
 * and that answer clears the cookie for every tab (a laptop that wakes with
 * several tabs open, a browser that restores several at start). Taking turns,
 * the second sends the cookie the first was given.
 *
 * AN OPTIMISATION, AND ON HTTPS ONLY. navigator.locks exists only in a secure
 * context, and Nexara also runs on plain HTTP (SECURE_COOKIES=auto or never;
 * lib/clipboard.ts branches on isSecureContext for the same reason), where
 * there is none and the tabs race, as they always did. What covers that case is
 * postRefresh, which asks again when the server says another tab won. Without
 * the API `work` just runs.
 *
 * The wait is bounded (REFRESH_LOCK_WAIT_MS): when it runs out, or the browser
 * will not hand the lock over, `work` runs without it, as it would without the
 * API. And once the lock is held — or the wait is over — the session may have
 * ended while this waited: then nothing is sent, and it is a StaleSessionError.
 *
 * A `courtesy` refresh does not wait at all. It is the one made ahead of a
 * token that is still good (tokenFromRefresh), and a tab whose refresh hangs
 * while it holds the lock would otherwise stall every request of every other
 * tab for the whole wait — Sign out included — though the token they hold is
 * fine. It asks for the lock with ifAvailable instead: free, it refreshes as
 * any refresh does; held by another tab, it is skipped (RefreshSkippedError),
 * nothing is sent, and the token held is used, which refreshes on a later
 * request. ifAvailable, and not a short wait, because the other tab's refresh
 * may be healthy and quick or hung and there is no telling which from here: a
 * short wait is a stall for the second case and a wasted wait for the first,
 * and the skip costs neither, the token being good.
 */
async function withRefreshLock<T>(
  epoch: number,
  work: () => Promise<T>,
  courtesy = false,
): Promise<T> {
  const locks = (navigator as { locks?: LockManager }).locks;

  const ready = (): Promise<T> => {
    if (epoch !== sessionEpoch) throw new StaleSessionError();
    return work();
  };

  const lock: { granted: boolean; skipped: boolean; result?: T } = {
    granted: false,
    skipped: false,
  };
  // A courtesy refresh waits for nothing, and so has no wait to give up.
  const waiting = new AbortController();
  const gaveUp = courtesy
    ? undefined
    : setTimeout(() => {
        waiting.abort();
      }, REFRESH_LOCK_WAIT_MS);
  try {
    // With no lock manager there is nothing to wait for, and nothing happens
    // here: the work runs below, as it does when the lock is never got.
    await locks?.request(
      REFRESH_LOCK,
      courtesy ? { ifAvailable: true } : { signal: waiting.signal },
      async (held) => {
        // ifAvailable hands over null when the lock is taken.
        if (held === null) {
          lock.skipped = true;
          return;
        }
        // Granted: nothing is waiting any more, and the timer that would have
        // given the wait up has nothing left to do.
        lock.granted = true;
        clearTimeout(gaveUp);
        lock.result = await ready();
      },
    );
  } catch (err) {
    // It ran, and failed: that is the refresh's own failure, as it came.
    if (lock.granted) throw err;
    // It never got the lock — the wait ran out, or the browser would not give
    // it — and carries on without, below.
  } finally {
    clearTimeout(gaveUp);
  }
  if (lock.skipped) throw new RefreshSkippedError();
  return lock.granted ? (lock.result as T) : ready();
}

/**
 * The boot resume's refresh, for auth-store's initialize(): POST /auth/refresh
 * for the session `epoch` under the same lock as every other refresh, and with
 * the same second ask of a "refresh_superseded" answer (postRefresh) — several
 * tabs restored at browser start resume on one cookie at one instant. It
 * resolves with the session that answered. It rejects a refusal (401, 403) with
 * an ApiClientError of its status; any other answer that is not OK, and one
 * that is no session, with a RefreshFailedError, as a refresh does; and a
 * network failure as it came. It stores nothing and handles no failure, and
 * asks once: resumeSessionPatiently asks again, and initialize() decides what
 * becomes of a failure that is final.
 */
export function resumeSession(epoch: number): Promise<AuthResponse> {
  return withRefreshLock(epoch, async () => {
    const res = await postRefresh(epoch);
    // A refusal is an ApiClientError of its status, as any answer that is
    // refused is: the one thing that tells the resume the session is gone.
    if (res.status === 401 || res.status === 403) {
      throw await errorFromResponse(res);
    }
    // Any other answer that is not OK says nothing about the session, as for any
    // refresh (refreshTokens): the resume could not look.
    if (!res.ok) throw await refreshFailureFrom(res);
    const session = await readSession(res);
    if (session === null)
      throw new RefreshFailedError(res.status, NOT_A_SESSION);
    return session;
  });
}

/**
 * The boot resume that does not give up on the first failure: resumeSession,
 * asked again up to RESUME_ATTEMPTS times in all, with a wait between them
 * (resumeRetryWait), while the page waits on its spinner. It stops, and
 * rejects, when
 *
 *  - the server REFUSED the cookie (401, 403): the session is gone, and a later
 *    attempt will not say otherwise;
 *  - the session changed hands meanwhile (a StaleSessionError): someone signed
 *    in while this waited, and the resume is no longer the page's to finish.
 *    That is judged before every attempt, and after each one that failed, before
 *    the wait: it is the epoch the attempt names that is looked at, and nothing
 *    is sent, slept on or queued for the lock on behalf of a session that has
 *    ended;
 *  - the attempts have run out: the last failure is the answer.
 *
 * Every other failure is "could not look": a refresh that lost its race for the
 * cookie twice (409 refresh_superseded), 429, a 5xx, no network, an answer that
 * is no session. It says nothing about the session, and it is what ended the
 * session at boot before, taking nexara_user with it — which every tab of the
 * browser shares, so the next reload of every other tab came up at the login
 * page. With tabs restored together on plain HTTP, where there is no lock to
 * take turns on (withRefreshLock), the resumes race on the one single-use
 * cookie and the losers meet exactly these.
 *
 * Each attempt is a resumeSession, so it takes the same lock and has the same
 * second ask of a superseded refresh.
 */
export async function resumeSessionPatiently(
  epoch: number,
): Promise<AuthResponse> {
  for (let attempt = 1; ; attempt++) {
    // Someone signed in while the last wait ran. The attempt would find that
    // out only once it had the lock — REFRESH_LOCK_WAIT_MS away behind another
    // tab whose refresh hangs — and the page, whose spinner this is, would wait
    // that long for an answer nobody wants.
    if (epoch !== sessionEpoch) throw new StaleSessionError();
    try {
      return await resumeSession(epoch);
    } catch (err) {
      // An ApiClientError is what resumeSession rejects a refusal with, and
      // nothing else (any other answer that is not OK is a RefreshFailedError).
      if (err instanceof ApiClientError) throw err;
      // The session changed hands while the attempt was out, which is also what
      // a StaleSessionError from the attempt itself says (the epoch only moves
      // on): the next attempt would only notice, and the wait before it would
      // be for nothing.
      if (epoch !== sessionEpoch) throw new StaleSessionError();
      if (attempt >= RESUME_ATTEMPTS) throw err;
      await new Promise<void>((resolve) => {
        setTimeout(resolve, resumeRetryWait(attempt, err));
      });
    }
  }
}

/**
 * How long the boot resume waits after its `attempt`-th failure: 1 s after the
 * first, 2 s after the second, 4 s after the third, each with up to 1 s of
 * jitter on top, or what the failure asked for in a Retry-After if that is
 * longer — and never more than RESUME_RETRY_MAX_MS.
 */
function resumeRetryWait(attempt: number, failure: unknown): number {
  const asked =
    failure instanceof RefreshFailedError ? failure.retryAfterMs : 0;
  const own =
    RESUME_RETRY_MS * 2 ** (attempt - 1) +
    Math.random() * RESUME_RETRY_JITTER_MS;
  return Math.min(Math.max(asked, own), RESUME_RETRY_MAX_MS);
}

async function refreshTokens(courtesy: boolean): Promise<AuthResponse> {
  // The session this refresh is for. The answer is applied only if that is
  // still the current one when it arrives (see sessionEpoch): a refresh that
  // belongs to a session that ended — by Sign out, by expiry, by another
  // sign-in — is dropped before anything of it is stored or reported, whether
  // the server answered it, refused it, or could not be reached.
  const epoch = sessionEpoch;

  // A refresh that failed lately, and has not been waited out: nothing is sent,
  // and whatever waits on this one fails at once with the failure that began
  // the wait — without waiting for the lock first. The refresh the wait is over
  // for is the first of them to come.
  if (refreshBackoff !== null) {
    if (performance.now() < refreshBackoff.until) throw refreshBackoff.failure;
    refreshBackoff = null;
  }

  // A refresh fails in two ways, and only one of them is about the session.
  //
  // The server REFUSES it (401, 403, below): the session is over, and that is
  // the one answer that ends it.
  //
  // Every other failure — rate limited (429), the server or a proxy in front
  // of it failing (5xx, or any other status), no network, an answer that is no
  // session — says nothing about the session. The refresh cookie may be as
  // good as it was, and a server that is restarting or has just said "too
  // many" is in no state to be asked again at once. So the session, its tokens
  // and its epoch are left as they are, the request that waited on the refresh
  // fails with the refresh's own failure (a RefreshFailedError, which is not an
  // ApiClientError, or the network's own error) rather than with an expired
  // session, and the next refresh waits (refreshBackoff). Ending the session
  // here instead signed users out for a restart or a proxy hiccup, and since
  // the end of a session also wipes their console tabs and dismissed issues
  // (stores/session-reset.ts), it cost them those as well.
  //
  // A failure of a session that has ended since is neither: it is that one's,
  // and neither fails the session now current nor makes it wait.
  const failed = (failure: unknown): unknown => {
    if (epoch !== sessionEpoch) return new StaleSessionError();
    const asked =
      failure instanceof RefreshFailedError ? failure.retryAfterMs : 0;
    const floor =
      REFRESH_BACKOFF_MS + Math.random() * REFRESH_BACKOFF_JITTER_MS;
    const wait = Math.min(Math.max(asked, floor), REFRESH_BACKOFF_MAX_MS);
    // performance.now(), not the date: a clock that is set back meanwhile
    // cannot stretch the wait. Read here, from the moment of the failure and
    // not of the refresh's start: one that took 20 s to fail holds the next
    // off for the 5 s that follow.
    refreshBackoff = { until: performance.now() + wait, failure };
    return failure;
  };

  return withRefreshLock(
    epoch,
    async () => {
      let res: Response;
      try {
        res = await postRefresh(epoch);
      } catch (err) {
        throw failed(err);
      }
      if (epoch !== sessionEpoch) throw new StaleSessionError();

      if (res.status === 401 || res.status === 403) {
        clearTokens();
        onAuthFailure?.();
        throw new RefreshRefusedError();
      }
      if (!res.ok) {
        // Its body is awaited, and the session may end meanwhile: failed() judges
        // that after the read, which is why the error is built in its argument.
        throw failed(await refreshFailureFrom(res));
      }

      // An answer that is no session — a proxy's page, an object with a user and
      // nothing else — is the refresh that failed that it is, and not half a
      // session stored.
      const data = await readSession(res);
      if (data === null) {
        throw failed(new RefreshFailedError(res.status, NOT_A_SESSION));
      }
      if (epoch !== sessionEpoch) throw new StaleSessionError();

      // A rotation keeps its user, and so its session: no new epoch. An answer
      // that names someone else — another tab signed them in on the shared
      // refresh cookie — is another session, and begins one as a sign-in does:
      // what was started for the user this tab held is not carried on as them.
      if (accessTokenUserId !== null && accessTokenUserId !== data.user.id) {
        storeTokens(data);
      } else {
        writeTokens(data);
      }
      onAuthRefresh?.(data);
      return data;
    },
    courtesy,
  );
}

/**
 * Begins a refresh and makes it the one in flight, which the callers in the tab
 * that want one share (refreshAhead, refreshNeeded) instead of each sending its
 * own. Only the attempt that starts it decides whether it is a courtesy (see
 * withRefreshLock); one that joins it takes it as it is.
 */
function startRefresh(courtesy: boolean): Promise<AuthResponse> {
  const attempt: Promise<AuthResponse> = refreshTokens(courtesy).finally(() => {
    // Only its own: clearTokens() and storeTokens() have made room for a newer
    // session's refresh, and a refresh that is needed has taken the place of a
    // courtesy one (refreshNeeded), which this one settling must not forget.
    if (refreshPromise === attempt) refreshPromise = null;
  });
  refreshPromise = attempt;
  refreshIsCourtesy = courtesy;
  return attempt;
}

/**
 * The refresh for a caller that is making one ahead of a token that is still
 * good (tokenFromRefresh): it shares a courtesy refresh in flight, and begins
 * one when there is none.
 *
 * It does not join one that is NEEDED. That one waits for the lock, up to
 * REFRESH_LOCK_WAIT_MS behind another tab whose refresh hangs, and joining it
 * would make this caller wait as long to be told what it has no need to know:
 * its own token is good. It is told it was skipped (RefreshSkippedError), as it
 * would be if the lock were taken, and sends the token it holds. That is the
 * case of a request that was refused for its token (refreshNeeded), waiting
 * for a refresh, while another is made whose token has not expired yet.
 */
function refreshAhead(): Promise<AuthResponse> {
  if (refreshPromise === null) return startRefresh(true);
  return refreshIsCourtesy
    ? refreshPromise
    : Promise.reject(new RefreshSkippedError());
}

/**
 * A refresh for a caller that cannot do without one: no token held, one past
 * its expiry, or a request refused. It shares whatever is in flight, and begins
 * one when there is none; and when what it joined was a courtesy refresh that
 * found the lock held by another tab and was skipped (RefreshSkippedError), it
 * makes its own, which does wait for the lock as a needed refresh does. By the
 * time it hears of the skip the attempt has been cleared, so the one it starts
 * is not a courtesy and cannot be skipped.
 *
 * `epoch` is the caller's session. A skip can be a long time coming, and the
 * session may have changed hands in it: the refresh a new session needs is not
 * this caller's to make, and none is sent for it (StaleSessionError).
 *
 * The second try takes a needed refresh in flight, and nothing else: never a
 * courtesy one, which is the kind that gets skipped, and a caller that needs a
 * refresh must not be sent away with a skip twice. That can happen, because the
 * attempt cleared is not kept clear: a courtesy refresh begun by another
 * request in the few microtasks between the skip and this caller hearing of it
 * is in flight by then. This caller begins its own, which takes its place as the
 * one in flight (startRefresh), and the courtesy refresh runs on for its own
 * callers. A try that cannot be skipped is a try that needs no second one, so
 * there is no loop here, and nothing to bound.
 */
async function refreshNeeded(epoch: number): Promise<AuthResponse> {
  try {
    return await (refreshPromise ?? startRefresh(false));
  } catch (err) {
    if (!(err instanceof RefreshSkippedError)) throw err;
    if (epoch !== sessionEpoch) throw new StaleSessionError();
    return refreshPromise !== null && !refreshIsCourtesy
      ? refreshPromise
      : startRefresh(false);
  }
}

/**
 * The access token a request is sent with: the one held, refreshed first when
 * it is about to expire; null when nobody is signed in. Rejects with a
 * refresh's own failure (see refreshTokens) only where there is no token held
 * to send instead, or the one held is long past its expiry (tokenFromRefresh).
 */
async function ensureValidToken(): Promise<string | null> {
  // First call after page load, before initialize() has said whether there is a
  // session to resume — try a cookie-based refresh to populate memory. Not once
  // a session has ended here, nor on a page that started signed out: see
  // signedOut. With nothing held, a refresh that cannot be made leaves nothing
  // to send: the request fails with it, rather than go out as nobody — to meet
  // a 401 and start a refresh of its own.
  if (!accessTokenInMemory) {
    return signedOut ? null : tokenFromRefresh(false);
  }

  // Proactively refresh if the token expires within 60 seconds. Ahead of its
  // expiry that is a courtesy to the request, and is made as one; once the
  // expiry has passed, by this clock, it is needed.
  const now = Math.floor(Date.now() / 1000);
  if (accessTokenExpiresAt > 0 && accessTokenExpiresAt - now < 60) {
    return tokenFromRefresh(accessTokenExpiresAt > now);
  }

  return accessTokenInMemory;
}

/**
 * The access token a request is sent with, and the session it is sent for:
 * ensureValidToken, and the epoch the token was chosen under, which the caller
 * judges.
 *
 * What ensureValidToken returns it has read from the session of the moment it
 * returned it, and its caller reads the session again some microtasks later.
 * Between the two a session can change hands — a sign-out, a sign-in. The token
 * is then the previous one's, and what the caller makes of it from there is
 * made under the NEXT one's session: a request took its epoch after the token,
 * so it was sent as the old user and its 401 was then taken for the new user's,
 * and replayed as them. So the epoch is taken FIRST and handed back with the
 * token, and the CALLER checks it against sessionEpoch, in its own continuation,
 * in the same turn as it sends (request, apiFetch, openApiRequest): a change at
 * any point since — before this looked, or in the microtasks before the caller
 * resumed — is seen there, and nothing can intervene between that check and the
 * send. A token that comes back across a change is refused by it: the request is
 * the ended session's, and is not sent. Reading sessionEpoch again once the
 * token has arrived, as a caller with no epoch to hold it to would, takes the
 * new session's and calls the old one's token its own.
 *
 * No token is the one answer the epoch cannot judge. A page whose cookie the
 * server refused has had its session ended by that very refresh (so the epoch
 * has moved, legitimately), and goes on with no token to meet a 401 and say the
 * session expired; nothing is being carried across, and the epoch handed back is
 * the current one — which a sign-in that landed before this looked has moved
 * just the same. What tells them apart is that "nobody is signed in" is only
 * still the answer if nobody has signed in since, and a sign-in leaves the
 * module signed in (storeTokens unlatches signedOut): so that is refused here. A
 * request sent as nobody under the next user's epoch meets a 401, finds that
 * session current, and is replayed as them.
 *
 * Every window above is reachable in a test, and is tested: ensureValidToken
 * makes one Date.now() call right before it returns the held token, and a spy on
 * it can queue the session change, directly or a few microtasks later, to land
 * before this looks, in the turn before its caller resumes, or after the request
 * is out (api-client.session.test.ts).
 */
async function tokenForRequest(): Promise<{
  token: string | null;
  epoch: number;
}> {
  const before = sessionEpoch;
  const token = await ensureValidToken();
  if (token === null) {
    if (!signedOut) throw new StaleSessionError();
    return { token, epoch: sessionEpoch };
  }
  return { token, epoch: before };
}

/**
 * The access token a refresh yields, for the request that waited on it; null
 * when there is no session to refresh. Two answers are not "no session": a
 * refresh that went stale, and one that came back for someone else — another
 * tab signed them in on the shared cookie, which began a session of theirs
 * (refreshTokens). Either way the request that waited belongs to the session
 * that ended, and is not sent for whoever is signed in by now. The token of an
 * answer that began another session is not judged here: tokenForRequest took
 * the epoch before it asked, and whoever sends with the token it hands back
 * refuses it if the session has changed since.
 *
 * Nor is a refresh that could not be made — rate limited, a server or proxy
 * failing, no network (refreshTokens): the session is as alive as it was, and
 * the failure is the waiting request's own, thrown as it came. Turning it into
 * "no token" would send the request out as nobody, to meet a 401 and start a
 * refresh of its own.
 *
 * Null is the server's refusal alone: that refresh has already ended the
 * session, so there is nobody to send for.
 *
 * ONE EXCEPTION to "the failure is the request's own", for a token that is
 * held (this is then the proactive refresh of a token about to expire, not a
 * page's first request): the refresh was a courtesy to the request, not a
 * condition of it. The token held is the session's own, the server takes it
 * until the second it expires, and the request may well succeed. Failing it
 * instead would turn every refresh problem into errors on requests that would
 * have gone through, for the last minute of each token — and, if this
 * browser's clock runs ahead of the server's, for as long as there is a
 * problem at all, since that clock is what judges the token about to expire.
 * So it is sent, unless it is past its expiry by more than the skew allowance
 * (EXPIRED_TOKEN_SKEW_S): then it is expired for real, the refresh is the only
 * way to a token, and the request fails with its failure at once instead of
 * being sent to meet a 401 each time. A token that has expired by less and is
 * refused is a 401, which request() turns into the refresh's own failure for as
 * long as the refresh is backing off.
 *
 * A `courtesy` refresh is the one made ahead of a token that is still good, by
 * this clock: the token is the answer, the refresh only a courtesy to the
 * request, and so it is not waited for — if another tab holds the lock the
 * refresh is skipped and the token held is used (withRefreshLock), and so it is
 * if a refresh that is needed is in flight, which is waited for in its turn
 * (refreshAhead). A refresh that is needed (no token, or one past its expiry)
 * waits for the lock, within its bound.
 */
async function tokenFromRefresh(courtesy: boolean): Promise<string | null> {
  const epoch = sessionEpoch;
  let refreshed: AuthResponse;
  try {
    refreshed = courtesy ? await refreshAhead() : await refreshNeeded(epoch);
  } catch (err) {
    if (err instanceof RefreshRefusedError) return null;
    // Not for a session that ended while the refresh was out, whatever it
    // failed with: the failure is that session's, and what is held now is the
    // next one's — this request is not theirs, and must not go out under their
    // token or be told of a failure that is not theirs. (A refusal is left
    // above: its own clearTokens() moved the epoch.)
    //
    // This is the one check for the page's first request, the held-token
    // fallback below and a failure that is only late. refreshTokens judged the
    // epoch when it decided the failure was this session's (failed()), so what
    // is left is the few microtasks before this catch runs — which a session
    // that changes hands in between, or a different-user answer whose
    // onAuthRefresh throws after storeTokens, does reach (see
    // api-client.refresh.test.ts).
    if (epoch !== sessionEpoch) throw new StaleSessionError();
    // What is held NOW, and not what was held when this began: the session is
    // the same, and a refresh that rotated its token and then failed — a
    // listener of onAuthRefresh that threw — has put a newer one in its place,
    // which is the one to send. The one held before is superseded.
    const held = accessTokenInMemory;
    // A courtesy refresh that was skipped — for the lock another tab holds, or
    // because one that is needed was in flight (refreshAhead): the token it was
    // made ahead of is still good, and is the answer — unless the skip was a
    // long time coming (a tab frozen while it was decided) and the token has
    // expired for real since, past the allowance: then the refresh is needed
    // after all, and is made. Only a courtesy refresh is skipped, and one that
    // is needed is never told of a skip (refreshNeeded), so this is never a
    // refresh with nothing held.
    if (err instanceof RefreshSkippedError) {
      return expiredBeyondSkew() ? tokenFromRefresh(false) : held;
    }
    if (held !== null && !expiredBeyondSkew()) return held;
    throw err;
  }
  return refreshed.access_token;
}

/**
 * Whether the token held is past its expiry, by this clock, by more than the
 * allowance. Asked only of a token that is held for a refresh before its
 * expiry, so that it has one.
 */
function expiredBeyondSkew(): boolean {
  return (
    Math.floor(Date.now() / 1000) - accessTokenExpiresAt > EXPIRED_TOKEN_SKEW_S
  );
}

class ApiClientError extends Error {
  constructor(
    public status: number,
    public body: ApiError,
  ) {
    super(body.message);
    this.name = "ApiClientError";
  }
}

/**
 * Whether a parsed body is an error envelope as far as ApiClientError needs
 * one: it has a message that is a string. The server's own is always {error,
 * message} (handlers.ErrorResponse), and says "" when it has nothing to say.
 *
 * One null-safe read answers for every body JSON can hold: `null` is the one
 * that has no properties to read, and a number, a string, an array or an
 * object without a message each answer undefined.
 */
function hasMessage(body: unknown): body is { message: string } {
  return typeof (body as { message?: unknown } | null)?.message === "string";
}

/**
 * The error for a response that is not OK: its status, and the {error,
 * message} body the server sent. A response with none gets its status text,
 * which over HTTP/2 is empty, and the message is then left to describeError
 * (lib/api-error.ts) to put as "HTTP 502". That is a proxy's 502, which has no
 * JSON body, and equally a body that is JSON but no envelope — `null`, a
 * string, an array, an object with no message — which is what such a proxy or
 * a WAF may send as well. It must still come out as an ApiClientError: this
 * is what both request() and a failed refresh (refreshTokens, which records
 * its back-off from what this returns) are built on, and neither can be left
 * with a TypeError from `null.message` instead.
 */
async function errorFromResponse(res: Response): Promise<ApiClientError> {
  let parsed: unknown;
  try {
    parsed = await res.json();
  } catch {
    // Not JSON at all — a proxy's page, or nothing — is answered below like any
    // other body that is no envelope.
  }
  const body = hasMessage(parsed)
    ? (parsed as ApiError)
    : { error: "unknown", message: res.statusText };
  return new ApiClientError(res.status, body);
}

/** What apiFetch hands fetch(): a RequestInit whose headers are a plain object. */
type ApiFetchInit = Omit<RequestInit, "headers"> & {
  headers?: Record<string, string>;
};

/**
 * fetch() for an API request apiClient does not make — a file read as a
 * Blob, a multipart upload, a public probe — in the same order as request():
 * the path is re-checked with assertApiPath before anything is sent, because
 * the ApiPath brand is a compile-time type only, and only then is an access
 * token resolved (which may refresh it — a request of its own) and added to
 * `init` as the Authorization header. A refresh that cannot be made, with no
 * token held to send instead, rejects with its own failure (see refreshTokens)
 * and nothing is sent; so does a session that changes hands while the token is
 * resolved, with a StaleSessionError (tokenForRequest). `auth: false` resolves
 * and sends no token, for the refresh itself and for public endpoints; `init`
 * then reaches fetch() as given.
 *
 * Every fetch the SPA makes goes through here or through request(): ESLint
 * refuses fetch anywhere but this file (eslint.config.js).
 */
export async function apiFetch(
  path: ApiPath,
  init: ApiFetchInit = {},
  { auth = true }: { auth?: boolean } = {},
): Promise<Response> {
  assertApiPath(path);
  if (!auth) {
    return fetch(path, init);
  }
  const { token, epoch } = await tokenForRequest();
  // The session the token was chosen for is still the current one, as of the
  // turn that sends (see tokenForRequest).
  if (epoch !== sessionEpoch) throw new StaleSessionError();
  return fetch(
    path,
    token
      ? {
          ...init,
          headers: { ...init.headers, Authorization: `Bearer ${token}` },
        }
      : init,
  );
}

/**
 * The request openApiRequest returns: an XMLHttpRequest, already opened and,
 * when there is a session, carrying its Authorization header, whose type
 * leaves out open().
 * Opening it again would aim that header at a path nothing checked: without
 * open() in the type that does not compile, and a cast that gets past the
 * type meets the request's own open(), which throws.
 */
export type OpenedApiRequest = Omit<XMLHttpRequest, "open">;

/**
 * A new XMLHttpRequest, opened on an API path — for the one request fetch
 * cannot make, an upload that reports its progress — in the same order as
 * apiFetch: the path is checked with assertApiPath, the request opened, and
 * only then an access token resolved (which may refresh it, a request of its
 * own) and set as the Authorization header. The caller adds the rest and
 * sends it. A refresh that cannot be made, with no token held to send
 * instead, rejects with its own failure (see refreshTokens), as it does for
 * apiFetch, and a session that changes hands while the token is resolved with
 * a StaleSessionError; the request has been opened by then, and is never
 * sent.
 *
 * ESLint refuses XMLHttpRequest anywhere but this file (eslint.config.js), so
 * this is the only way to get one. No exported function returns the stored
 * access token, and the three helpers that attach it — this one, request()
 * and apiFetch() — attach it only to a path assertApiPath has passed. That
 * is a promise about where the stored token goes, not about who can obtain
 * one: any module can call an auth endpoint through apiClient and read the
 * token in its answer, as the login and OIDC callback flows do before they
 * hand it to storeTokens().
 */
export async function openApiRequest(
  method: string,
  path: ApiPath,
): Promise<OpenedApiRequest> {
  assertApiPath(path);
  const xhr = new XMLHttpRequest();
  xhr.open(method, path);
  // An own property, neither writable nor configurable, in front of the
  // prototype's open(): the re-aim a cast would allow now throws, and the
  // property can be neither reassigned nor deleted. It stops every spelling
  // that goes through the request. The prototype's own open() —
  // Object.getPrototypeOf(xhr).open.call(xhr, …) — still gets past it, and
  // what makes that harmless is the platform: open() empties the request's
  // author headers (XMLHttpRequest Standard, open(): "Empty this's author
  // request headers"), so a re-aimed request carries no Authorization, and
  // the one credential the browser adds by itself, the refresh cookie, is
  // scoped to /api/v1/auth/ (RefreshCookiePath,
  // internal/api/handlers/auth_cookies.go).
  Object.defineProperty(xhr, "open", {
    value: () => {
      throw new PathSegmentError(
        path,
        "an opened API request cannot be re-opened",
      );
    },
  });
  const { token, epoch } = await tokenForRequest();
  // Still the session the token was chosen for, as of the turn that hands it
  // over (see tokenForRequest).
  if (epoch !== sessionEpoch) throw new StaleSessionError();
  if (token) {
    xhr.setRequestHeader("Authorization", `Bearer ${token}`);
  }
  return xhr;
}

/**
 * How request() authenticates. Private, and so is request(): what a caller can
 * choose is a method of apiClient, which are "refresh" and "none", or
 * signOutRequest, which is "held".
 *
 *  - "refresh", for every method of apiClient but the two below: the token
 *    held, refreshed first when it is about to expire, and a 401 refreshes and
 *    replays the request once.
 *  - "held": the token held, as it is — expired or not, which is for the server
 *    to say — and never a refresh: not started, not waited for, not after a
 *    401 either. For a request whose credential is the refresh cookie and not
 *    the token, which can only be sent if nothing is waited for first: Sign out
 *    (signOutRequest), and nothing else.
 *  - "none": no token at all, for the public endpoints.
 */
type RequestAuth = "refresh" | "held" | "none";

async function request<T>(
  method: string,
  path: ApiPath,
  body?: unknown,
  auth: RequestAuth = "refresh",
): Promise<T> {
  // Before the token refresh below, which is itself a request: a path the
  // brand let through by a cast must not cause even that to be sent.
  assertApiPath(path);
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  // The token the request goes out with, which a 401 below is about: it is
  // what tells a request refused for a token that is gone from one refused
  // for the token another request has replaced it with since. And the session
  // it is sent for, which that 401 is about as well: the one the token was
  // resolved for (tokenForRequest), and not one read again after it — a token
  // that came from one session must not be judged against the next.
  let sentToken: string | null = null;
  let epoch = sessionEpoch;
  if (auth === "refresh") {
    const resolved = await tokenForRequest();
    // Checked here, in the turn that sends, and not only where the token was
    // chosen: the session can change hands in the microtasks between the two,
    // and a request that went out then would carry the old session's token, or
    // none, into the new one's (see tokenForRequest).
    if (resolved.epoch !== sessionEpoch) throw new StaleSessionError();
    epoch = resolved.epoch;
    if (resolved.token) sentToken = resolved.token;
  } else if (auth === "held") {
    sentToken = accessTokenInMemory;
  }
  if (sentToken) headers["Authorization"] = `Bearer ${sentToken}`;

  const serializedBody = body != null ? JSON.stringify(body) : null;

  let res = await fetch(path, {
    method,
    headers,
    body: serializedBody,
    credentials: "same-origin",
  });

  // 401 retry with refresh (single attempt)
  if (res.status === 401 && auth === "refresh") {
    // Its session may have ended while it was in flight — signed out, expired,
    // replaced by another sign-in. A 401 for that session says nothing about the
    // one now current, and refreshing here would replay the request under that
    // one's token: the ended session's request would run as the new user.
    if (epoch !== sessionEpoch) throw new StaleSessionError();
    // Nobody is signed in here (see signedOut): this 401 is the answer, and a
    // refresh would resume a session off a cookie nothing has a claim on.
    if (signedOut) {
      throw new ApiClientError(401, {
        error: "unauthorized",
        message: "Session expired",
      });
    }
    let replayWith: string;
    if (accessTokenInMemory !== null && accessTokenInMemory !== sentToken) {
      // The token this was refused for is not the one held any more: another
      // request met the same 401 first, and its refresh has rotated it. What is
      // held is the same session's (the epoch is the one this was sent for) and
      // just issued, so the request is replayed with it and no second refresh is
      // spent — a request refused later than that would otherwise rotate it
      // again for nothing, and so would each one after it. Still one replay: if
      // that is refused too, it is the answer.
      replayWith = accessTokenInMemory;
    } else {
      let refreshResult: AuthResponse;
      try {
        refreshResult = await refreshNeeded(epoch);
      } catch (err) {
        // The server refused the session, which refreshTokens has ended: this is
        // the expired session the user is told of.
        if (err instanceof RefreshRefusedError) {
          throw new ApiClientError(401, {
            error: "unauthorized",
            message: "Session expired",
          });
        }
        // Anything else leaves the session as it is. A refresh that belonged to
        // a session that has since ended went stale, and ending the current one
        // here would sign the new user out; and a failure that merely arrives
        // after the session changed hands is that session's too, and is not
        // told to this request as its own — the check below. What is left is
        // the refresh's own failure, as it came: rate limited, a server or proxy
        // failing, no network, or backing off from one of those
        // (refreshTokens). The request is not sent again, and not as "Session
        // expired", which it is not known to be.
        if (epoch !== sessionEpoch) throw new StaleSessionError();
        throw err;
      }
      // It answered, perhaps for someone else (see tokenFromRefresh): replaying
      // the request now would run it as them.
      if (epoch !== sessionEpoch) throw new StaleSessionError();
      replayWith = refreshResult.access_token;
    }
    headers["Authorization"] = `Bearer ${replayWith}`;
    try {
      res = await fetch(path, {
        method,
        headers,
        body: serializedBody,
        credentials: "same-origin",
      });
    } catch (err) {
      // The retry never came back. Its session may have ended while it was out,
      // and then this is that session's failure, not the current one's. In a
      // session that is still current it is a network failure like any other,
      // and reaches the caller as one: it says nothing about whether the
      // session is alive, and the refresh that came just before it was good.
      if (epoch !== sessionEpoch) throw new StaleSessionError();
      throw err;
    }
  }

  if (!res.ok) {
    throw await errorFromResponse(res);
  }

  // Handle 204 No Content (e.g. DELETE responses)
  if (res.status === 204 || res.headers.get("content-length") === "0") {
    return undefined as T;
  }

  return (await res.json()) as T;
}

/**
 * The envelope every collection endpoint returns (Go: handlers.ListResponse).
 * `total` is the count matching the request's filters before limit/offset, so
 * it can exceed `items.length` on a paginated endpoint.
 */
export interface ListResponse<T> {
  items: T[];
  total: number;
}

/**
 * Unwraps a list envelope, throwing if the response is not one.
 *
 * Deliberately strict rather than falling back to `Array.isArray(body)`. A
 * tolerant unwrap would silently paper over an endpoint that never got
 * converted, which is precisely the failure this envelope exists to remove —
 * and it fails quietly, since iterating an object's values yields no error.
 * A thrown error names the path, so a missed endpoint surfaces as a broken
 * query with a usable message instead of an empty list.
 */
function unwrapList<T>(path: string, body: unknown): ListResponse<T> {
  if (
    typeof body === "object" &&
    body !== null &&
    Array.isArray((body as ListResponse<T>).items)
  ) {
    return body as ListResponse<T>;
  }
  throw new Error(
    `GET ${path}: expected a {items,total} list envelope, got ${
      Array.isArray(body) ? "a bare array" : typeof body
    }`,
  );
}

/**
 * Every method takes an {@link ApiPath}, which only the apiPath tag
 * (lib/api-path.ts) produces: a path whose interpolated values were each
 * checked to be one segment and encoded. A path built any other way — a
 * plain template literal, a concatenation — does not type-check.
 */
export const apiClient = {
  get: <T>(path: ApiPath) => request<T>("GET", path),
  /**
   * GET a collection, returning just the rows. The common case — reach for
   * `page` instead when the caller needs `total` for pagination.
   */
  list: async <T>(path: ApiPath): Promise<T[]> =>
    unwrapList<T>(path, await request<unknown>("GET", path)).items,
  /** GET a collection with its total, for paginated views. */
  page: async <T>(path: ApiPath): Promise<ListResponse<T>> =>
    unwrapList<T>(path, await request<unknown>("GET", path)),
  post: <T>(path: ApiPath, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: ApiPath, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: ApiPath, body?: unknown) => request<T>("PATCH", path, body),
  delete: <T>(path: ApiPath, body?: unknown) =>
    request<T>("DELETE", path, body),
  postPublic: <T>(path: ApiPath, body?: unknown) =>
    request<T>("POST", path, body, "none"),
  getPublic: <T>(path: ApiPath) => request<T>("GET", path, undefined, "none"),
};

/**
 * Sign out's request to the server: POST /auth/logout, sent with the token held,
 * as it is, and no refresh — none started, none waited for under the cross-tab
 * lock, none after a 401. /auth/logout is authOptional (internal/api/router.go):
 * the credential it revokes with is the refresh cookie, and the token only lets
 * the server check that the session is the caller's, which it does for a token
 * that is still valid and skips for one that is not. A token to resolve first —
 * refreshed, or waited for under the lock — is what kept the request from being
 * sent at all when the refresh was failing, and left the session and its cookie
 * alive on the server.
 *
 * A function of its own, with the path fixed and nothing to choose, and not an
 * option of apiClient: what it leaves out is safe only for a request that the
 * refresh cookie authenticates and the token merely corroborates, which is this
 * one and no other. Whether it should be sent at all — the cookie may not be
 * this tab's user's — is for its caller to judge (auth-store logout()).
 */
export async function signOutRequest(): Promise<void> {
  await request<unknown>("POST", apiPath`/api/v1/auth/logout`, {}, "held");
}

export { ApiClientError };
