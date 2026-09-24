/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import ts from "typescript";

/**
 * Keeps every API path on apiPath (lib/api-path.ts).
 *
 * apiClient, apiFetch and openApiRequest only accept an ApiPath, so the type
 * checker already refuses a plain string there; each re-checks the path at
 * runtime (assertApiPath) before anything is sent, because the brand is a
 * type only; and ESLint refuses fetch and XMLHttpRequest anywhere but
 * lib/api-client.ts (eslint.config.js; request-sinks.lint.test.ts shows each
 * spelling reported), so no fetch or XHR leaves around them — and the XHR
 * openApiRequest returns cannot be re-opened: open() is left out of its
 * type, and the request's own open() throws for a caller who casts past that
 * (api-client.test.ts). What none of those can see is a path built for some
 * other use, or a cast to ApiPath or never. This scan reads the source, not
 * the types, and fails on:
 *
 *   1. a template literal, not tagged apiPath, with "/api/" anywhere in its
 *      fixed text — unless that text starts with a URL scheme and a literal
 *      host, as someone else's URL does (https://hooks.example.com/api/…);
 *      a host that is interpolated (`https://${location.host}/api/…`) may
 *      be this one, so it is not exempt;
 *   2. an apiPath template that does not start with a literal "/api/";
 *   3. an apiPath template the tag would refuse for its own fixed text: an
 *      interpolation before the "?" that is not a whole segment, a path that
 *      ends in "/", an empty or dot segment ("." or ".." or "%2e" in either
 *      case), or a "#", backslash, space or control character anywhere in the
 *      fixed text (the tag throws on each at runtime; this finds it without
 *      running it);
 *   4. a "+" that appends anything but a literal after a literal starting
 *      with "/api/";
 *   5. a type assertion to ApiPath — under its own name or any local alias —
 *      or to never, anywhere but lib/api-path.ts itself.
 *
 * A URL the browser loads by itself — a WebSocket's, window.open's, an href
 * or src — goes through neither the brand nor assertApiPath: rules 1 and 4
 * are all that hold a path built for one, and only when its fixed text shows
 * "/api/". A template such as `${base}/pools/${id}` shows none, and nothing
 * here catches it; sent with fetch or an XHR it still meets the brand,
 * ESLint and assertApiPath.
 *
 * Test files are not scanned: they build paths as expected values on
 * purpose, and api-path.test.ts writes deliberately broken templates.
 */

// Vite resolves a leading-slash glob against its root (frontend/), so this can
// never reach the stale copies of the tree that live in git worktrees under
// .claude/worktrees/. Every extension the bundler compiles as TypeScript;
// request-sinks.source.test.ts refuses any other source extension under src.
const sources: Record<string, string> = import.meta.glob(
  "/src/**/*.{ts,tsx,mts,cts}",
  { eager: true, query: "?raw", import: "default" },
);

/** The only file the exemption names — exactly this, not any path ending so. */
const API_PATH_FILE = "/src/lib/api-path.ts";

interface Finding {
  file: string;
  line: number;
  rule: number;
  text: string;
}

interface ScanResult {
  findings: Finding[];
  /** apiPath templates seen: the floor below. */
  tagged: number;
}

function isApiPathTag(node: ts.Node): node is ts.TaggedTemplateExpression {
  return (
    ts.isTaggedTemplateExpression(node) && node.tag.getText() === "apiPath"
  );
}

/** The static text of a template, one string per literal part. */
function templateParts(t: ts.TemplateLiteral): string[] {
  return ts.isNoSubstitutionTemplateLiteral(t)
    ? [t.text]
    : [t.head.text, ...t.templateSpans.map((s) => s.literal.text)];
}

/**
 * Whether an apiPath template's fixed text is one the tag refuses at runtime.
 * Interpolations stand in as the segment "x": the tag checks each value
 * itself, and a value is always a whole segment when this passes.
 */
