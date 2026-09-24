/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import { Linter } from "eslint";
import globals from "globals";
import tseslint from "typescript-eslint";

/**
 * The ESLint half of "every request goes through lib/api-client.ts": the
 * block in eslint.config.js that refuses fetch and XMLHttpRequest everywhere
 * else. A lint rule nothing trips is indistinguishable from one that was
 * deleted or scoped to no file, and the shipped tree trips neither of these
 * — so each spelling of a bypass is linted here with the block as committed,
 * and required to come back with exactly the rule that owns it.
 *
 * Only that block is run, over a parser with no type information: its two
 * rules read scope and syntax, not types, and the project's typed rules would
 * need a real file inside the TypeScript project.
 */

const loaded = import.meta.glob<{ default: Linter.Config[] }>(
  "/eslint.config.js",
  { eager: true },
);

/** The committed block that carries the request-sink rules. */
function sinkBlock(): Linter.Config {
  const config = loaded["/eslint.config.js"]?.default ?? [];
  const blocks = config.filter(
    (block) => block.rules?.["no-restricted-globals"] !== undefined,
  );
  expect(blocks).toHaveLength(1);
  const [block] = blocks;
  if (block === undefined) throw new Error("no request-sink block");
  return block;
}

/** The frontend directory, which the config's file patterns are relative to. */
const FRONTEND = new URL("../..", import.meta.url).pathname.replace(/\/$/, "");

const PLANTED = "src/features/planted/planted.ts";

/** The rule ids the committed block reports for `code` in `file`. */
function ruleIds(code: string, file = PLANTED): (string | null)[] {
  const linter = new Linter({ cwd: FRONTEND });
  const messages = linter.verify(
    code,
    [
      {
        files: ["**/*.{ts,tsx,mts,cts}"],
        languageOptions: {
          parser: tseslint.parser,
          ecmaVersion: 2020,
          sourceType: "module",
          globals: globals.browser,
        },
        // The block uses @typescript-eslint/no-restricted-imports; the
        // real config registers the plugin in its first block.
        plugins: { "@typescript-eslint": tseslint.plugin },
      },
      sinkBlock(),
    ],
    { filename: `${FRONTEND}/${file}` },
  );
  // A parse error would come back as a message with no rule: fail on it
  // rather than count it.
  expect(messages.filter((m) => m.fatal === true)).toEqual([]);
  return messages.map((m) => m.ruleId);
}

