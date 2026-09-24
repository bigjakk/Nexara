/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import ts from "typescript";
import { Linter } from "eslint";
import tseslint from "typescript-eslint";

/**
 * What the request-sink block in eslint.config.js cannot hold by itself,
 * held by reading the source:
 *
 *   - every eslint-disable of one of its rules — or of no-namespace, which is
 *     what keeps out the one scope where its ambient-declaration check has to
 *     look inside a block — and every blanket eslint-disable, is refused
 *     unless it is on the reviewed list below: a rule that any line can
 *     switch off is only as strong as the review of that line, so each
 *     exemption has to arrive as a diff to this file;
 *   - production code may not reach test code (which the block exempts)
 *     through any specifier that has to be resolved to be judged, through
 *     import.meta.glob, or through a new URL(…, import.meta.url);
 *   - the block has to be the configuration in force, and reach every source
 *     file: the lint script pins it, no other eslint.config.* exists under
 *     frontend/, and src holds no file of an extension it does not lint;
 *   - the type-only imports it allows are erased, which holds while tsconfig
 *     leaves verbatimModuleSyntax off.
 *
 * Directives are read by ESLint itself (SourceCode#getDisableDirectives and
 * #applyInlineConfig), so what counts as one here is exactly what counts as
 * one when the linter runs — a directive quoted inside a string is not one.
 */

const sources: Record<string, string> = import.meta.glob(
  "/src/**/*.{ts,tsx,mts,cts,js,jsx,mjs,cjs}",
  { eager: true, query: "?raw", import: "default" },
);

/** Every file under src, by name only: nothing is loaded. */
const everyFile = Object.keys(import.meta.glob("/src/**/*"));

/** Every ESLint configuration file under frontend/, by name only. */
const eslintConfigs = Object.keys(
  import.meta.glob([
    "/**/eslint.config.{js,mjs,cjs,ts,mts,cts}",
    "!**/node_modules/**",
  ]),
);

const texts: Record<string, string> = import.meta.glob(
  ["/package.json", "/tsconfig.json", "/tsconfig.app.json"],
  { eager: true, query: "?raw", import: "default" },
);

const loaded = import.meta.glob<{ default: Linter.Config[] }>(
  "/eslint.config.js",
  { eager: true },
);

/**
 * The frontend directory, which every specifier is resolved against, and
 * under which the Linter lints files.
 */
const FRONTEND = new URL("../..", import.meta.url).pathname.replace(/\/$/, "");

/** The rules of the committed request-sink block. */
function sinkRules(): string[] {
  const config = loaded["/eslint.config.js"]?.default ?? [];
  const blocks = config.filter(
    (block) => block.rules?.["no-restricted-globals"] !== undefined,
  );
  expect(blocks).toHaveLength(1);
  return Object.keys(blocks[0]?.rules ?? {}).sort();
}

/**
 * The rules whose disabling this refuses: the block's own, and
 * @typescript-eslint/no-namespace. A namespace that is code is the one scope
 * besides a module's where a sink declaration hides the global, and the
 * ambient-declaration check reaches into it — but a namespace can exist only
 * with no-namespace switched off, so that switch is reviewed too.
 */
function watchedRules(): string[] {
  return [...sinkRules(), "@typescript-eslint/no-namespace"].sort();
}

/**
 * The reviewed exemptions: one entry per directive, as
 * "<file> <directive> <rule> -- <its justification>", with "*" for a
 * blanket one. Empty: no source switches a watched rule off. The sanctioned
 * escape for the property rule's documented false positive (an unrelated
 * .fetch() method; see eslint.config.js) is a justified
 * eslint-disable-next-line no-restricted-properties, and it goes here.
 */
const REVIEWED: string[] = [];

/**
 * What this reads from ESLint's SourceCode. Both methods are public, and
 * ESLint's own linting runs on them, but the published SourceCode class type
 * leaves them out (@eslint/core declares them, as optional, on the interface
 * the class implements).
 */
interface DirectiveReader {
  getDisableDirectives(): {
    directives: { type: string; value: string; justification?: string }[];
  };
  applyInlineConfig(): {
    configs: { config: { rules?: Record<string, unknown> } }[];
  };
}