function badFixedText(parts: string[]): boolean {
  // eslint-disable-next-line no-control-regex -- the same class as apiPathProblem's, control characters on purpose
  if (parts.some((p) => /[\u0000- #\\]/.test(p))) {
    return true;
  }
  let region = "";
  let inQuery = false;
  for (let i = 0; i < parts.length && !inQuery; i++) {
    const part = parts[i] ?? "";
    const at = part.indexOf("?");
    region += at < 0 ? part : part.slice(0, at);
    inQuery = at >= 0;
    if (!inQuery && i < parts.length - 1) {
      const next = parts[i + 1] ?? "";
      const whole =
        region.endsWith("/") &&
        (next === "" || next.startsWith("/") || next.startsWith("?"));
      if (!whole) {
        return true;
      }
      region += "x";
    }
  }
  return region
    .slice(1)
    .split("/")
    .some((seg) => seg === "" || /^(?:\.|%2e){1,2}$/i.test(seg));
}

/** The names ApiPath goes by in a file: its own, and any alias of it. */
function apiPathNames(sf: ts.SourceFile): Set<string> {
  const names = new Set(["ApiPath"]);
  const visit = (node: ts.Node): void => {
    if (
      ts.isImportSpecifier(node) &&
      (node.propertyName ?? node.name).text === "ApiPath"
    ) {
      names.add(node.name.text);
    }
    if (ts.isTypeAliasDeclaration(node) && names.has(node.type.getText())) {
      names.add(node.name.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return names;
}

/**
 * Fixed text that starts with a URL scheme and then a literal host
 * ("https://hooks.example.com") names another server's URL, not an API path.
 */
const URL_SCHEME = /^[a-z][a-z0-9+.-]*:\/\/[^/]/i;

function scan(files: Record<string, string>): ScanResult {
  const findings: Finding[] = [];
  let tagged = 0;

  for (const [file, text] of Object.entries(files)) {
    const sf = ts.createSourceFile(
      file,
      text,
      ts.ScriptTarget.Latest,
      true,
      file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );
    const report = (node: ts.Node, rule: number) => {
      findings.push({
        file,
        line: sf.getLineAndCharacterOfPosition(node.getStart()).line + 1,
        rule,
        text: node.getText().replace(/\s+/g, " ").slice(0, 120),
      });
    };
    const aliases = apiPathNames(sf);
    const isLiteral = (e: ts.Node) =>
      ts.isStringLiteral(e) ||
      ts.isNoSubstitutionTemplateLiteral(e) ||
      ts.isNumericLiteral(e);

    const visit = (node: ts.Node): void => {
      // Rules 1-3: templates.
      if (
        ts.isTemplateExpression(node) ||
        ts.isNoSubstitutionTemplateLiteral(node)
      ) {
        const parts = templateParts(node);
        if (isApiPathTag(node.parent)) {
          tagged++;
          if (!(parts[0] ?? "").startsWith("/api/")) {
            // Its segments cannot be judged either: the path it extends is
            // not in the template.
            report(node, 2);
          } else if (badFixedText(parts)) {
            report(node, 3);
          }
        } else if (
          parts.some((p) => p.includes("/api/")) &&
          !URL_SCHEME.test(parts[0] ?? "")
        ) {
          report(node, 1);
        }
      }

      // Rule 4: "+" chains.
      if (
        ts.isBinaryExpression(node) &&
        node.operatorToken.kind === ts.SyntaxKind.PlusToken &&
        !(
          ts.isBinaryExpression(node.parent) &&
          node.parent.operatorToken.kind === ts.SyntaxKind.PlusToken
        )
      ) {
        const operands: ts.Expression[] = [];
        const flatten = (e: ts.Expression): void => {
          if (
            ts.isBinaryExpression(e) &&
            e.operatorToken.kind === ts.SyntaxKind.PlusToken
          ) {
            flatten(e.left);
            flatten(e.right);
          } else {
            operands.push(e);
          }
        };
        flatten(node);
        const firstApi = operands.findIndex(
          (e) =>
            (ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e)) &&
            e.text.startsWith("/api/"),
        );
        if (
          firstApi >= 0 &&
          operands.slice(firstApi + 1).some((e) => !isLiteral(e))
        ) {
          report(node, 4);
        }
      }

      // Rule 5: assertions.
      if (
        (ts.isAsExpression(node) || ts.isTypeAssertionExpression(node)) &&
        (node.type.kind === ts.SyntaxKind.NeverKeyword ||
          aliases.has(node.type.getText())) &&
        file !== API_PATH_FILE
      ) {
        report(node, 5);
      }

      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return { findings, tagged };
}

/** The tree as shipped: every non-test source file. */
function shippedSources(): Record<string, string> {
  return Object.fromEntries(
    Object.entries(sources).filter(
      ([file]) =>
        !/\.test\.(?:[cm]?ts|tsx)$/.test(file) && !/\.d\.[cm]?ts$/.test(file),
    ),
  );
}

/** The one scan of the whole tree; the planted cases scan their own file. */
let shippedScan: ScanResult | undefined;
function scanShipped(): ScanResult {
  shippedScan ??= scan(shippedSources());
  return shippedScan;
}

const PLANTED = "/src/features/planted/planted.ts";

function rulesFound(files: Record<string, string>) {
  return scan(files).findings.map((f) => ({ file: f.file, rule: f.rule }));
}

describe("API paths", () => {
  it("are all built with apiPath", () => {
    const { findings, tagged } = scanShipped();
    expect(findings).toEqual([]);
    // Anti-vacuity: the tree builds some 550 API paths through the tag. A
    // glob or a parse that quietly found nothing would agree with "no
    // findings".
    expect(tagged).toBeGreaterThan(450);
  });

  // Each rule is shown to bite on one planted file, scanned on its own and
  // required to come back with exactly that rule. The shipped tree is clean
  // (above), so a rule that had stopped rejecting anything could not pass.
  it.each([
    [
      1,
      "a raw interpolation",
      "const p = `/api/v1/clusters/${id}/pools/${encodeURIComponent(pool)}`;",
    ],
    [1, "an untagged static template", "const p = `/api/v1/version`;"],
    [
      1,
      "an origin prefixed to an API path",
      "const u = `${window.location.origin}/api/v1/reports/runs/${id}/html`;",
    ],
    [
      1,
      "an API path opened in a new window",
      'window.open(`${origin}/api/v1/reports/runs/${id}/html`, "_blank");',
    ],
    [
      1,
      "a scheme that is not in the first part",
      "const u = `${proto}://${host}/api/v1/clusters/${id}`;",
    ],
    [
      1,
      "a scheme with an interpolated host",
      "const u = `https://${location.host}/api/v1/clusters/${id}`;",
    ],
    [
      2,
      "a tag that does not start at /api/",
      "const p = apiPath`${base}/pools`;",
    ],
    [3, "a partial segment", "const p = apiPath`/api/v1/pools/x-${id}`;"],
    [3, "a trailing slash", "const p = apiPath`/api/v1/pools/`;"],
    [
      3,
      "a trailing slash after a value",
      "const p = apiPath`/api/v1/pools/${id}/`;",
    ],
    [3, "an empty segment", "const p = apiPath`/api/v1//pools`;"],
    [3, "a dot segment", "const p = apiPath`/api/v1/./pools`;"],
    [3, "a %2E%2e segment", "const p = apiPath`/api/v1/%2E%2e/pools`;"],
    [3, "a fragment", "const p = apiPath`/api/v1/pools#x`;"],
    [3, "a backslash", "const p = apiPath`/api/v1\\\\pools`;"],
    [3, "a tab", "const p = apiPath`/api/v1/.\\t./pools`;"],
    [3, "a CR", "const p = apiPath`/api/v1/.\\r./pools`;"],
    [3, "a LF", "const p = apiPath`/api/v1/.\\n./pools`;"],
    [3, "a space", "const p = apiPath`/api/v1/pools/.. `;"],
    [3, "a NUL", "const p = apiPath`/api/v1/pools/..\\u0000`;"],
    [3, "a space in the query", "const p = apiPath`/api/v1/tasks?q=a b`;"],
    [4, "a concatenation", 'const p = "/api/v1/pools/" + id;'],
    [5, "a cast", 'const p = "/api/v1/pools" as ApiPath;'],
    [
      5,
      "a cast through an aliased import",
      'import type { ApiPath as P } from "@/lib/api-path"; const p = "/api/v1/pools" as P;',
    ],
    [
      5,
      "a cast through a type alias",
      'import type { ApiPath } from "@/lib/api-path"; type P = ApiPath; const p = "/api/v1/pools" as P;',
    ],
    [5, "a cast to never", 'apiClient.get("/api/v1/pools" as never);'],
  ])("rule %i catches %s", (rule, _name, code) => {
    expect(rulesFound({ [PLANTED]: code })).toEqual([{ file: PLANTED, rule }]);
  });

  it("exempts only the real lib/api-path.ts, not a file named like it", () => {
    const cast = 'const p = "/api/v1/pools" as ApiPath;';
    expect(
      rulesFound({ "/src/features/planted/lib/api-path.ts": cast }),
    ).toEqual([{ file: "/src/features/planted/lib/api-path.ts", rule: 5 }]);
    // Positive control: the same code in the real file is what it is for.
    expect(rulesFound({ [API_PATH_FILE]: cast })).toEqual([]);
  });

  it.each([
    [
      "an origin prefixed to a static path",
      'const uri = window.location.origin + "/api/v1/auth/oidc/callback";',
    ],
    [
      "an origin prefixed to a tagged path",
      "const u = `${window.location.origin}${apiPath`/api/v1/reports/runs/${id}/html`}`;",
    ],
    [
      "someone else's URL with /api/ inside it",
      "const hook = `https://hooks.example.com/api/webhooks/${id}/${token}`;",
    ],
    [
      "someone else's URL, concatenated",
      'const hook = "https://hooks.example.com/api/webhooks/" + id;',
    ],
    [
      "a query value in a tagged template",
      "const p = apiPath`/api/v1/tasks?q=${q}`;",
    ],
  ])("accepts %s", (_name, code) => {
    expect(rulesFound({ [PLANTED]: code })).toEqual([]);
  });
});
