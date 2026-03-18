import type {
  FilterCriteria,
  InventoryRow,
  ParsedQuery,
  ResourceType,
} from "../types/inventory";

const VALID_FIELDS = new Set([
  "type",
  "status",
  "cluster",
  "node",
  "name",
  "tags",
  "pool",
  "ha",
  "template",
  "cpu",
  "mem",
  "vmid",
  "cpus",
  "cores",
  "uptime",
]);

const DURATION_MULTIPLIERS: Record<string, number> = {
  s: 1,
  m: 60,
  h: 3600,
  d: 86400,
  w: 604800,
};

/**
 * Tokenize a query string, respecting quoted values within field:value pairs.
 * e.g. `name:"my server" type:vm hello` → [`name:"my server"`, `type:vm`, `hello`]
 */
function tokenize(query: string): string[] {
  const tokens: string[] = [];
  const regex = /(?:[^\s"]*"[^"]*"[^\s"]*|[^\s]+)/g;
  let match: RegExpExecArray | null;
  while ((match = regex.exec(query)) !== null) {
    tokens.push(match[0]);
  }
  return tokens;
}

/** Strip surrounding double quotes from a string. */
function stripQuotes(s: string): string {
  if (s.startsWith('"') && s.endsWith('"') && s.length >= 2) {
    return s.slice(1, -1);
  }
  return s;
}

/**
 * Parse a search query string into structured filters.
 *
 * Supported syntax:
 * - `field:value`        — equality match (e.g. `type:vm`, `status:running`)
 * - `field:val1,val2`    — match any value (e.g. `status:running,paused`)
 * - `!field:value`       — negated match (e.g. `!status:stopped`)
 * - `field>N%`           — greater-than comparison (e.g. `cpu>80%`)
 * - `field<N%`           — less-than comparison (e.g. `mem<50%`)
 * - `uptime>1d`          — duration comparison (s/m/h/d/w suffixes)
 * - `field:"quoted val"` — quoted values with spaces
 * - bare text            — fuzzy search across name, cluster, node, vmid
 */
export function parseQuery(query: string): ParsedQuery {
  const filters: FilterCriteria[] = [];
  const freeTextParts: string[] = [];

  const tokens = tokenize(query);

  for (const rawToken of tokens) {
    // Check for negation prefix on field:value filters
    const isNegated = rawToken.startsWith("!");
    const token = isNegated ? rawToken.slice(1) : rawToken;

    // field>N or field<N with optional suffix (%, s, m, h, d, w)
    const compMatch = /^([a-z]+)([><])(\d+(?:\.\d+)?)(s|m|h|d|w|%)?$/i.exec(
      token,
    );
    if (compMatch?.[1] && compMatch[2] && compMatch[3]) {
      const field = compMatch[1].toLowerCase();
      if (VALID_FIELDS.has(field)) {
        let numValue = parseFloat(compMatch[3]);
        const suffix = compMatch[4]?.toLowerCase();

        // Convert duration suffixes to seconds
        if (suffix && suffix !== "%" && DURATION_MULTIPLIERS[suffix] != null) {
          numValue = numValue * DURATION_MULTIPLIERS[suffix];
        }

        filters.push({
          field,
          operator: compMatch[2] === ">" ? "gt" : "lt",
          value: String(numValue),
        });
        continue;
      }
    }

    // field:value or field:"quoted value"
    const kvMatch = /^([a-z]+):(.+)$/i.exec(token);
    if (kvMatch?.[1] && kvMatch[2]) {
      const field = kvMatch[1].toLowerCase();
      if (VALID_FIELDS.has(field)) {
        const rawValue = stripQuotes(kvMatch[2]);
        filters.push({
          field,
          operator: isNegated ? "neq" : "eq",
          value: rawValue.toLowerCase(),
        });
        continue;
      }
    }

    // Free text (include original token, not stripped)
    freeTextParts.push(rawToken);
  }

  return {
    filters,
    freeText: freeTextParts.join(" ").toLowerCase(),
  };
}

function normalizeStatus(status: string): string {
  return status.toLowerCase();
}

function normalizeType(type: string): ResourceType | null {
  const map: Record<string, ResourceType> = {
    vm: "vm",
    qemu: "vm",
    ct: "ct",
    lxc: "ct",
    container: "ct",
    node: "node",
  };
  return map[type.toLowerCase()] ?? null;
}

function getFieldValue(
  row: InventoryRow,
  field: string,
): string | number | null {
  switch (field) {
    case "type":
      return row.type;
    case "status":
      return normalizeStatus(row.status);
    case "cluster":
      return row.clusterName.toLowerCase();
    case "node":
      return row.nodeName.toLowerCase();
    case "name":
      return row.name.toLowerCase();
    case "tags":
      return row.tags.toLowerCase();
    case "pool":
      return row.pool.toLowerCase();
    case "ha":
      return row.haState.toLowerCase();
    case "template":
      return row.template ? "true" : "false";
    case "cpu":
      return row.cpuPercent;
    case "mem":
      return row.memPercent;
    case "vmid":
      return row.vmid;
    case "cpus":
    case "cores":
      return row.cpuCount;
    case "uptime":
      return row.uptime;
    default:
      return null;
  }
}

function matchFilter(row: InventoryRow, filter: FilterCriteria): boolean {
  const { field, operator, value } = filter;

  if (operator === "eq" || operator === "neq") {
    const values = value.split(",");

    // Special handling for type aliases
    if (field === "type") {
      const matches = values.some((v) => {
        const normalized = normalizeType(v);
        return normalized !== null && row.type === normalized;
      });
      return operator === "eq" ? matches : !matches;
    }

    const fieldValue = getFieldValue(row, field);
    if (fieldValue === null) return operator === "neq";

    let matches: boolean;
    if (typeof fieldValue === "number") {
      matches = values.some((v) => fieldValue === Number(v));
    } else {
      matches = values.some((v) => fieldValue.includes(v));
    }
    return operator === "eq" ? matches : !matches;
  }

  // gt / lt — numeric comparison
  const fieldValue = getFieldValue(row, field);
  if (fieldValue === null || typeof fieldValue !== "number") return false;

  const numValue = Number(value);
  if (Number.isNaN(numValue)) return false;

  return operator === "gt" ? fieldValue > numValue : fieldValue < numValue;
}

/**
 * Apply parsed query filters + free-text search to an array of rows.
 * Free text searches across name, cluster name, node name, and VMID.
 */
export function applyFilter(
  rows: InventoryRow[],
  parsed: ParsedQuery,
): InventoryRow[] {
  return rows.filter((row) => {
    // All structured filters must match (AND logic)
    for (const filter of parsed.filters) {
      if (!matchFilter(row, filter)) return false;
    }

    // Free text matches across multiple fields
    if (parsed.freeText) {
      const haystack = [
        row.name.toLowerCase(),
        row.clusterName.toLowerCase(),
        row.nodeName.toLowerCase(),
        row.vmid !== null ? String(row.vmid) : "",
        row.tags.toLowerCase(),
      ].join(" ");

      if (!haystack.includes(parsed.freeText)) {
        return false;
      }
    }

    return true;
  });
}