function directiveReader(sourceCode: unknown, file: string): DirectiveReader {
  if (
    typeof sourceCode !== "object" ||
    sourceCode === null ||
    !("getDisableDirectives" in sourceCode) ||
    !("applyInlineConfig" in sourceCode) ||
    typeof sourceCode.getDisableDirectives !== "function" ||
    typeof sourceCode.applyInlineConfig !== "function"
  ) {
    throw new Error(
      `${file} was not linted, or ESLint no longer reads directives this way`,
    );
  }
  return sourceCode as DirectiveReader;
}

interface Directives {
  /** Directives that switch off a watched rule, or every rule. */
  refused: string[];
  /** How many disable directives were read in all. */
  total: number;
}

/** The disable directives and inline rule configs in one file. */
function directivesIn(file: string, text: string, rules: string[]): Directives {
  const linter = new Linter({ cwd: FRONTEND });
  const messages = linter.verify(
    text,
    [
      {
        // A pattern naming the extensions: one that names every file ("**/*")
        // does not by itself make a file one the Linter lints.
        files: ["**/*.{ts,tsx,mts,cts,js,jsx,mjs,cjs}"],
        languageOptions: {
          parser: tseslint.parser,
          parserOptions: { ecmaFeatures: { jsx: /x$/.test(file) } },
        },
      },
    ],
    { filename: `${FRONTEND}${file}` },
  );
  expect(messages.filter((m) => m.fatal === true)).toEqual([]);
  // Unknown on purpose: typed non-null, it is null for a file the Linter
  // did not lint.
  const linted: unknown = linter.getSourceCode();
  const sourceCode = directiveReader(linted, file);

  const refused: string[] = [];
  let total = 0;
  for (const directive of sourceCode.getDisableDirectives().directives) {
    if (directive.type === "enable") {
      continue;
    }
    total++;
    const named = directive.value
      .split(",")
      .map((rule) => rule.trim())
      .filter((rule) => rule !== "");
    const justification = (directive.justification ?? "").trim();
    if (named.length === 0) {
      refused.push(`${file} eslint-${directive.type} * -- ${justification}`);
    }
    for (const rule of named.filter((r) => rules.includes(r))) {
      refused.push(
        `${file} eslint-${directive.type} ${rule} -- ${justification}`,
      );
    }
  }
  // /* eslint <rule>: off */ switches a rule off for the whole file.
  for (const { config } of sourceCode.applyInlineConfig().configs) {
    for (const rule of Object.keys(config.rules ?? {})) {
      if (rules.includes(rule)) {
        refused.push(`${file} eslint ${rule} -- inline configuration`);
      }
    }
  }
  return { refused, total };
}

/** The directives of every file in `files`. */
function scanDirectives(
  files: Record<string, string>,
  rules: string[],
): Directives {
  const refused: string[] = [];
  let total = 0;
  for (const [file, text] of Object.entries(files)) {
    // Every directive names "eslint" in its label, and a comment cannot
    // spell it any other way, so a file without it holds none.
    if (!text.includes("eslint")) {
      continue;
    }
    const found = directivesIn(file, text, rules);
    refused.push(...found.refused);
    total += found.total;
  }
  return { refused: refused.sort(), total };
}

function isTestSource(file: string): boolean {
  return file.startsWith("/src/test/") || /\.test\.[cm]?[jt]sx?$/.test(file);
}

/** Test code, as a resolved path: the test tree, or a test module. */
function isTestCode(target: string): boolean {
  const tree = `${FRONTEND}/src/test`;
  return (
    target === tree ||
    target.startsWith(`${tree}/`) ||
    /\.test(?:\.[cm]?[jt]sx?)?$/.test(target)
  );
}

/** An absolute path with its "." and ".." segments resolved. */
function normalize(absolute: string): string {
  const out: string[] = [];
  for (const segment of absolute.split("/")) {
    if (segment === "" || segment === ".") {
      continue;
    }
    if (segment === "..") {
      out.pop();
    } else {
      out.push(segment);
    }
  }
  return `/${out.join("/")}`;
}

