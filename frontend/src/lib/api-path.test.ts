import { describe, expect, it } from "vitest";

import {
  apiPath,
  assertApiPath,
  keepColons,
  PathSegmentError,
  pathSegmentProblem,
  queryParams,
  unaddressableHint,
} from "./api-path";

/**
 * The oracle for every refusal here is the URL parser a browser runs, not a
 * restatement of the rule in api-path.ts. `URL` in this environment is NOT
 * jsdom's own: vitest's jsdom environment (4.1.11) replaces it with a
 * subclass of node:url's URL, which is Node's implementation of the WHATWG
 * URL Standard — the standard fetch and XMLHttpRequest apply to a request's
 * URL before sending it. The same behaviour was measured on the wire with
 * Node 20's fetch, and a review found no disagreement against whatwg-url
 * (16.0.1), the standard's reference implementation, either.
 *
 * The question asked of the parser is the one that matters: does a value,
 * encoded the way the SPA always encoded path values, still arrive as the
 * one segment it was put in as?
 */
const BASE = "http://nexara.example.com";
const CLUSTER = "3f2504e0-4f89-11d3-9a0c-0305e82c3301";
const PREFIX = `/api/v1/clusters/${CLUSTER}/pools/`;

/** What the browser sends for a path, per the parser. */
function onTheWire(path: string): string {
  return new URL(path, BASE).pathname;
}

/**
 * Whether `value`, encoded with encodeURIComponent and put in a path as its
 * final segment or as a middle one, reaches the server as that one segment:
 * the parser leaves the path exactly as built, and the segment decodes back
 * to the value.
 */
function survivesTheParser(value: string, where: "final" | "middle"): boolean {
  const built = `${PREFIX}${encodeURIComponent(value)}${where === "middle" ? "/members" : ""}`;
  const sent = onTheWire(built);
  if (sent !== built) {
    return false;
  }
  const segments = sent.split("/");
  const at = where === "middle" ? segments.length - 2 : segments.length - 1;
  return decodeURIComponent(segments[at] ?? "\u0000") === value;
}

/** apiPath's answer for the same value in the same place. */
function built(value: string, where: "final" | "middle"): string {
  return where === "middle"
    ? apiPath`/api/v1/clusters/${CLUSTER}/pools/${value}/members`
    : apiPath`/api/v1/clusters/${CLUSTER}/pools/${value}`;
}

function refuses(value: string, where: "final" | "middle"): boolean {
  try {
    built(value, where);
    return false;
  } catch (err) {
    if (err instanceof PathSegmentError) {
      return true;
    }
    throw err;
  }
}

/**
 * Every string of up to three characters over an alphabet chosen to reach
 * each way a segment can be re-read: the dot and every piece of its "%2e"
 * spelling in both cases, both separators the parser knows ("/" and, in an
 * http URL, "\"), a tab (the parser strips tabs and newlines from anywhere
 * in the URL), a space, the characters that end a path ("?" and "#"), a
 * letter, and "%" alone.
 */
function corpus(): string[] {
  const alphabet = [
    ".",
    "%",
    "2",
    "e",
    "E",
    "/",
    "\\",
    "\t",
    " ",
    "?",
    "#",
    "a",
  ];
  const out: string[] = [];
  let frontier = [""];
  for (let length = 1; length <= 3; length++) {
    frontier = frontier.flatMap((prefix) => alphabet.map((c) => prefix + c));
    out.push(...frontier);
  }
  return out;
}

/**
 * Forged paths: raw text of the kind a cast lets past the brand, never
 * encoded. Every string of up to three characters over an alphabet that
 * reaches each way the parser rewrites a path — the dot and its "%2e"
 * spelling, both separators, the characters it deletes anywhere (TAB, LF,
 * CR), the ones it strips from the ends of the URL (NUL, another C0 control,
 * space), the characters that end a path ("?" and "#") and a letter — as a
 * final segment and as a middle one.
 */
function forgedCorpus(): string[] {
  const alphabet = [
    ".",
    "%",
    "2",
    "e",
    "E",
    "/",
    "\\",
    "\t",
    "\n",
    "\r",
    "\u0000",
    "\u001f",
    " ",
    "?",
    "#",
    "a",
  ];
  const out: string[] = [];
  let frontier = [""];
  for (let length = 1; length <= 3; length++) {
    frontier = frontier.flatMap((prefix) => alphabet.map((c) => prefix + c));
    for (const value of frontier) {
      out.push(`${PREFIX}${value}`, `${PREFIX}${value}/members`);
    }
  }
  return out;
}