describe("the request-sink lint rules", () => {
  it.each([
    ["a call of the global fetch", 'void fetch("/api/v1/version");'],
    ["fetch.call", 'void fetch.call(undefined, "/api/v1/version");'],
    ["fetch held in a variable", 'const f = fetch; void f("/api/v1/version");'],
    ["a new XMLHttpRequest", "const xhr = new XMLHttpRequest(); void xhr;"],
    [
      "XMLHttpRequest.prototype.open",
      'declare const xhr: object; XMLHttpRequest.prototype.open.call(xhr, "POST", "/x");',
    ],
    ["fetchLater", 'fetchLater("/api/v1/version", { method: "POST" });'],
    ["a new EventSource", 'const e = new EventSource("/api/v1/x"); void e;'],
  ])("no-restricted-globals reports %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["no-restricted-globals"]);
  });

  it.each([
    ["window.fetch", 'void window.fetch("/api/v1/version");'],
    ["self.fetch", 'void self.fetch("/api/v1/version");'],
    ["globalThis.fetch", 'void globalThis.fetch("/api/v1/version");'],
    ['window["fetch"]', 'void window["fetch"]("/api/v1/version");'],
    [
      "fetch destructured from window",
      'const { fetch: f } = window; void f("/api/v1/version");',
    ],
    [
      "fetch read off another object",
      'void document.defaultView?.fetch("/api/v1/version");',
    ],
    ["window.XMLHttpRequest", "const X = window.XMLHttpRequest; void X;"],
    // Read off an object the rule could not name by identifier: what a rule
    // narrowed to window, self and globalThis would miss.
    ["top.fetch", 'void top?.fetch("/api/v1/version");'],
    ["parent.fetch", 'void parent.fetch("/api/v1/version");'],
    [
      "an iframe's window",
      'declare const f: HTMLIFrameElement; void f.contentWindow?.fetch("/api/v1/version");',
    ],
    ["navigator.sendBeacon", 'navigator.sendBeacon("/api/v1/x", "{}");'],
    ["window.fetchLater", 'window.fetchLater("/api/v1/version");'],
    ["window.EventSource", "const E = window.EventSource; void E;"],
  ])("no-restricted-properties reports %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["no-restricted-properties"]);
  });

  it.each([
    [
      "a local that happens to be called fetch",
      'function load(fetch: (u: string) => void) { fetch("/x"); }',
    ],
    [
      // A real binding, unlike an ambient one: the call reaches this
      // function, not the global.
      "a local function that happens to be called fetch",
      'const fetch = (u: string) => u.length; void fetch("/x");',
    ],
    [
      // The request openApiRequest returns: re-opening it is refused by its
      // type and by its own open(), which throws (api-client.test.ts) —
      // neither of which these rules can see.
      "an open() on some other object",
      'declare const req: { open(m: string, u: string): void }; req.open("POST", "/x");',
    ],
    ["the XMLHttpRequest type", "declare const x: XMLHttpRequest; void x;"],
  ])("reports nothing for %s", (_name, code) => {
    expect(ruleIds(code)).toEqual([]);
  });

  // An ambient declaration makes the name a module binding: the calls below
  // are no longer references to the global, so no-restricted-globals is
  // silent, yet the declaration emits no code and every call still reaches
  // the global at runtime. Each is reported on its declaration instead.
  it.each([
    [
      "declare function fetch",
      'declare function fetch(u: string): Promise<Response>; void fetch("/api/v1/version");',
    ],
    [
      "declare const XMLHttpRequest",
      'declare const XMLHttpRequest: { new (): { open(m: string, u: string): void } }; new XMLHttpRequest().open("POST", "/x");',
    ],
    [
      "declare function fetchLater",
      'declare function fetchLater(u: string): unknown; fetchLater("/api/v1/version");',
    ],
    [
      "declare let EventSource",
      'declare let EventSource: { new (u: string): object }; void new EventSource("/api/v1/x");',
    ],
    [
      "declare class EventSource",
      'declare class EventSource { constructor(u: string) } void new EventSource("/api/v1/x");',
    ],
  ])("no-restricted-syntax reports %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["no-restricted-syntax"]);
  });

  it("no-restricted-syntax reports an exported ambient declaration too", () => {
    expect(
      ruleIds(
        'export declare function fetch(u: string): Promise<Response>; void fetch("/x");',
      ),
    ).toEqual(["no-restricted-syntax"]);
  });

  // A namespace that is code is a scope like a module's: a declaration in it
  // hides the global from the namespace's own calls, which still reach it.
  it.each([
    [
      "a namespace",
      'namespace Probe { declare function fetch(u: string): Promise<Response>; export const r = fetch("/x"); }',
    ],
    [
      "an exported namespace",
      "export namespace Probe { declare const XMLHttpRequest: { new (): object }; export const x = new XMLHttpRequest(); }",
    ],
    [
      "a nested namespace",
      'namespace A { export namespace B { declare function fetchLater(u: string): unknown; export const r = fetchLater("/x"); } }',
    ],
  ])("no-restricted-syntax reports a declaration inside %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["no-restricted-syntax"]);
  });

  it.each([
    [
      "an ambient declaration of any other name",
      'declare function load(u: string): void; load("/x");',
    ],
    // Declarations inside a declare block, which describe code that lives
    // elsewhere and hold none that runs.
    [
      "a library augmentation",
      'declare module "some-lib" { export function fetch(u: string): Promise<unknown>; }',
    ],
    [
      "a namespace nested in a library augmentation",
      'declare module "some-lib" { namespace Inner { function fetch(u: string): void; } }',
    ],
    [
      "an ambient namespace",
      "declare namespace Client { function fetch(u: string): void; }",
    ],
  ])("no-restricted-syntax leaves alone %s", (_name, code) => {
    expect(ruleIds(code)).toEqual([]);
  });

  it("reports every use of a request API a global augmentation declares", () => {
    // declare global { … } IS the global, so it hides nothing: the use is
    // reported by no-restricted-globals, and the declaration is let be.
    expect(
      ruleIds(
        'declare global { function fetchLater(u: string): unknown } fetchLater("/x"); export {};',
      ),
    ).toEqual(["no-restricted-globals"]);
  });

  // TypeScript compiles an import = alias only of a namespace member, and
  // globalThis is a namespace to it (window and navigator are not): the
  // alias emits const f = globalThis.fetch.
  it.each([
    [
      "globalThis.fetch",
      'import f = globalThis.fetch; export const r = f("/api/v1/version");',
    ],
    [
      "globalThis.EventSource",
      'import E = globalThis.EventSource; export const e = new E("/api/v1/x");',
    ],
  ])("no-restricted-syntax reports an import = alias of %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["no-restricted-syntax"]);
  });

  it("no-restricted-syntax leaves an import = alias of anything else alone", () => {
    expect(
      ruleIds("import L = globalThis.location; export const h = L.href;"),
    ).toEqual([]);
  });

  // The price of reading the name off any object, which the config comment
  // documents: an unrelated method called fetch is reported too, and the
  // sanctioned escape is a disable comment that says why.
  it("reports an unrelated fetch() method, and honours the documented escape", () => {
    const call =
      "declare const query: { fetch(): Promise<void> }; void query.fetch();";
    expect(ruleIds(call)).toEqual(["no-restricted-properties"]);
    expect(
      ruleIds(
        "declare const query: { fetch(): Promise<void> };\n" +
          "// eslint-disable-next-line no-restricted-properties -- TanStack's Query.fetch, not a request\n" +
          "void query.fetch();",
      ),
    ).toEqual([]);
  });

  it.each([
    [
      "the test utilities by alias",
      'import { renderWithProviders } from "@/test/test-utils";',
    ],
    ["the test tree's root", 'import "@/test";'],
    [
      "a re-export from the test tree",
      'export { renderWithProviders } from "@/test/test-utils";',
    ],
    ["a test module", 'import { fixture } from "./widget.test";'],
    ["a test module by its file name", 'import "../widget.test.tsx";'],
  ])("@typescript-eslint/no-restricted-imports reports %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["@typescript-eslint/no-restricted-imports"]);
  });

  it.each([
    ["the test tree", 'export const m = import("@/test/test-utils");'],
    ["a test module", 'export const m = import("./widget.test");'],
    [
      "the test tree, as a template literal",
      "export const m = import(`@/test/test-utils`);",
    ],
    [
      "a test module, as a template literal",
      "export const m = import(`./widget.test`);",
    ],
  ])("no-restricted-syntax reports a dynamic import of %s", (_name, code) => {
    expect(ruleIds(code)).toEqual(["no-restricted-syntax"]);
  });

  it.each([
    ["the api client", 'import { apiFetch } from "@/lib/api-client";'],
    ["a name that only starts with test", 'import "@/testing/helpers";'],
    ["a package with test in its name", 'import "@testing-library/react";'],
    // Erased by the compiler, so nothing can ride in through them.
    [
      "a type-only import from the test tree",
      'import type { RenderOptions } from "@/test/test-utils";',
    ],
    [
      "type-only specifiers from the test tree",
      'import { type RenderOptions } from "@/test/test-utils";',
    ],
    // A relative specifier cannot be told from a production folder named
    // test without resolving it, so it is request-sinks.source.test.ts's.
    ["a production folder named test", 'import "./test/fixtures";'],
    [
      "a dynamic import of any other module",
      'export const m = import("@/lib/api-path");',
    ],
    [
      "a template literal naming any other module",
      "export const m = import(`@/lib/api-path`);",
    ],
    // A computed specifier is seen by nothing: the config comment lists it.
    [
      "a template literal with a substitution",
      "declare const x: string; export const m = import(`@/test/${x}`);",
    ],
  ])("the import rules allow %s", (_name, code) => {
    expect(ruleIds(code)).toEqual([]);
  });

  it("covers exactly the source it should, and exempts exactly the client and the tests", () => {
    // Pinned, so that neither a narrower reach nor a new exemption can slip
    // in beside the test below.
    const block = sinkBlock();
    expect(block.files).toEqual(["src/**/*.{ts,tsx,mts,cts}"]);
    expect(block.ignores).toEqual([
      "src/lib/api-client.ts",
      "src/**/*.test.{ts,tsx,mts,cts}",
      "src/test/**",
    ]);
  });

  it("reaches .mts and .cts source, which the bundler compiles too", () => {
    const code = 'void fetch("/api/v1/version");';
    expect(ruleIds(code, "src/features/planted/planted.mts")).toEqual([
      "no-restricted-globals",
    ]);
    expect(ruleIds(code, "src/features/planted/planted.cts")).toEqual([
      "no-restricted-globals",
    ]);
    expect(ruleIds(code, "src/features/planted/planted.test.mts")).toEqual([]);
  });

  it("exempts only lib/api-client.ts and the tests", () => {
    const code = 'void fetch("/api/v1/version");';
    expect(ruleIds(code, "src/lib/api-client.ts")).toEqual([]);
    expect(ruleIds(code, "src/lib/api-client.test.ts")).toEqual([]);
    expect(ruleIds(code, "src/test/setup.ts")).toEqual([]);
    // A file named like the exempt one, anywhere else, is not it.
    expect(ruleIds(code, "src/features/planted/lib/api-client.ts")).toEqual([
      "no-restricted-globals",
    ]);
    // A test may import the test tree; a production file may not.
    const setup = 'import "@/test/test-utils";';
    expect(ruleIds(setup, "src/features/planted/planted.test.tsx")).toEqual([]);
    expect(ruleIds(setup, "src/features/planted/planted.tsx")).toEqual([
      "@typescript-eslint/no-restricted-imports",
    ]);
  });
});
