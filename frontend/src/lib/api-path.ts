/**
 * Building the path of an API request.
 *
 * Every value that goes into an API path goes through {@link apiPath}, a
 * tagged template: it checks each interpolated value is exactly one path
 * segment, percent-encodes it, and returns the only type apiClient accepts.
 *
 *     apiClient.delete(apiPath`/api/v1/clusters/${clusterId}/pools/${poolId}`)
 *
 * # Why a tag, and why it refuses anything
 *
 * A browser does not send the URL it is given. The WHATWG URL parser every
 * browser shares — fetch, XMLHttpRequest and the address bar alike —
 * resolves "." and ".." path segments before the request leaves: it drops a
 * ".", drops a ".." together with the segment before it, and treats "%2e"
 * in either case as a dot while doing so. encodeURIComponent leaves a dot
 * alone, so encoding a name does not protect it. A pool named ".." made
 * `/api/v1/clusters/<id>/pools/..` go out as `/api/v1/clusters/<id>/`, and
 * the server routed that DELETE to the cluster: deleting the pool deleted
 * the cluster.
 *
 * What is dangerous was MEASURED with the real parser rather than guessed,
 * and api-path.test.ts keeps measuring it: it compares this module's refusals
 * with what `new URL()` does to a generated corpus. Once a value has been
 * through encodeURIComponent, exactly three values fail to arrive as the
 * segment they were meant to be:
 *
 *   - "." and "..", which are resolved away as above;
 *   - "", which leaves an empty segment. The parser keeps that one, but the
 *     server's router ignores a trailing slash, so `.../pools/` reaches the
 *     pool COLLECTION rather than a pool.
 *
 * Everything else survives intact, including every spelling of a dot that
 * is not a bare dot: encodeURIComponent turns "%2e" into "%252e", which no
 * parser treats as a dot, and turns "/" into "%2F", which the browser sends
 * as it is, so "infra/prod" and "a/.." each stay ONE segment. Encoding is
 * what makes that true, so the tag always encodes — a value interpolated
 * RAW would be dangerous in far more ways ("a/..", "%2e%2e", "\", a tab).
 *
 * The server refuses a write whose path ends in "/"
 * (refuseTrailingSlashWrites, internal/api/middleware.go), which is what a
 * FINAL dot segment leaves behind. A dot segment in the MIDDLE of a path
 * leaves no such mark — `.../pools/../members` goes out as
 * `/api/v1/clusters/<id>/members` — so for that case this module is the only
 * guard.
 *
 * A tag rather than a `pathSegment(v)` function: a function has to be
 * remembered at every interpolation, and a site that forgets it still
 * compiles and still works for every name but the dangerous one. The tag
 * validates and encodes every interpolation it is given, UUIDs included
 * (they pass and encode to themselves), so there is no allow-list of "safe"
 * identifiers to maintain, and api-path.guard.test.ts fails on an API path
 * template that is not tagged.
 *
 * # The query string
 *
 * Everything after the first "?" in the template is the query. There, a
 * string or number is encoded as a query value, and a URLSearchParams is
 * spliced in as it serialises itself, already encoded; an empty query drops
 * its "?". Dot segments mean nothing in a query, so nothing there is refused.
 *
 *     apiPath`/api/v1/settings/${key}?scope=${scope}`
 *     apiPath`/api/v1/tasks?${params}`
 */

declare const apiPathBrand: unique symbol;

/**
 * A request path built by {@link apiPath}. apiClient takes nothing else, so a
 * path assembled any other way does not type-check.
 */
export type ApiPath = string & { readonly [apiPathBrand]: true };

/**
 * A value that cannot be sent as one path segment. Thrown by {@link apiPath},
 * from inside the queryFn or mutationFn that builds the path, so it reaches
 * the query's error state or the mutation's error toast like any failed
 * request.
 */
export class PathSegmentError extends Error {
  override readonly name = "PathSegmentError";

  constructor(
    readonly value: unknown,
    reason: string,
  ) {
    super(reason);
  }
}

/**
 * Why `value` cannot be sent as one API path segment, or null when it can.
 * The measured rule; see the module comment.
 */
export function pathSegmentProblem(value: string): string | null {
  if (value === "") {
    return "an empty name cannot be sent as a path segment: the request would reach the collection, not one object";
  }
  if (value === "." || value === "..") {
    return `"${value}" cannot be sent as a path segment: a browser resolves "." and ".." in a URL before sending it, so the request would reach a different resource`;
  }
  return null;
}

