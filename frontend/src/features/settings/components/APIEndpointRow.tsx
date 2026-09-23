import { useId } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { APIEndpoint, APIItems, APIParameter } from "@/types/api";

const METHOD_COLORS: Record<string, string> = {
  GET: "text-emerald-600 border-emerald-300 dark:text-emerald-400 dark:border-emerald-700",
  POST: "text-blue-600 border-blue-300 dark:text-blue-400 dark:border-blue-700",
  PUT: "text-amber-600 border-amber-300 dark:text-amber-400 dark:border-amber-700",
  DELETE: "text-red-600 border-red-300 dark:text-red-400 dark:border-red-700",
  PATCH:
    "text-purple-600 border-purple-300 dark:text-purple-400 dark:border-purple-700",
};

function getMethodColor(method: string): string {
  return METHOD_COLORS[method.toUpperCase()] ?? "text-muted-foreground";
}

/**
 * Where a parameter goes on the wire. Colour-coded because it is the one
 * fact a caller has to get right before anything else works, and because it
 * was undocumented until now — a query-string parameter was indistinguishable
 * from a body one in every version of this page before this.
 */
const SOURCE_COLORS: Record<string, string> = {
  path: "text-violet-600 border-violet-300 dark:text-violet-400 dark:border-violet-700",
  query:
    "text-cyan-600 border-cyan-300 dark:text-cyan-400 dark:border-cyan-700",
  body: "text-orange-600 border-orange-300 dark:text-orange-400 dark:border-orange-700",
};

/**
 * Render a default for display.
 *
 * Only reached when the parameter HAS one — the caller checks for the key's
 * presence, never for a falsy value, because `false` and `0` are defaults a
 * caller must be told about.
 */
function formatDefault(value: unknown): string {
  if (typeof value === "string") return `"${value}"`;
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  // Anything else (null, an object default) goes through JSON so it reads
  // the way the caller would have to send it. JSON.stringify is typed as
  // returning a string but answers undefined for a value it cannot encode;
  // hasDefault has already excluded the only such value that can reach here.
  return JSON.stringify(value);
}

/**
 * Whether the parameter declares a default at all.
 *
 * `"default" in p` rather than `p.default !== undefined`: the server omits
 * the key when there is none, and this is the whole three-state distinction
 * (required / optional-with-a-fallback / optional-and-the-endpoint-decides).
 */
function hasDefault(p: APIParameter): boolean {
  return "default" in p && p.default !== undefined;
}

/**
 * The requirement column's text, which is where the three states land.
 *
 * "Optional" on its own is not a shrug — it means the endpoint does something
 * of its own when the caller says nothing, and the description says what.
 */
function requirementLabel(p: APIParameter): string {
  if (!p.optional) return "Required";
  if (hasDefault(p)) return `Default ${formatDefault(p.default)}`;
  return "Optional";
}

/**
 * The facets a value must satisfy, shared by APIParameter and APIItems.
 *
 * Every bound is read with `!== undefined`, never for truthiness: a
 * `minimum` of 0 is a real floor and `if (p.minimum)` would silently drop
 * it — the same absent-versus-zero trap as `default`.
 */
type Constrained = Pick<
  APIParameter,
  | "type"
  | "format"
  | "pattern"
  | "rule"
  | "minimum"
  | "maximum"
  | "min_length"
  | "max_length"
>;

