/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import ts from "typescript";

/**
 * Every date the app shows follows the Date format preference, so it is
 * formatted in lib/format.ts (or by components/DateTime.tsx, which calls it).
 * This reads the source and fails on a date formatted anywhere else:
 * toLocaleDateString / toLocaleTimeString / toDateString / toTimeString /
 * toUTCString on anything, a new Intl.DateTimeFormat or RelativeTimeFormat,
 * and toLocaleString on a value it can see is a Date (new Date(…), a name
 * bound to one or typed Date, or a name ending Date, At or _at). A number's
 * toLocaleString (a count) is not a date. Test files are not scanned.
 */

// A leading-slash glob resolves against Vite's root (frontend/), as in
// api-path.guard.test.ts.
const sources: Record<string, string> = import.meta.glob(
  ["/src/**/*.{ts,tsx}", "!/src/**/*.test.{ts,tsx}", "!/src/test/**"],
  { eager: true, query: "?raw", import: "default" },
);

const FORMAT_FILE = "/src/lib/format.ts";

/** What is allowed outside lib/format.ts, by file and call, and why. */
const ALLOWED: Record<string, string> = {
  "/src/features/guest-tools/lib/virtio-win-schedule.ts Intl.DateTimeFormat":
    "reads the browser's time zone name to send with a schedule; not display",
  "/src/features/history/lib/history.ts toDateString":
    "compares whether two instants fall on the same day; not display",
};

const ALWAYS_DATE = new Set([
  "toLocaleDateString",
  "toLocaleTimeString",
  "toDateString",
  "toTimeString",
  "toUTCString",
]);
const INTL_DATE = new Set(["DateTimeFormat", "RelativeTimeFormat"]);
const DATE_NAME = /(Date|At|_at)$/;

function isNewDate(e: ts.Expression): boolean {
  while (ts.isParenthesizedExpression(e)) e = e.expression;
  return (
    ts.isNewExpression(e) &&
    ts.isIdentifier(e.expression) &&
    e.expression.text === "Date"
  );
}

/** Names this file binds to a Date: `= new Date(…)` or `: Date`. */
function dateNames(sf: ts.SourceFile): Set<string> {
  const names = new Set<string>();
  const visit = (node: ts.Node) => {
    if (
      (ts.isVariableDeclaration(node) || ts.isParameter(node)) &&
      ts.isIdentifier(node.name) &&
      ((node.initializer !== undefined && isNewDate(node.initializer)) ||
        node.type?.getText(sf) === "Date")
    ) {
      names.add(node.name.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return names;
}

/** Each date formatted in a file, as "<file> <call>". */
function findings(file: string): string[] {
  const sf = ts.createSourceFile(
    file,
    sources[file] ?? "",
    ts.ScriptTarget.Latest,
    true,
    file.endsWith("x") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const dates = dateNames(sf);
  const found: string[] = [];
  const visit = (node: ts.Node) => {
    if (
      (ts.isNewExpression(node) || ts.isCallExpression(node)) &&
      ts.isPropertyAccessExpression(node.expression) &&
      node.expression.expression.getText(sf) === "Intl" &&
      INTL_DATE.has(node.expression.name.text)
    ) {
      found.push(`${file} Intl.${node.expression.name.text}`);
    }
    if (
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression)
    ) {
      const method = node.expression.name.text;
      const receiver = node.expression.expression;
      const name = ts.isIdentifier(receiver)
        ? receiver.text
        : ts.isPropertyAccessExpression(receiver)
          ? receiver.name.text
          : "";
      if (
        ALWAYS_DATE.has(method) ||
        (method === "toLocaleString" &&
          (isNewDate(receiver) || dates.has(name) || DATE_NAME.test(name)))
      ) {
        found.push(`${file} ${method}`);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return found;
}

describe("dates", () => {
  const files = Object.keys(sources);
  const all = files.flatMap(findings);

  it("are formatted only in lib/format.ts", () => {
    const outside = all.filter(
      (f) => !f.startsWith(`${FORMAT_FILE} `) && !(f in ALLOWED),
    );
    expect(outside).toEqual([]);
  });

  it("keeps no allow-list entry that nothing uses", () => {
    expect(Object.keys(ALLOWED).filter((k) => !all.includes(k))).toEqual([]);
  });

  // Anti-vacuity: lib/format.ts formats dates itself, so a scan that stops
  // reading the source (or recognising a call) finds nothing there.
  it("sees the formatting lib/format.ts does", () => {
    expect(files.length).toBeGreaterThan(500);
    expect(all).toContain(`${FORMAT_FILE} Intl.DateTimeFormat`);
    expect(all).toContain(`${FORMAT_FILE} toLocaleDateString`);
  });
});