describe("apiPath against the URL parser", () => {
  // The values the brief names, each checked in both positions.
  const named = [
    ".",
    "..",
    "%2e",
    "%2E%2E",
    ".%2e",
    "...",
    ".a",
    "a/b",
    "infra/prod",
    "a/..",
    "../a",
    "pool with spaces",
    "ünïcødé-池",
    "a?b",
    "a#b",
    "\\",
    "%",
  ];

  it.each(
    named.flatMap((v) => [[v, "final"] as const, [v, "middle"] as const]),
  )(
    "refuses %j as a %s segment exactly when the parser would not deliver it",
    (value, where) => {
      const dangerous = !survivesTheParser(value, where);
      expect(refuses(value, where)).toBe(dangerous);
      if (!dangerous) {
        // And what it builds, the parser sends unchanged.
        const path = built(value, where);
        expect(onTheWire(path)).toBe(path);
      }
    },
  );

  it("has the parser resolving exactly the two bare dot names, and the spellings that are dots only unencoded", () => {
    // The precondition that makes the refusals mean something: the parser
    // really does move these requests elsewhere...
    expect(onTheWire(`${PREFIX}..`)).toBe(`/api/v1/clusters/${CLUSTER}/`);
    expect(onTheWire(`${PREFIX}.`)).toBe(`/api/v1/clusters/${CLUSTER}/pools/`);
    expect(onTheWire(`${PREFIX}../members`)).toBe(
      `/api/v1/clusters/${CLUSTER}/members`,
    );
    // ...including "%2e" spellings, when they reach it raw...
    expect(onTheWire(`${PREFIX}%2E%2E`)).toBe(`/api/v1/clusters/${CLUSTER}/`);
    expect(onTheWire(`${PREFIX}.%2e`)).toBe(`/api/v1/clusters/${CLUSTER}/`);
    // ...which encoding stops, so as VALUES they are ordinary names.
    expect(built("%2E%2E", "final")).toBe(`${PREFIX}%252E%252E`);
    expect(built("a/..", "final")).toBe(`${PREFIX}a%2F..`);
  });

  it("has the parser deleting a tab, CR and LF and reading a backslash as a slash", () => {
    // Why the tag refuses those four in a template's FIXED text, where no
    // encoding protects them: each turns ".?." into a ".." the parser then
    // resolves.
    expect(onTheWire("/api/v1/x/.\t./y")).toBe("/api/v1/y");
    expect(onTheWire("/api/v1/x/.\r./y")).toBe("/api/v1/y");
    expect(onTheWire("/api/v1/x/.\n./y")).toBe("/api/v1/y");
    expect(onTheWire("/api/v1/x\\..\\y")).toBe("/api/v1/y");
  });

  it("has the parser stripping a space or C0 control from the end of the URL", () => {
    // Why a space and every control character are refused too, not only the
    // three the parser deletes wherever they are: at the end of the URL it
    // strips the rest, and what is left of a final ".. " is a final "..".
    expect(onTheWire(`${PREFIX}.. `)).toBe(`/api/v1/clusters/${CLUSTER}/`);
    expect(onTheWire(`${PREFIX}..\u0000`)).toBe(`/api/v1/clusters/${CLUSTER}/`);
    expect(onTheWire(`${PREFIX}.\u001f`)).toBe(
      `/api/v1/clusters/${CLUSTER}/pools/`,
    );
  });

  it("agrees with the parser over every short string, in both positions", () => {
    const disagreements: string[] = [];
    let refused = 0;
    for (const value of corpus()) {
      for (const where of ["final", "middle"] as const) {
        const dangerous = !survivesTheParser(value, where);
        const got = refuses(value, where);
        if (got !== dangerous) {
          disagreements.push(
            `${JSON.stringify(value)} (${where}): refused=${String(got)}`,
          );
        }
        if (got) {
          refused++;
        }
      }
    }
    expect(disagreements).toEqual([]);
    // Positive control: the corpus contains the dangerous values, so a
    // tag that refused nothing could not pass the comparison above.
    expect(refused).toBe(4);
  });

  it("refuses the empty name, which the parser keeps and the server's router drops", () => {
    // Not a parser fact: `.../pools/` goes out as written. It is the
    // server's — Fiber ignores a trailing slash (StrictRouting is off), so
    // the request routes to the pool COLLECTION; the Go test
    // TestTrailingSlashDeleteNeverReachesTheClusterDelete measures that
    // dispatch. An empty segment names no object in either position.
    expect(onTheWire(PREFIX)).toBe(PREFIX);
    expect(refuses("", "final")).toBe(true);
    expect(refuses("", "middle")).toBe(true);
  });

  it("names the value and the reason in what it throws", () => {
    let thrown: unknown;
    try {
      built("..", "final");
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toBeInstanceOf(PathSegmentError);
    expect((thrown as PathSegmentError).value).toBe("..");
    expect((thrown as PathSegmentError).message).toBe(pathSegmentProblem(".."));
  });
});

describe("apiPath", () => {
  it("encodes every value, and leaves a UUID as it is", () => {
    expect(
      apiPath`/api/v1/clusters/${CLUSTER}/nodes/${"pve-01"}/network/${"vmbr0"}`,
    ).toBe(`/api/v1/clusters/${CLUSTER}/nodes/pve-01/network/vmbr0`);
    expect(
      apiPath`/api/v1/clusters/${CLUSTER}/access/users/${"root@pam"}`,
    ).toBe(`/api/v1/clusters/${CLUSTER}/access/users/root%40pam`);
  });

  it("takes a finite number as its digits and refuses any other", () => {
    expect(apiPath`/api/v1/clusters/${CLUSTER}/firewall/rules/${0}`).toBe(
      `/api/v1/clusters/${CLUSTER}/firewall/rules/0`,
    );
    expect(
      () => apiPath`/api/v1/clusters/${CLUSTER}/firewall/rules/${Number.NaN}`,
    ).toThrow(PathSegmentError);
  });

  it("refuses a value that is not a string or a number, whatever the types said", () => {
    const missing = undefined as unknown as string;
    expect(() => apiPath`/api/v1/clusters/${missing}/nodes`).toThrow(
      PathSegmentError,
    );
  });

  it("keeps colons raw only where asked, and still refuses a dot name there", () => {
    const iface = apiPath`/api/v1/clusters/${CLUSTER}/networks/${"pve-01"}/${keepColons("eth0:0")}`;
    expect(iface).toBe(`/api/v1/clusters/${CLUSTER}/networks/pve-01/eth0:0`);
    expect(onTheWire(iface)).toBe(iface);
    const subnet = apiPath`/api/v1/clusters/${CLUSTER}/sdn/vnets/${"vnet1"}/subnets/${keepColons("zone1-2001:db8::-64")}`;
    expect(onTheWire(subnet)).toBe(
      `/api/v1/clusters/${CLUSTER}/sdn/vnets/vnet1/subnets/zone1-2001:db8::-64`,
    );
    // Only the colon is left alone.
    expect(apiPath`/api/v1/x/${keepColons("a:b/c")}`).toBe("/api/v1/x/a:b%2Fc");
    expect(apiPath`/api/v1/x/${"a:b"}`).toBe("/api/v1/x/a%3Ab");
    expect(() => apiPath`/api/v1/x/${keepColons("..")}`).toThrow(
      PathSegmentError,
    );
  });

  it("encodes query values, splices URLSearchParams, and drops an empty query", () => {
    expect(apiPath`/api/v1/settings/${"k"}?scope=${"a&b=c"}`).toBe(
      "/api/v1/settings/k?scope=a%26b%3Dc",
    );
    const params = new URLSearchParams({ limit: "50", q: "a b" });
    expect(apiPath`/api/v1/tasks?${params}`).toBe(
      "/api/v1/tasks?limit=50&q=a+b",
    );
    expect(apiPath`/api/v1/tasks?${new URLSearchParams()}`).toBe(
      "/api/v1/tasks",
    );
    expect(apiPath`/api/v1/tasks?limit=${50}`).toBe("/api/v1/tasks?limit=50");
    // A dot is nothing special in a query.
    expect(apiPath`/api/v1/tasks?node=${".."}`).toBe("/api/v1/tasks?node=..");
  });

  it("builds query params from an object, leaving out what is undefined", () => {
    expect(
      queryParams({ force: "true", skip: undefined, n: 3 }).toString(),
    ).toBe("force=true&n=3");
    expect(
      apiPath`/api/v1/clusters/${CLUSTER}?${queryParams({ revoke_pve_credentials: undefined })}`,
    ).toBe(`/api/v1/clusters/${CLUSTER}`);
  });

  it.each([
    ["a template outside /api/", () => apiPath`/healthz`],
    ["a template that starts with a value", () => apiPath`${"/api/v1/x"}/y`],
    ["a partial segment", () => apiPath`/api/v1/x/y-${"z"}`],
    ["a value glued to the next literal", () => apiPath`/api/v1/x/${"z"}.json`],
    [
      "query parameters in the path",
      () => apiPath`/api/v1/x/${new URLSearchParams()}`,
    ],
    ["a fragment", () => apiPath`/api/v1/x#y`],
    // Characters the parser rewrites or deletes: a backslash reads as "/"
    // in an http URL, and a TAB, CR or LF is deleted — so a fixed ".\t."
    // would reach the server as "..".
    ["a backslash in the fixed text", () => apiPath`/api/v1/x\\y`],
    ["a tab in the fixed text", () => apiPath`/api/v1/x/.\t./y`],
    ["a CR in the fixed text", () => apiPath`/api/v1/x/.\r./y`],
    ["a LF in the fixed text", () => apiPath`/api/v1/x/.\n./y`],
    // And a space or any other control character, which the parser strips
    // from the end of the URL: a fixed final ".. " would arrive as "..".
    ["a space in the fixed text", () => apiPath`/api/v1/x/.. `],
    ["a NUL in the fixed text", () => apiPath`/api/v1/x/..\u0000`],
    // The whole template is held to it, the query included.
    [
      "a fragment in the fixed query",
      () => apiPath`/api/v1/tasks?node=${"a"}#b`,
    ],
    ["a tab in the fixed query", () => apiPath`/api/v1/tasks?node=\t${"a"}`],
    ["a trailing slash", () => apiPath`/api/v1/x/`],
    ["an empty static segment", () => apiPath`/api/v1//x`],
    ["a static dot segment", () => apiPath`/api/v1/./x`],
  ])("refuses %s as a programming error", (_name, build) => {
    let thrown: unknown;
    try {
      build();
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toBeInstanceOf(Error);
    expect(thrown).not.toBeInstanceOf(PathSegmentError);
  });
});

describe("assertApiPath", () => {
  it("passes every path the tag builds, over the whole corpus", () => {
    let checked = 0;
    for (const value of [...corpus(), "infra/prod", "ünï", "a b"]) {
      for (const where of ["final", "middle"] as const) {
        if (refuses(value, where)) continue;
        const path = built(value, where);
        expect(() => {
          assertApiPath(path);
        }).not.toThrow();
        checked++;
      }
      // And with a query carrying the same value, both ways the tag takes one.
      expect(() => {
        assertApiPath(apiPath`/api/v1/tasks?node=${value}`);
        assertApiPath(
          apiPath`/api/v1/tasks?${new URLSearchParams({ q: value })}`,
        );
      }).not.toThrow();
    }
    // Anti-vacuity: the corpus is almost entirely paths the tag builds.
    expect(checked).toBeGreaterThan(3000);
  });

  it("delivers every segment of every forged path it passes, and nothing after a slash", () => {
    // The oracle for the check itself: whatever passes, however it was
    // built, the browser sends as written.
    const rerouted: string[] = [];
    let passed = 0;
    for (const forged of forgedCorpus()) {
      try {
        assertApiPath(forged);
      } catch (err) {
        if (err instanceof PathSegmentError) continue;
        throw err;
      }
      passed++;
      const at = forged.indexOf("?");
      const pathPart = at < 0 ? forged : forged.slice(0, at);
      const sent = onTheWire(forged);
      if (sent !== pathPart || sent.endsWith("/")) {
        rerouted.push(`${JSON.stringify(forged)} -> ${sent}`);
      }
    }
    expect(rerouted).toEqual([]);
    // Anti-vacuity, measured: 718 of the 8736 forged paths pass — the ones
    // written only in ".", "%", "2", "e", "E", "/", "?" and "a" that hold no
    // empty or dot segment — so the comparison ran over them. Exact, so a
    // check that began refusing more would show here too.
    expect(passed).toBe(718);
  });

  // Each forged path is refused for the reason its name gives, so a case
  // cannot stay green on a rule other than the one it is there to hold.
  const REASONS = {
    prefix: /^an API path starts with \/api\/$/,
    character:
      /^an API path holds no "#", backslash, space or control character$/,
    segment: /^the path .* has an empty or dot segment, or ends in "\/"$/,
  } as const;

  it.each([
    ["a pool named ..", "segment", `${PREFIX}..`],
    ["a pool named .", "segment", `${PREFIX}.`],
    ["a dot segment in the middle", "segment", `${PREFIX}../members`],
    ["a %2e%2e segment", "segment", `${PREFIX}%2e%2E`],
    ["a .%2e segment", "segment", `${PREFIX}.%2e`],
    ["a %2e. segment", "segment", `${PREFIX}%2E.`],
    ["a trailing slash", "segment", PREFIX],
    ["a doubled slash", "segment", `/api/v1/clusters//pools`],
    ["a backslash", "character", `/api/v1/clusters\\${CLUSTER}`],
    ["a tab", "character", `/api/v1/clusters/.\t./pools`],
    ["a CR", "character", `/api/v1/clusters/x\ry`],
    ["a LF", "character", `/api/v1/clusters/x\ny`],
    ['a pool named ".. "', "character", `${PREFIX}.. `],
    ['a pool named "..\\u0000"', "character", `${PREFIX}..\u0000`],
    ["a fragment", "character", `/api/v1/clusters/x#y`],
    ["a fragment in the query", "character", `/api/v1/tasks?q=a#b`],
    ["a path outside /api/", "prefix", "/healthz"],
    ["a relative path", "prefix", "api/v1/version"],
    // Spelled without "//", so the prefix rule is the only one it breaks.
    ["an absolute URL", "prefix", "https:evil.example.com/api/v1/version"],
    // Every spelling of this one starts with two separators, which breaks a
    // second rule as well — an empty segment, a backslash or a control
    // character between them — so only the reason shows the prefix rule is
    // the one that refused it.
    ["a protocol-relative URL", "prefix", "//evil.example.com/api/v1/version"],
  ] as const)("refuses %s, by the %s rule", (_name, reason, forged) => {
    let thrown: unknown;
    try {
      assertApiPath(forged);
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toBeInstanceOf(PathSegmentError);
    expect((thrown as PathSegmentError).value).toBe(forged);
    expect((thrown as PathSegmentError).message).toMatch(REASONS[reason]);
  });

  it("has the parser sending both URLs above to another host", () => {
    // The precondition that makes those two refusals mean something.
    expect(new URL("https:evil.example.com/api/v1/version", BASE).host).toBe(
      "evil.example.com",
    );
    expect(new URL("//evil.example.com/api/v1/version", BASE).host).toBe(
      "evil.example.com",
    );
  });

  it.each([
    ["a number", 42],
    ["undefined", undefined],
    ["a String object", new String("/api/v1/version")],
    ["an object with a path for its text", { toString: () => "/api/v1/x" }],
  ])("refuses %s, whatever the types said", (_name, forged) => {
    let thrown: unknown;
    try {
      assertApiPath(forged);
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toBeInstanceOf(PathSegmentError);
    expect((thrown as PathSegmentError).message).toBe(
      "an API path must be a string",
    );
  });
});

describe("unaddressableHint", () => {
  it("is null for every name the tag can address, and explains the ones it cannot", () => {
    for (const name of ["infra", ".hidden", "...", "a/..", "%2e%2e"]) {
      expect(unaddressableHint(name)).toBeNull();
    }
    for (const name of [".", "..", ""]) {
      expect(unaddressableHint(name)).toMatch(/Proxmox web UI/);
    }
    expect(unaddressableHint("..")).toContain('".."');
    // A request path, precisely: a query or a body carries ".." fine.
    expect(unaddressableHint("..")).toContain("in a request path");
  });
});