/**
 * Where a specifier in `file` points, as a real path — the @/ alias mapped to
 * src (tsconfig's paths), a relative one against the file's directory, a
 * root-absolute one against frontend/, Vite's root — or null for a package.
 * Resolved against the real directory, so a specifier that climbs out of the
 * project and back in lands where it really does.
 */
function resolveSpecifier(file: string, spec: string): string | null {
  if (spec.startsWith("@/")) {
    return normalize(`${FRONTEND}/src/${spec.slice(2)}`);
  }
  if (/^\.\.?\//.test(spec)) {
    const dir = `${FRONTEND}${file}`.replace(/\/[^/]*$/, "");
    return normalize(`${dir}/${spec}`);
  }
  if (spec.startsWith("/")) {
    return normalize(`${FRONTEND}${spec}`);
  }
  return null;
}

/** What one file reaches at runtime. */
interface Reach {
  /** Specifiers of imports, re-exports, dynamic imports, import = require. */
  specifiers: string[];
  /** String literals given to new URL(…, import.meta.url). */
  urls: string[];
  /** How many import.meta.glob calls the file makes. */
  globs: number;
}

function isImportMeta(node: ts.Node): boolean {
  return (
    ts.isMetaProperty(node) &&
    node.keywordToken === ts.SyntaxKind.ImportKeyword &&
    node.name.text === "meta"
  );
}

/**
 * What a file reaches at runtime, leaving out the imports the compiler
 * erases: import type, export type, and an import whose every name is a
 * type (erased while verbatimModuleSyntax is off; see its test below).
 */