/**
 * The explanation a row shows, as visible text in place of its actions, for
 * an object Nexara cannot address — or null when it can.
 */
export function unaddressableHint(name: string): string | null {
  if (pathSegmentProblem(name) === null) {
    return null;
  }
  return name === ""
    ? "An object with no name cannot be managed here. Use the Proxmox web UI."
    : `A browser cannot send the name "${name}" in a request path, so Nexara cannot manage it. Use the Proxmox web UI.`;
}

/**
 * A path value whose colons must reach the server as colons. Built by
 * {@link keepColons}.
 */
class ColonSegment {
  constructor(readonly value: string) {}
}

/**
 * Marks a path value to be sent with its colons unencoded, for the two path
 * slots whose route takes a colon literally: a network interface ("eth0:0",
 * PVE's alias interface) and an SDN subnet id (an IPv6 subnet's id carries
 * the address's colons). Their Nexara routes match the raw parameter against
 * a pattern with ":" in it (pve-object-id-colon, internal/api/apischema/
 * catalogue.go) and do not decode it, so an encoded "%3A" is refused.
 *
 * The value is still checked and every other character encoded. A colon is
 * not a separator, cannot form a dot segment, and leaves the path as one
 * segment; the path always starts with "/", so no colon can be read as a
 * scheme.
 */
export function keepColons(value: string): ColonSegment {
  return new ColonSegment(value);
}

/**
 * Query parameters from a plain object, leaving out every key whose value is
 * undefined — for a flag sent only when set:
 *
 *     apiPath`/api/v1/clusters/${id}?${queryParams({ force: force ? "true" : undefined })}`
 */
export function queryParams(
  entries: Record<string, string | number | undefined>,
): URLSearchParams {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(entries)) {
    if (value !== undefined) {
      params.set(key, String(value));
    }
  }
  return params;
}

type PathValue = string | number | ColonSegment | URLSearchParams;

/** One interpolated value, checked and encoded as a path segment. */
function segment(value: unknown): string {
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new PathSegmentError(
        value,
        `${String(value)} cannot be sent as a path segment`,
      );
    }
    return String(value);
  }
  const colons = value instanceof ColonSegment;
  const text = colons ? value.value : value;
  if (typeof text !== "string") {
    throw new PathSegmentError(
      value,
      "a path segment must be a string or a number",
    );
  }
  const problem = pathSegmentProblem(text);
  if (problem !== null) {
    throw new PathSegmentError(text, problem);
  }
  let encoded: string;
  try {
    encoded = encodeURIComponent(text);
  } catch {
    // A lone surrogate, which no name the API holds can contain.
    throw new PathSegmentError(
      text,
      "a name that is not valid Unicode cannot be sent as a path segment",
    );
  }
  // encodeURIComponent writes every escape as "%" and two upper-case hex
  // digits and escapes every "%" it is given, so "%3A" in its output is
  // always an escaped colon.
  return colons ? encoded.replaceAll("%3A", ":") : encoded;
}

/** One interpolated value in the query string. */
function queryValue(value: unknown): string {
  if (value instanceof URLSearchParams) {
    return value.toString();
  }
  if (typeof value === "string" || typeof value === "number") {
    return encodeURIComponent(String(value));
  }
  throw new Error(
    "apiPath: a query value must be a string, a number or URLSearchParams",
  );
}

/**
 * Why a string is not a request path the SPA may send, or null when it is
 * one: it must start with "/api/"; nowhere may it hold a "#" (a fragment
 * ends the path), a backslash (which the URL parser reads as "/" in an http
 * URL), or a space or control character, U+0000 to U+0020 — the parser
 * deletes a TAB, CR or LF wherever it is and strips the rest from both ends
 * of the URL, so ".\t." would arrive as ".." and a final ".. " as a final
 * ".."; and every segment of the path must be non-empty and not a dot
 * segment in any spelling the parser resolves — ".", "..", or "%2e" in
 * either case for either dot.
 *
 * The one definition both halves use: the tag holds its own output to it,
 * and {@link assertApiPath} holds every request to it where the request
 * leaves. api-path.test.ts checks it against the parser: every path it
 * accepts from a corpus of forged strings arrives with every segment intact.
 */