function constraintLines(c: Constrained): ConstraintLine[] {
  const out: ConstraintLine[] = [];
  // The named rule is NOT one of these lines. Everything constraintLines
  // returns is a facet of the PARAMETER, and the rule is a facet of the
  // rule — which is a different subject, and putting the two in one flat
  // list is what made `up to 40 chars` read as a contradiction of
  // `permits … 2 to 128 characters.` RuleShape renders it in its own
  // scope instead.
  //
  // The exception is a `format` with no rule block to head. That cannot
  // happen from this server — every registered format is catalogued — but
  // a name with nothing behind it is still better than no name at all.
  if (c.format && !c.rule) out.push({ text: `format: ${c.format}` });

  // The length bounds count CHARACTERS on a string and ELEMENTS on an
  // array — the server's own rejection says "must have at least N items"
  // for the latter (checkLength in apischema/validate.go). Saying "chars"
  // for both would misreport the rule a caller has to satisfy.
  const unit = c.type === "array" ? "items" : "chars";

  // Destructured for readability — the four bounds are read several
  // times each. String(...) around every number because the repo's
  // ESLint forbids a bare number in a template literal
  // (restrict-template-expressions).
  const { minimum, maximum, min_length: minLen, max_length: maxLen } = c;
  if (minimum !== undefined && maximum !== undefined) {
    out.push({ text: `${String(minimum)} to ${String(maximum)}` });
  } else if (minimum !== undefined)
    out.push({ text: `min ${String(minimum)}` });
  else if (maximum !== undefined) out.push({ text: `max ${String(maximum)}` });

  if (minLen !== undefined && maxLen !== undefined) {
    out.push({ text: `${String(minLen)}–${String(maxLen)} ${unit}` });
  } else if (minLen !== undefined)
    out.push({ text: `at least ${String(minLen)} ${unit}` });
  else if (maxLen !== undefined)
    out.push({ text: `up to ${String(maxLen)} ${unit}` });

  return out;
}

function EnumChips({ values }: { values: string[] }) {
  return (
    <div className="mt-1 flex flex-wrap items-center gap-1">
      {/* Keyed by index as well as value: nothing in the schema engine
          rejects a duplicated enum entry, and two identical keys is a
          React warning. */}
      {values.map((value, i) => (
        <code
          key={`${value}-${String(i)}`}
          className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]"
        >
          {value}
        </code>
      ))}
    </div>
  );
}

/**
 * One constraint, ready to render. `mono` marks a line that ends in
 * literal syntax — a regex — which is unreadable in a proportional font
 * at this size. The line stays a SINGLE text node either way, so it is
 * still one thing to read and one thing to copy.
 */
interface ConstraintLine {
  text: string;
  mono?: boolean;
}

function ConstraintText({ lines }: { lines: ConstraintLine[] }) {
  if (lines.length === 0) return null;
  return (
    <div className="mt-0.5 text-[11px] text-muted-foreground">
      {lines.map((line) => (
        <div key={line.text} className={line.mono ? "font-mono" : undefined}>
          {line.text}
        </div>
      ))}
    </div>
  );
}

/**
 * The regex to show BESIDE the parameter's own constraints, as opposed to
 * inside the rule block.
 *
 * Only an uncatalogued pattern lands here: it is a regex with no prose and
 * no name, so there is no block to put it in. A catalogued pattern's regex
 * is the same string as `rule.regex` — that is how the rule was found —
 * and a format's regex has never been anywhere but the rule, so both
 * belong to the rule and render there.
 *
 * `||`, not `??`. This is the one place the absent-versus-zero rule that
 * governs `minimum` and `default` does NOT apply: an empty regex is not a
 * constraint that matches nothing, it is no constraint.
 */
function looseRegex(c: Pick<Constrained, "pattern" | "rule">): string {
  return c.rule ? "" : c.pattern || "";
}

/**
 * Whether the parameter publishes a facet of its own that applies ON TOP
 * of its named rule.
 *
 * Nothing here decides whether the facet actually BINDS — a max_length of
 * 200 over a rule capped at 128 narrows nothing — so the line it drives
 * says only that the limits apply as well, which is true either way. The
 * claim to avoid is "this parameter is stricter", which would sometimes be
 * false; the claim to make is that both hold, which is the JSON Schema
 * reading and is always true.
 */
function hasOwnLimits(c: Constrained): boolean {
  return (
    c.minimum !== undefined ||
    c.maximum !== undefined ||
    c.min_length !== undefined ||
    c.max_length !== undefined
  );
}

/**
 * The named rule, in its own scope.
 *
 * It borrows ItemsShape's treatment — a left rule and an indent under a
 * lead-in — because it is the same kind of thing: a named sub-schema whose
 * facets are its own and not the parameter's. That structure is what stops
 * `permits … 2 to 128 characters.` reading as a correction of the
 * `up to 40 chars` line above it. Both are true; they are about different
 * subjects, and the indent says which is which.
 */
