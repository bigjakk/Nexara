/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import ts from "typescript";

// A failed mutation is reported once: the global toast sees only a hook's own onError, so an
// onError passed to mutate() of a hook without one toasts twice and is lost on unmount. This
// reads the source for that, for settle(x.mutateAsync(…)) on a hook not opted out, and for a
// render of x.error or x.isError when x's hook has no onError. A hook counts as having one
// by its options, a same-file spread or errorsHandledLocally; an unreadable spread is not judged.

// A leading-slash glob resolves against Vite's root (frontend/), as in
// api-path.guard.test.ts.
const sources: Record<string, string> = import.meta.glob(
  ["/src/**/*.{ts,tsx}", "!/src/**/*.test.{ts,tsx}", "!/src/test/**"],
  { eager: true, query: "?raw", import: "default" },
);

/**
 * "param": only when the hook is called with `true` for its flag, which is its
 * last parameter (errorsShownByCaller = false).
 */
type Has = "yes" | "no" | "unknown" | "param";

function parse(file: string): ts.SourceFile {
  return ts.createSourceFile(
    file,
    sources[file] ?? "",
    ts.ScriptTarget.Latest,
    true,
    file.endsWith("x") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}

function objectNamed(sf: ts.SourceFile, name: string) {
  let found: ts.ObjectLiteralExpression | null = null;
  const visit = (node: ts.Node) => {
    if (
      ts.isVariableDeclaration(node) &&
      ts.isIdentifier(node.name) &&
      node.name.text === name &&
      node.initializer !== undefined &&
      ts.isObjectLiteralExpression(node.initializer)
    ) {
      found = node.initializer;
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return found as ts.ObjectLiteralExpression | null;
}

/** Whether an options object literal carries an onError. */
function hasOnError(obj: ts.ObjectLiteralExpression, sf: ts.SourceFile): Has {
  let result: Has = "no";
  for (const p of obj.properties) {
    if (p.name !== undefined && p.name.getText(sf) === "onError") return "yes";
    if (!ts.isSpreadAssignment(p)) continue;
    const e = ts.isParenthesizedExpression(p.expression)
      ? p.expression.expression
      : p.expression;
    if (ts.isIdentifier(e) && e.text === "errorsHandledLocally") return "yes";
    if (
      ts.isConditionalExpression(e) &&
      e.whenTrue.getText(sf) === "errorsHandledLocally"
    ) {
      return "param";
    }
    const local = ts.isIdentifier(p.expression)
      ? objectNamed(sf, p.expression.text)
      : null;
    if (local === null) result = "unknown";
    else if (hasOnError(local, sf) === "yes") return "yes";
  }
  return result;
}

function optionsOfUseMutation(node: ts.Node, sf: ts.SourceFile) {
  const out: ts.ObjectLiteralExpression[] = [];
  const visit = (n: ts.Node) => {
    if (
      ts.isCallExpression(n) &&
      n.expression.getText(sf) === "useMutation" &&
      n.arguments[0] !== undefined &&
      ts.isObjectLiteralExpression(n.arguments[0])
    ) {
      out.push(n.arguments[0]);
    }
    ts.forEachChild(n, visit);
  };
  visit(node);
  return out;
}

/** Where each flag-gated hook takes its flag: hook name -> argument index. */
const flagAt = new Map<string, number>();

/** file -> hook name -> whether its mutation has an onError. */
function mutationHooks(files: ts.SourceFile[]) {
  const hooks = new Map<string, Map<string, Has>>();
  for (const sf of files) {
    const visit = (node: ts.Node) => {
      let name: string | null = null;
      let body: ts.Node | undefined;
      let params = 0;
      if (ts.isFunctionDeclaration(node) && node.name) {
        name = node.name.text;
        body = node.body;
        params = node.parameters.length;
      } else if (
        ts.isVariableDeclaration(node) &&
        ts.isIdentifier(node.name) &&
        node.initializer !== undefined &&
        (ts.isArrowFunction(node.initializer) ||
          ts.isFunctionExpression(node.initializer))
      ) {
        name = node.name.text;
        body = node.initializer.body;
        params = node.initializer.parameters.length;
      }
      if (name !== null && /^use[A-Z]/.test(name) && body !== undefined) {
        const opts = optionsOfUseMutation(body, sf);
        if (opts.length === 1 && opts[0]) {
          const byName = hooks.get(sf.fileName) ?? new Map<string, Has>();
          const has = hasOnError(opts[0], sf);
          byName.set(name, has);
          if (has === "param") flagAt.set(`${sf.fileName}#${name}`, params - 1);
          hooks.set(sf.fileName, byName);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return hooks;
}

/** The scanned file an import specifier names, if it is one. */
function resolve(from: string, spec: string): string | null {
  let base: string;
  if (spec.startsWith("@/")) base = `/src/${spec.slice(2)}`;
  else if (spec.startsWith(".")) {
    const parts = from.split("/").slice(0, -1);
    for (const seg of spec.split("/")) {
      if (seg === "..") parts.pop();
      else if (seg !== ".") parts.push(seg);
    }
    base = parts.join("/");
  } else return null;
  for (const ext of [".ts", ".tsx", "/index.ts", "/index.tsx"]) {
    if (`${base}${ext}` in sources) return `${base}${ext}`;
  }
  return null;
}

interface Finding {
  at: string;
  hook: string;
}

function scan() {
  const files = Object.keys(sources).map(parse);
  const hooks = mutationHooks(files);
  const findings: Finding[] = [];
  const rendered: Finding[] = [];
  let callsWithOptions = 0;
  let settled = 0;
  // Calls of a flag-gated hook with a flag the scan cannot read.
  const unjudged = new Set<string>();
  // Reads of a mutation's error that the scan judged, for the floor.
  let errorReads = 0;
  for (const sf of files) {
    // Where each imported name comes from, and what each local holds.
    const imported = new Map<string, string>();
    for (const st of sf.statements) {
      if (
        !ts.isImportDeclaration(st) ||
        !ts.isStringLiteral(st.moduleSpecifier)
      )
        continue;
      const file = resolve(sf.fileName, st.moduleSpecifier.text);
      const named = st.importClause?.namedBindings;
      if (file === null || named === undefined || !ts.isNamedImports(named))
        continue;
      for (const el of named.elements) imported.set(el.name.text, file);
    }
    const fileOf = (callee: string) => imported.get(callee) ?? sf.fileName;
    const hookOf = (callee: string): Has | undefined =>
      hooks.get(fileOf(callee))?.get(callee);
    const locals = new Map<string, { hook: string; has: Has }>();
    const settlers = new Set<string>();
    const at = (node: ts.Node, hook: string): Finding => {
      const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
      return { at: `${sf.fileName}:${String(line + 1)}`, hook };
    };
    const report = (node: ts.Node, hook: string) => {
      findings.push(at(node, hook));
    };
    const visit = (node: ts.Node) => {
      if (
        ts.isVariableDeclaration(node) &&
        node.initializer !== undefined &&
        ts.isCallExpression(node.initializer)
      ) {
        const call = node.initializer;
        const callee = call.expression.getText(sf);
        const arg = call.arguments[0];
        let has =
          callee === "useMutation" &&
          arg !== undefined &&
          ts.isObjectLiteralExpression(arg)
            ? hasOnError(arg, sf)
            : hookOf(callee);
        if (callee === "useSaveOutcome" && ts.isIdentifier(node.name)) {
          settlers.add(node.name.text);
        }
        if (has === "param") {
          // Literal true opts out; absent or literal false does not; any other
          // flag is not judged, and is pinned in the test below.
          const flag =
            call.arguments[flagAt.get(`${fileOf(callee)}#${callee}`) ?? -1];
          if (flag?.kind === ts.SyntaxKind.TrueKeyword) {
            has = "yes";
          } else if (
            flag === undefined ||
            flag.kind === ts.SyntaxKind.FalseKeyword
          ) {
            has = "no";
          } else {
            has = "unknown";
            unjudged.add(`${sf.fileName} ${callee}(${flag.getText(sf)})`);
          }
        }
        if (has !== undefined) {
          if (ts.isIdentifier(node.name)) {
            locals.set(node.name.text, { hook: callee, has });
          } else if (ts.isObjectBindingPattern(node.name)) {
            for (const el of node.name.elements) {
              const prop = (el.propertyName ?? el.name).getText(sf);
              if (prop === "mutate" || prop === "mutateAsync") {
                locals.set(`()${el.name.getText(sf)}`, { hook: callee, has });
              }
              if ((prop === "error" || prop === "isError") && has === "no")
                rendered.push(at(el, callee));
            }
          }
        }
      }
      // const x = cond ? a : b, of two mutations: reported by the toast when
      // either one is.
      if (
        ts.isVariableDeclaration(node) &&
        ts.isIdentifier(node.name) &&
        node.initializer !== undefined &&
        ts.isConditionalExpression(node.initializer)
      ) {
        const [a, b] = [
          node.initializer.whenTrue,
          node.initializer.whenFalse,
        ].map((e) => locals.get(e.getText(sf)));
        if (a !== undefined && b !== undefined) {
          locals.set(node.name.text, a.has === "no" ? a : b);
        }
      }
      // x.error or x.isError: the component renders the failure.
      if (
        ts.isPropertyAccessExpression(node) &&
        (node.name.text === "error" || node.name.text === "isError") &&
        ts.isIdentifier(node.expression)
      ) {
        const m = locals.get(node.expression.text);
        if (m !== undefined && m.has !== "unknown") errorReads++;
        if (m?.has === "no") rendered.push(at(node, m.hook));
      }
      if (ts.isCallExpression(node) && node.arguments[1] !== undefined) {
        const e = node.expression;
        const target =
          ts.isPropertyAccessExpression(e) &&
          (e.name.text === "mutate" || e.name.text === "mutateAsync")
            ? locals.get(e.expression.getText(sf))
            : ts.isIdentifier(e)
              ? locals.get(`()${e.text}`)
              : undefined;
        const opts = node.arguments[1];
        if (target !== undefined && ts.isObjectLiteralExpression(opts)) {
          callsWithOptions++;
          if (target.has === "no" && hasOnError(opts, sf) === "yes") {
            report(node, target.hook);
          }
        }
        // settle(x.mutateAsync(…), { onError }): useSaveOutcome shows the
        // failure, so the hook must be opted out of the toast as well.
        const save = node.arguments[0];
        if (
          ts.isIdentifier(e) &&
          settlers.has(e.text) &&
          save !== undefined &&
          ts.isCallExpression(save) &&
          ts.isPropertyAccessExpression(save.expression) &&
          save.expression.name.text === "mutateAsync" &&
          ts.isObjectLiteralExpression(opts)
        ) {
          const hook = locals.get(save.expression.expression.getText(sf));
          if (hook !== undefined) {
            settled++;
            if (hook.has === "no") report(node, hook.hook);
          }
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  const hookCount = [...hooks.values()].reduce((n, m) => n + m.size, 0);
  return {
    findings,
    rendered,
    errorReads,
    callsWithOptions,
    settled,
    unjudged,
    hookCount,
  };
}

/**
 * Renders a mutation's failure while its hook leaves it to the toast, on
 * purpose or until its owner fixes it: "<file>#<hook>" -> why. An entry the
 * scan no longer finds fails the test, so the list only shrinks.
 */
const SHOWN_AND_TOASTED: Record<string, string> = {};

/** One scan for both tests: it parses every source file. */
let once: ReturnType<typeof scan> | undefined;
const scanned = () => (once ??= scan());

describe("a failed mutation", () => {
  it("is never handed an onError at the call by a hook that has none", () => {
    const { findings, callsWithOptions, settled, unjudged, hookCount } =
      scanned();
    expect(findings).toEqual([]);
    // The one caller whose opt-out is a flag the scan cannot read: it passes
    // `onError !== undefined` and settles only when that holds.
    expect([...unjudged]).toEqual([
      "/src/features/clusters/components/node/NodeShutdownConfirm.tsx useShutdownNode(onError !== undefined)",
    ]);
    // The floor: the scan still finds the hooks and the calls it judges.
    expect(hookCount).toBeGreaterThan(250);
    expect(callsWithOptions).toBeGreaterThan(60);
    expect(settled).toBeGreaterThan(100);
  });

  it("is not rendered by a component while the toast reports it too", () => {
    const { rendered, errorReads } = scanned();
    const found = new Map(
      rendered.map((f) => [`${f.at.split(":")[0] ?? ""}#${f.hook}`, f.at]),
    );
    expect([...found].filter(([key]) => !(key in SHOWN_AND_TOASTED))).toEqual(
      [],
    );
    expect(
      Object.keys(SHOWN_AND_TOASTED).filter((key) => !found.has(key)),
    ).toEqual([]);
    // The floor: the scan still sees the components that render a failure.
    expect(errorReads).toBeGreaterThan(150);
  });
});