function apiPathProblem(path: string): string | null {
  if (!path.startsWith("/api/")) {
    return "an API path starts with /api/";
  }
  // eslint-disable-next-line no-control-regex -- U+0000 to U+0020 is the point: the characters the parser deletes or strips
  if (/[\u0000- #\\]/.test(path)) {
    return 'an API path holds no "#", backslash, space or control character';
  }
  const at = path.indexOf("?");
  const pathPart = at < 0 ? path : path.slice(0, at);
  for (const part of pathPart.slice(1).split("/")) {
    if (part === "" || /^(?:\.|%2e){1,2}$/i.test(part)) {
      return `the path ${pathPart} has an empty or dot segment, or ends in "/"`;
    }
  }
  return null;
}

/**
 * The runtime half of the {@link ApiPath} brand. The brand is a compile-time
 * type only — an `as ApiPath` or `as never` gets any value past it — so the
 * points where a request actually leaves check it again: request(),
 * apiFetch() and openApiRequest() in lib/api-client.ts, which between them
 * make every fetch and XHR the SPA sends (ESLint refuses fetch and
 * XMLHttpRequest anywhere else; see eslint.config.js). A URL the browser
 * loads by itself — an <img src>, a <link href>, a WebSocket — passes through
 * none of them; only the path guard's rules 1 and 4 hold those, and only
 * where the fixed text shows "/api/".
 *
 * Throws a {@link PathSegmentError}, before anything is sent, for anything
 * but a string that passes the check the tag holds its own output to. What
 * passes is safe to send whoever built it: the browser sends every segment
 * it names in its place, percent-encoding aside. The parser escapes some
 * characters itself — among them `"`, a backtick, "<", ">", "{", "}" and
 * anything outside ASCII; a `"` goes out as %22 — and the router does not
 * decode a segment, so a name holding one arrives escaped, but still as one
 * segment in its place (see apiPathProblem).
 */
export function assertApiPath(path: unknown): void {
  if (typeof path !== "string") {
    throw new PathSegmentError(path, "an API path must be a string");
  }
  const problem = apiPathProblem(path);
  if (problem !== null) {
    throw new PathSegmentError(path, problem);
  }
}

/**
 * The tag. Every interpolation before the first "?" must be a WHOLE path
 * segment — between two slashes, or after the last one — so that checking
 * the value is checking the segment; everything after it is the query.
 *
 * Throws a {@link PathSegmentError} for a value that cannot be one segment,
 * and a plain Error for a template that is written wrong (not under /api/, a
 * partial segment, a "#", backslash, space or control character in its
 * fixed text, an empty or dot segment of its own, a trailing slash) — the
 * second kind is a bug in the call site, which a test reaches the first time
 * it runs it.
 */
export function apiPath(
  strings: TemplateStringsArray,
  ...values: readonly PathValue[]
): ApiPath {
  let path = "";
  let query: string | null = null;
  for (let i = 0; i < strings.length; i++) {
    const literal = strings[i] ?? "";
    if (query !== null) {
      query += literal;
    } else {
      const at = literal.indexOf("?");
      if (at < 0) {
        path += literal;
      } else {
        path += literal.slice(0, at);
        query = literal.slice(at + 1);
      }
    }
    if (i === values.length) {
      break;
    }
    const value = values[i];
    if (query !== null) {
      query += queryValue(value);
      continue;
    }
    const next = strings[i + 1] ?? "";
    if (
      !path.endsWith("/") ||
      !(next === "" || next.startsWith("/") || next.startsWith("?"))
    ) {
      throw new Error(
        "apiPath: an interpolation must be a whole path segment, between two slashes or after the last one",
      );
    }
    if (value instanceof URLSearchParams) {
      throw new Error("apiPath: query parameters belong after the ?");
    }
    path += segment(value);
  }
  const out = query === null || query === "" ? path : `${path}?${query}`;
  // Every value was checked and encoded above — an encoded value holds no
  // "#", backslash, space or control character and no bare dot run — so
  // what can fail here is the template's own fixed text: a path that does
  // not start at "/api/", one of those characters, which the URL parser
  // would rewrite (a fixed ".\t." reaches the server as ".."), an empty or
  // dot segment, or a trailing slash. One check, the same one assertApiPath
  // runs, rather than a second copy on each literal.
  const problem = apiPathProblem(out);
  if (problem !== null) {
    throw new Error(`apiPath: ${problem}`);
  }
  return out as ApiPath;
}