function RuleShape({ param }: { param: Constrained }) {
  const { rule } = param;
  if (!rule) return null;
  const lines: ConstraintLine[] = [
    // "format:" or "rule:" rather than one label for both, because the
    // payload distinguishes them and the distinction is real: a format
    // NORMALIZES the value it validates and a pattern never rewrites it.
    { text: `${param.format ? "format" : "rule"}: ${rule.name}` },
    { text: `permits ${rule.permits}` },
  ];
  if (rule.regex) lines.push({ text: `matches ${rule.regex}`, mono: true });
  return (
    <div className="mt-1 border-l pl-2">
      <ConstraintText lines={lines} />
      {hasOwnLimits(param) && (
        <div className="text-[11px] text-muted-foreground">
          {"\u2026and the limits above apply as well"}
        </div>
      )}
    </div>
  );
}

/**
 * Everything about a parameter's VALUE, in one cell: its type, the shape
 * its typetext spells out, and every rule a request has to satisfy.
 *
 * One cell rather than seven columns. A caller reads constraints as prose,
 * and a six- or twelve-column table is unreadable at the `md` breakpoint
 * this switches on. Folding them in beside the type rather than into the
 * description keeps the description free text and gives a caller debugging
 * a 400 one predictable place to look — a pattern is not a remark about
 * the parameter, it is the reason the request was rejected.
 */
function ParameterShape({ param }: { param: APIParameter }) {
  const lines = constraintLines(param);
  const regex = looseRegex(param);
  if (regex) lines.push({ text: `matches ${regex}`, mono: true });
  if (param.requires?.length) {
    lines.push({ text: `send with ${param.requires.join(", ")}` });
  }
  return (
    <>
      <span className="font-mono">{param.type}</span>
      {param.typetext && (
        <div className="font-mono text-[11px] opacity-70">{param.typetext}</div>
      )}
      {param.enum && param.enum.length > 0 && <EnumChips values={param.enum} />}
      <ConstraintText lines={lines} />
      <RuleShape param={param} />
      {param.items && <ItemsShape items={param.items} />}
    </>
  );
}

/** The element schema of an array, nested under its parameter. */
function ItemsShape({ items }: { items: APIItems }) {
  const lines = constraintLines(items);
  const regex = looseRegex(items);
  if (regex) lines.push({ text: `matches ${regex}`, mono: true });
  return (
    <div className="mt-1 border-l pl-2">
      {/* The trailing space is an explicit expression, not a literal: a
          space before a closing tag is what prettier eats when it wraps
          the line, and nothing lints the result. */}
      <span className="text-[11px] text-muted-foreground">{"each item: "}</span>
      <span className="font-mono text-[11px]">{items.type}</span>
      {items.typetext && (
        <span className="ml-1 font-mono text-[11px] opacity-70">
          {items.typetext}
        </span>
      )}
      {items.enum && items.enum.length > 0 && <EnumChips values={items.enum} />}
      <ConstraintText lines={lines} />
      <RuleShape param={items} />
      {items.description && (
        <div className="text-[11px] text-muted-foreground">
          {items.description}
        </div>
      )}
    </div>
  );
}

/** The parameter's name, plus any second name the endpoint also accepts. */
function ParameterName({ param }: { param: APIParameter }) {
  return (
    <>
      <span className="font-mono text-xs font-medium">{param.name}</span>
      {param.alias && (
        <div className="font-mono text-[11px] text-muted-foreground">
          or {param.alias}
        </div>
      )}
    </>
  );
}

function SourceBadge({ source }: { source: APIParameter["source"] }) {
  return (
    <Badge
      variant="outline"
      className={`font-mono text-[11px] ${SOURCE_COLORS[source] ?? "text-muted-foreground"}`}
    >
      {source}
    </Badge>
  );
}

/**
 * The parameter table, shown from `md` up.
 *
 * Below that it is replaced by ParameterList: five columns cannot be read on
 * a phone, and a horizontally scrolling table is worse than a stacked one.
 */
