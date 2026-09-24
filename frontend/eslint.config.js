import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

const FETCH_MESSAGE =
  "Send it with apiClient or apiFetch (src/lib/api-client.ts), which check the path with assertApiPath first.";
const XHR_MESSAGE =
  "Open it with openApiRequest (src/lib/api-client.ts), which checks the path with assertApiPath first.";
const OTHER_SINK_MESSAGE =
  "It sends a request past src/lib/api-client.ts, where every path is checked with assertApiPath. Use apiClient or apiFetch, or have this reviewed and add it there.";
// The request APIs a module must not declare for itself, and the ones it must
// not alias with import =; see no-restricted-syntax below.
const SINK_NAMES = "/^(?:fetch|fetchLater|XMLHttpRequest|EventSource)$/";
const SINK_ALIAS_NAMES =
  "/^(?:fetch|fetchLater|XMLHttpRequest|EventSource|sendBeacon)$/";
// A dynamic import() specifier naming test code: the test tree by its alias,
// or a test module by its name.
const TEST_SPECIFIER = "/^@\\/test(?:\\/|$)|\\.test(?:\\.[cm]?[jt]sx?)?$/";
const TEST_TREE_MESSAGE =
  "Test code is exempt from the request-sink rules; production code must not import it.";

export default tseslint.config(
  { ignores: ["dist", "postcss.config.js", "vite-env.d.ts"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.strictTypeChecked],
    files: ["**/*.{ts,tsx,mts,cts}"],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
      parserOptions: {
        project: ["./tsconfig.app.json", "./tsconfig.node.json"],
        tsconfigRootDir: import.meta.dirname,
      },
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      // eslint-plugin-react-hooks v7's "recommended" preset now bundles the
      // React Compiler diagnostics (set-state-in-effect, refs, immutability,
      // etc.), which flag ~75 pre-existing call sites. Keep the classic
      // two-rule behavior here so the bump stays a dependency change; adopting
      // the compiler rules is a separate, deliberate code-quality effort.
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
    },
  },
  {
    // Every fetch and XMLHttpRequest the SPA makes goes through
    // src/lib/api-client.ts — request(), apiFetch(), openApiRequest() — which
    // re-checks the path with assertApiPath before anything is sent, and is
    // the only code that attaches the stored access token to a request
    // (src/lib/api-path.ts has why). These rules refuse the request APIs anywhere else, by reference
    // rather than by spelling: no-restricted-globals follows scope, so
    // fetch.call(...), `const f = fetch` and XMLHttpRequest.prototype are all
    // reported and a local named fetch is not; no-restricted-properties
    // reports the names read off ANY object — window.fetch, self["fetch"],
    // `const { fetch } = window`, navigator.sendBeacon.
    //
    // Any object, deliberately, rather than window, self and globalThis: the
    // rule can name an object only by its identifier, so top.fetch,
    // parent.fetch and iframe.contentWindow.fetch would go unseen. The cost is
    // a false positive on an unrelated method of the same name — TanStack
    // Query's query.fetch(), a class's own this.fetch() — where the escape is
    // an eslint-disable-next-line comment saying why the call is no request.
    //
    // fetchLater takes a RequestInit, so it can carry an Authorization
    // header; EventSource and sendBeacon send requests too. None is used
    // today, and refusing them puts a first use in front of a reviewer.
    //
    // no-restricted-syntax closes the hole a scope-following rule has: an
    // ambient declaration — declare function fetch(…), declare const
    // XMLHttpRequest — makes the name a binding of the scope it sits in, so
    // references to it there are no longer the global's and
    // no-restricted-globals says nothing, while the declaration emits no code
    // and every call still reaches the global. That holds at a module's top
    // level and inside a namespace that is code (namespace N { … } — a
    // namespace the source can only have with no-namespace switched off,
    // which request-sinks.source.test.ts refuses too). TypeScript 6's DOM
    // library has no fetchLater, so such a declaration is exactly how its
    // first use would be written. Only inside a declare block does it hide
    // nothing: declare module "x" { … } and declare namespace N { … }
    // describe code that lives elsewhere and hold none that runs, and
    // declare global { … } IS the global, whose every use the rules above
    // still report. The rule also refuses an overload signature named after
    // one of these outside such a block, which only a local function that
    // shadows the global would have.
    //
    // It refuses an import = alias of one as well: import f =
    // globalThis.fetch compiles (globalThis is a namespace to TypeScript;
    // window and navigator are not) and emits const f = globalThis.fetch,
    // yet it is neither a member access nor a reference to the global name.
    //
    // NOT seen, and so owned by nothing here: a name computed at runtime
    // (globalThis[name], Reflect.get(window, "fetch"), a dynamic import()
    // with a computed specifier); a prototype reached
    // through an instance (Object.getPrototypeOf(xhr).open, which also gets
    // past the refusing open() an opened request carries); a form's
    // submit(); new WebSocket, which the event hub, the console and VNC
    // open on /ws paths rather than API paths; a binding imported from a
    // package that sends requests itself; and every URL the browser loads by
    // itself — an <img src>, a <link href>, window.open, location.href — of
    // which rules 1 and 4 of src/lib/api-path.guard.test.ts hold only those
    // whose fixed text shows "/api/".
    //
    // Tests and src/test/** are exempt: they stub fetch on purpose. So
    // production code must not import them, or a request could ride in
    // through the import. @typescript-eslint/no-restricted-imports refuses
    // the two spellings that are unambiguous from the specifier alone — the
    // test tree by its alias (@/test/…) and a test module by its name
    // (….test, ….test.ts) — and no-restricted-syntax refuses the same two in
    // a dynamic import() whose specifier is a literal, a template literal
    // with no substitution included. A type-only import is allowed: it is
    // erased, so nothing can ride in — true while tsconfig leaves
    // verbatimModuleSyntax off, which request-sinks.source.test.ts pins. A
    // specifier that has to be resolved to be judged — a relative one, an
    // alias with a dot segment, one climbing out of src and back — is left
    // to request-sinks.source.test.ts, which resolves every specifier against
    // the importing file; it also refuses import.meta.glob in production
    // code, a new URL(…, import.meta.url) into test code, an eslint.config.*
    // other than this one, a source file of an extension this block does
    // not lint, and a new eslint-disable of any rule in this block.
    //
    // The lint script pins this file with -c (package.json): ESLint 10 looks
    // for eslint.config.* from each file's own directory upward, so without
    // the pin a config dropped into src would replace this one for its
    // subtree.
    //
    // src/lib/request-sinks.lint.test.ts runs this block over each spelling.
    files: ["src/**/*.{ts,tsx,mts,cts}"],
    ignores: [
      "src/lib/api-client.ts",
      "src/**/*.test.{ts,tsx,mts,cts}",
      "src/test/**",
    ],
    rules: {
      "no-restricted-globals": [
        "error",
        { name: "fetch", message: FETCH_MESSAGE },
        { name: "XMLHttpRequest", message: XHR_MESSAGE },
        { name: "fetchLater", message: OTHER_SINK_MESSAGE },
        { name: "EventSource", message: OTHER_SINK_MESSAGE },
      ],
      "no-restricted-properties": [
        "error",
        { property: "fetch", message: FETCH_MESSAGE },
        { property: "XMLHttpRequest", message: XHR_MESSAGE },
        { property: "fetchLater", message: OTHER_SINK_MESSAGE },
        { property: "EventSource", message: OTHER_SINK_MESSAGE },
        { property: "sendBeacon", message: OTHER_SINK_MESSAGE },
      ],
      "no-restricted-syntax": [
        "error",
        {
          selector: `:matches(TSDeclareFunction, ClassDeclaration[declare=true], VariableDeclaration[declare=true] > VariableDeclarator)[id.name=${SINK_NAMES}]:not(TSModuleDeclaration[declare=true] *)`,
          message:
            "A declared request API hides the global from the rules above, while every call still reaches it. Use apiClient, apiFetch or openApiRequest (src/lib/api-client.ts).",
        },
        {
          selector: `TSImportEqualsDeclaration TSQualifiedName[right.name=${SINK_ALIAS_NAMES}]`,
          message:
            "An import = alias of a request API reaches the global past the rules above. Use apiClient, apiFetch or openApiRequest (src/lib/api-client.ts).",
        },
        {
          selector: `:matches(ImportExpression[source.value=${TEST_SPECIFIER}], ImportExpression[source.type="TemplateLiteral"][source.expressions.length=0][source.quasis.0.value.cooked=${TEST_SPECIFIER}])`,
          message: TEST_TREE_MESSAGE,
        },
      ],
      "@typescript-eslint/no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              regex: "^@/test(?:/|$)",
              allowTypeImports: true,
              message: TEST_TREE_MESSAGE,
            },
            {
              regex: "\\.test(?:\\.[cm]?[jt]sx?)?$",
              allowTypeImports: true,
              message: TEST_TREE_MESSAGE,
            },
          ],
        },
      ],
    },
  },
  {
    files: ["vite.config.ts", "vitest.config.ts"],
    rules: {
      "@typescript-eslint/no-unsafe-call": "off",
      "@typescript-eslint/no-unsafe-assignment": "off",
      "@typescript-eslint/no-unsafe-member-access": "off",
    },
  },
);