function runtimeReach(file: string, text: string): Reach {
  const sf = ts.createSourceFile(
    file,
    text,
    ts.ScriptTarget.Latest,
    true,
    /x$/.test(file) ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const reach: Reach = { specifiers: [], urls: [], globs: 0 };
  const allTypes = (elements: readonly { isTypeOnly: boolean }[]) =>
    elements.length > 0 && elements.every((e) => e.isTypeOnly);
  const visit = (node: ts.Node): void => {
    let spec: ts.Expression | undefined;
    let typeOnly = false;
    if (ts.isImportDeclaration(node)) {
      spec = node.moduleSpecifier;
      const clause = node.importClause;
      typeOnly =
        clause !== undefined &&
        (clause.phaseModifier === ts.SyntaxKind.TypeKeyword ||
          (clause.name === undefined &&
            clause.namedBindings !== undefined &&
            ts.isNamedImports(clause.namedBindings) &&
            allTypes(clause.namedBindings.elements)));
    } else if (ts.isExportDeclaration(node)) {
      spec = node.moduleSpecifier;
      typeOnly =
        node.isTypeOnly ||
        (node.exportClause !== undefined &&
          ts.isNamedExports(node.exportClause) &&
          allTypes(node.exportClause.elements));
    } else if (
      ts.isCallExpression(node) &&
      node.expression.kind === ts.SyntaxKind.ImportKeyword
    ) {
      spec = node.arguments[0];
    } else if (
      ts.isImportEqualsDeclaration(node) &&
      ts.isExternalModuleReference(node.moduleReference)
    ) {
      spec = node.moduleReference.expression;
      typeOnly = node.isTypeOnly;
    } else if (
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression) &&
      isImportMeta(node.expression.expression) &&
      node.expression.name.text.startsWith("glob")
    ) {
      reach.globs++;
    } else if (
      ts.isNewExpression(node) &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === "URL"
    ) {
      const [first, base] = node.arguments ?? [];
      if (
        first !== undefined &&
        ts.isStringLiteralLike(first) &&
        base !== undefined &&
        ts.isPropertyAccessExpression(base) &&
        isImportMeta(base.expression) &&
        base.name.text === "url"
      ) {
        reach.urls.push(first.text);
      }
    }
    if (spec !== undefined && ts.isStringLiteralLike(spec) && !typeOnly) {
      reach.specifiers.push(spec.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return reach;
}

interface ReachScan {
  refused: string[];
  /** Specifiers resolved to a path: the floor below. */
  resolved: number;
}

/** What production code in `files` reaches of the test code. */
function testCodeReached(files: Record<string, string>): ReachScan {
  const refused: string[] = [];
  let resolved = 0;
  for (const [file, text] of Object.entries(files)) {
    if (isTestSource(file)) {
      continue;
    }
    const reach = runtimeReach(file, text);
    for (const spec of reach.specifiers) {
      const target = resolveSpecifier(file, spec);
      if (target === null) {
        continue;
      }
      resolved++;
      if (isTestCode(target)) {
        refused.push(`${file} imports ${spec}`);
      }
    }
    for (const url of reach.urls) {
      const target = resolveSpecifier(file, url);
      if (target !== null && isTestCode(target)) {
        refused.push(`${file} builds a URL to ${url}`);
      }
    }
    // What a glob matches is Vite's to decide, and one broad enough reaches
    // the test tree and every *.test.* module: none is used in production
    // code, so a first use is reviewed here.
    if (reach.globs > 0) {
      refused.push(`${file} calls import.meta.glob`);
    }
  }
  return { refused, resolved };
}

/** The extensions the request-sink block lints, and the files that are data. */
const LINTED = ["ts", "tsx", "mts", "cts"];
const DATA = ["css", "json", "md"];

/** Files under src of an extension that is neither linted nor data. */
function unlintedFiles(files: string[]): string[] {
  return files.filter((file) => {
    const extension = /\.([^./]+)$/.exec(file)?.[1] ?? "";
    return !LINTED.includes(extension) && !DATA.includes(extension);
  });
}

/** ESLint configurations other than the committed one. */
function strayConfigs(files: string[]): string[] {
  return files.filter((file) => file !== "/eslint.config.js");
}

/** A tsconfig's compilerOptions, read the way tsc reads the file. */
function compilerOptions(file: string): Record<string, unknown> {
  const text = texts[file];
  if (text === undefined) throw new Error(`${file} is missing`);
  const parsed = ts.parseConfigFileTextToJson(file, text);
  expect(parsed.error).toBeUndefined();
  const config = parsed.config as { compilerOptions?: Record<string, unknown> };
  return config.compilerOptions ?? {};
}

describe("the source the request-sink rules cannot see", () => {
  it("switches no watched rule off, and no rule off wholesale, beyond the reviewed list", () => {
    // The block's four rules, read from the committed config, and the one
    // that keeps namespaces out.
    expect(watchedRules()).toEqual([
      "@typescript-eslint/no-namespace",
      "@typescript-eslint/no-restricted-imports",
      "no-restricted-globals",
      "no-restricted-properties",
      "no-restricted-syntax",
    ]);
    const { refused, total } = scanDirectives(sources, watchedRules());
    expect(refused).toEqual([...REVIEWED].sort());
    // Anti-vacuity: the tree holds some two dozen disable directives for
    // other rules (react-hooks/exhaustive-deps and the like; 27 when this
    // was written), so a scan that read none could not agree with the list
    // above. A floor rather than the count, so that an unrelated disable
    // added elsewhere does not fail a test about these rules.
    expect(total).toBeGreaterThan(20);
  });

  it("finds a watched directive whatever form it takes, file by file", () => {
    // Each file holds one form and nothing else, so a scan that skipped a
    // file for lacking some other form would lose exactly that one.
    const planted: Record<string, string> = {
      "/src/a.ts": "/* eslint-disable no-restricted-globals */\nvoid 0;",
      "/src/b.ts": '/* eslint no-restricted-properties: "off" */\nvoid 0;',
      "/src/c.ts":
        "void 0; // eslint-disable-line @typescript-eslint/no-restricted-imports",
      "/src/d.ts":
        "// eslint-disable-next-line @typescript-eslint/no-namespace -- a probe\nnamespace Probe {}",
      "/src/e.ts": "export const clean = true;",
    };
    expect(scanDirectives(planted, watchedRules())).toEqual({
      refused: [
        "/src/a.ts eslint-disable no-restricted-globals -- ",
        "/src/b.ts eslint no-restricted-properties -- inline configuration",
        "/src/c.ts eslint-disable-line @typescript-eslint/no-restricted-imports -- ",
        "/src/d.ts eslint-disable-next-line @typescript-eslint/no-namespace -- a probe",
      ],
      total: 3,
    });
  });

  it.each([
    [
      "a disable of a sink rule on the next line",
      '// eslint-disable-next-line no-restricted-globals -- "just this once"\nvoid fetch("/x");',
      [
        '/src/p.ts eslint-disable-next-line no-restricted-globals -- "just this once"',
      ],
    ],
    [
      "a disable of a sink rule on its own line",
      'void window.fetch("/x"); // eslint-disable-line no-restricted-properties',
      ["/src/p.ts eslint-disable-line no-restricted-properties -- "],
    ],
    [
      "a disable of a sink rule for the rest of the file",
      "/* eslint-disable no-restricted-syntax, react-hooks/exhaustive-deps */",
      ["/src/p.ts eslint-disable no-restricted-syntax -- "],
    ],
    [
      "a disable of the import rule",
      '// eslint-disable-next-line @typescript-eslint/no-restricted-imports\nimport "@/test/test-utils";',
      [
        "/src/p.ts eslint-disable-next-line @typescript-eslint/no-restricted-imports -- ",
      ],
    ],
    [
      "a blanket disable",
      "/* eslint-disable */",
      ["/src/p.ts eslint-disable * -- "],
    ],
    [
      "a blanket disable of the next line",
      "// eslint-disable-next-line\nvoid 0;",
      ["/src/p.ts eslint-disable-next-line * -- "],
    ],
    [
      "an inline configuration switching a sink rule off",
      '/* eslint no-restricted-globals: "off" */',
      ["/src/p.ts eslint no-restricted-globals -- inline configuration"],
    ],
    [
      "a disable of any other rule",
      "// eslint-disable-next-line react-hooks/exhaustive-deps -- reset on open only\nvoid 0;",
      [],
    ],
    [
      "a directive quoted inside a string",
      'export const s = "// eslint-disable-next-line no-restricted-globals";',
      [],
    ],
    ["an enable directive", "/* eslint-enable no-restricted-globals */", []],
  ])("refuses exactly what it should: %s", (_name, text, want) => {
    expect(directivesIn("/src/p.ts", text, watchedRules()).refused).toEqual(
      want,
    );
  });

  it("lets no production code reach test code", () => {
    const { refused, resolved } = testCodeReached(sources);
    expect(refused).toEqual([]);
    // Anti-vacuity: the tree resolves well over a thousand specifiers
    // (aliases and relative ones alike), so a scan that resolved none could
    // not agree with the list above.
    expect(resolved).toBeGreaterThan(1000);
  });

  it("resolves every specifier before deciding, and lets a type-only import be", () => {
    // Up out of src, out of frontend/ itself, and back in by its own name.
    const project = FRONTEND.replace(/^.*\//, "");
    const planted: Record<string, string> = {
      "/src/features/planted/a.ts":
        'import { x } from "../../test/test-utils";',
      "/src/lib/b.ts": 'import "../test/setup";',
      "/src/c.tsx": 'import "./test/test-utils";',
      "/src/features/planted/d.ts":
        'export const m = import("../../test/test-utils");',
      "/src/features/planted/e.ts":
        'export { renderWithProviders } from "../../test/test-utils";',
      "/src/features/planted/f.ts": 'import "/src/test/test-utils";',
      // The bare directory, which a bundler resolves to its index.
      "/src/lib/g.ts": 'import "../test";',
      // An alias with a dot segment, and a climb out and back in.
      "/src/features/planted/h.ts": 'import "@/features/../test/test-utils";',
      "/src/features/planted/i.ts": `import "../../../../${project}/src/test/test-utils";`,
      // The alias itself, and a test module reached by a path.
      "/src/features/planted/j.ts": 'import "@/test/test-utils";',
      "/src/features/planted/k.ts": 'import "./widget.test";',
      // A URL built from the module's own, and any import.meta.glob.
      "/src/features/planted/l.ts":
        'export const w = new URL("../../test/worker.ts", import.meta.url);',
      "/src/features/planted/m.ts":
        'export const all = import.meta.glob("../../test/*.tsx", { eager: true });',
      // A production folder named test is not the test tree.
      "/src/features/planted/n.ts": 'import "./test/fixtures";',
      "/src/features/planted/o.ts":
        'export const w = new URL("./worker.ts", import.meta.url);',
      // Erased by the compiler.
      "/src/features/planted/p.ts":
        'import type { RenderOptions } from "../../test/test-utils";',
      "/src/features/planted/q.ts":
        'import { type RenderOptions } from "@/test/test-utils";',
      "/src/features/planted/r.ts":
        'export type { RenderOptions } from "../../test/test-utils";',
      // A test file may reach anything.
      "/src/features/planted/s.test.ts": 'import "../../test/test-utils";',
    };
    expect(testCodeReached(planted).refused).toEqual([
      "/src/features/planted/a.ts imports ../../test/test-utils",
      "/src/lib/b.ts imports ../test/setup",
      "/src/c.tsx imports ./test/test-utils",
      "/src/features/planted/d.ts imports ../../test/test-utils",
      "/src/features/planted/e.ts imports ../../test/test-utils",
      "/src/features/planted/f.ts imports /src/test/test-utils",
      "/src/lib/g.ts imports ../test",
      "/src/features/planted/h.ts imports @/features/../test/test-utils",
      `/src/features/planted/i.ts imports ../../../../${project}/src/test/test-utils`,
      "/src/features/planted/j.ts imports @/test/test-utils",
      "/src/features/planted/k.ts imports ./widget.test",
      "/src/features/planted/l.ts builds a URL to ../../test/worker.ts",
      "/src/features/planted/m.ts calls import.meta.glob",
    ]);
  });

  it("erases what it treats as type-only: tsconfig leaves verbatimModuleSyntax off", () => {
    // With it on, import { type X } from "m" compiles to import {} from "m",
    // which runs the module; the type-only allowance above, and the lint
    // rule's allowTypeImports, would then let test code in.
    for (const file of ["/tsconfig.json", "/tsconfig.app.json"]) {
      expect(compilerOptions(file)["verbatimModuleSyntax"]).not.toBe(true);
    }
    // Positive control: the reader does see the option where it is set.
    const withIt = ts.parseConfigFileTextToJson(
      "x.json",
      '{ "compilerOptions": { /* c */ "verbatimModuleSyntax": true } }',
    ).config as { compilerOptions: Record<string, unknown> };
    expect(withIt.compilerOptions["verbatimModuleSyntax"]).toBe(true);
  });

  it("holds no source of an extension the block does not lint", () => {
    expect(unlintedFiles(everyFile)).toEqual([]);
    // Anti-vacuity, and a positive control.
    expect(everyFile.length).toBeGreaterThan(600);
    expect(
      unlintedFiles([
        "/src/a.js",
        "/src/b.mjs",
        "/src/c.jsx",
        "/src/d.vue",
        "/src/e.ts",
        "/src/f.mts",
        "/src/g.css",
        "/src/Makefile",
      ]),
    ).toEqual([
      "/src/a.js",
      "/src/b.mjs",
      "/src/c.jsx",
      "/src/d.vue",
      "/src/Makefile",
    ]);
  });

  it("reads every extension the block lints, as the path guard does", () => {
    // The block's reach, pinned exactly in request-sinks.lint.test.ts, is
    // the extension list here, and the path guard globs the same list.
    const config = loaded["/eslint.config.js"]?.default ?? [];
    const block = config.find(
      (b) => b.rules?.["no-restricted-globals"] !== undefined,
    );
    expect(block?.files).toEqual([`src/**/*.{${LINTED.join(",")}}`]);
    expect(sources["/src/lib/api-path.guard.test.ts"]).toContain(
      `"/src/**/*.{${LINTED.join(",")}}"`,
    );
  });

  it("has one ESLint configuration, and the lint script pins it", () => {
    expect(eslintConfigs).toEqual(["/eslint.config.js"]);
    expect(
      strayConfigs(["/eslint.config.js", "/src/eslint.config.ts"]),
    ).toEqual(["/src/eslint.config.ts"]);
    // ESLint 10 looks for eslint.config.* from each file's directory
    // upward; -c names the one file used for every file instead.
    const pkg = JSON.parse(texts["/package.json"] ?? "{}") as {
      scripts?: Record<string, string>;
    };
    expect(pkg.scripts?.["lint"]).toMatch(/^eslint -c eslint\.config\.js /);
  });
});