function ParameterTable({ parameters }: { parameters: APIParameter[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead className="w-[18%]">Name</TableHead>
          <TableHead className="w-[26%]">Type &amp; constraints</TableHead>
          <TableHead className="w-[9%]">In</TableHead>
          {/* "Requirement", not "Required": the cell holds one of three
              answers, and two of them are not yes-or-no. */}
          <TableHead className="w-[14%]">Requirement</TableHead>
          <TableHead>Description</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {parameters.map((param) => (
          <TableRow key={param.name}>
            <TableCell className="align-top">
              <ParameterName param={param} />
            </TableCell>
            <TableCell className="break-words align-top text-xs text-muted-foreground">
              <ParameterShape param={param} />
            </TableCell>
            <TableCell className="align-top">
              <SourceBadge source={param.source} />
            </TableCell>
            <TableCell
              className={`align-top text-xs ${param.optional ? "text-muted-foreground" : "font-medium"}`}
            >
              {requirementLabel(param)}
            </TableCell>
            <TableCell className="align-top text-xs text-muted-foreground">
              {param.description}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** The same parameters as a definition list, for widths a table cannot use. */
function ParameterList({ parameters }: { parameters: APIParameter[] }) {
  return (
    <dl className="space-y-3">
      {parameters.map((param) => (
        <div key={param.name} className="space-y-1">
          <dt className="flex flex-wrap items-center gap-2">
            <ParameterName param={param} />
            <SourceBadge source={param.source} />
            <span
              className={`text-[11px] ${param.optional ? "text-muted-foreground" : "font-medium"}`}
            >
              {requirementLabel(param)}
            </span>
          </dt>
          <dd className="break-words text-xs text-muted-foreground">
            {/* The description gets its own BLOCK. Left as a bare
                expression beside ParameterShape it butted straight up
                against that component's leading inline <span>, and every
                parameter rendered as "…lowest free one.integer" — JSX
                emits no whitespace text node between two expressions on
                separate lines, and nothing lints the result. */}
            {param.description && <div>{param.description}</div>}
            <ParameterShape param={param} />
          </dd>
        </div>
      ))}
    </dl>
  );
}

export interface APIEndpointRowProps {
  endpoint: APIEndpoint;
  expanded: boolean;
  onToggle: () => void;
}

/**
 * One endpoint in the catalog, expandable into its request contract.
 *
 * An endpoint with no parameters to show renders as a plain row with no
 * chevron and nothing to open. The payload sends none for an endpoint that
 * declares no parameters, for one of the few that predate the declaration
 * layer and publish no schema, and for every endpoint of an older server that
 * predates the field — and does not say which. So the row claims nothing: a
 * placeholder ("no parameters") would be false for most of the legacy routes
 * and for the file uploads, which read form fields no schema describes, and
 * the missing chevron marks none of the three.
 */
export function APIEndpointRow({
  endpoint,
  expanded,
  onToggle,
}: APIEndpointRowProps) {
  const parameters = endpoint.parameters ?? [];
  const expandable = parameters.length > 0;
  // A disclosure widget names the region it opens. useId rather than the
  // endpoint key: a path carries ':' and '/', which are not valid in the
  // fragment an id is addressed by.
  const panelId = useId();

  const summary = (
    <>
      <Badge
        variant="outline"
        className={`w-16 shrink-0 justify-center font-mono text-xs ${getMethodColor(endpoint.method)}`}
      >
        {endpoint.method}
      </Badge>
      <span className="break-all font-mono text-sm">{endpoint.path}</span>
      <span className="text-sm text-muted-foreground">
        {endpoint.description}
      </span>
      {endpoint.permission && (
        <Badge
          variant="outline"
          className="ml-auto shrink-0 text-xs text-muted-foreground"
        >
          {endpoint.permission}
        </Badge>
      )}
    </>
  );

  return (
    <div className="py-1">
      {expandable ? (
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          aria-controls={expanded ? panelId : undefined}
          className="flex w-full flex-wrap items-center gap-2 rounded py-1.5 text-left hover:bg-muted/50"
        >
          {expanded ? (
            <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
          )}
          {summary}
        </button>
      ) : (
        <div className="flex flex-wrap items-center gap-2 py-1.5">
          {/* Keeps the method badges of expandable and plain rows aligned. */}
          <span className="h-4 w-4 shrink-0" aria-hidden="true" />
          {summary}
        </div>
      )}

      {expandable && expanded && (
        <div
          id={panelId}
          className="ml-6 mt-1 rounded-md border bg-muted/30 p-3"
        >
          <div className="hidden md:block">
            <ParameterTable parameters={parameters} />
          </div>
          <div className="md:hidden">
            <ParameterList parameters={parameters} />
          </div>
        </div>
      )}
    </div>
  );
}
