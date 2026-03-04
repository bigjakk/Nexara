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
]);

/**
 * Parse a search query string into structured filters.
 *
 * Supported syntax:
 * - `field:value`  — exact match (e.g. `type:vm`, `status:running`)
 * - `field>N%`     — greater-than comparison (e.g. `cpu>80%`)
 * - `field<N%`     — less-than comparison (e.g. `mem<50%`)
 * - bare text      — fuzzy name match
 */
export function parseQuery(query: string): ParsedQuery {
  const filters: FilterCriteria[] = [];
  const freeTextParts: string[] = [];

  // Regex declared inside function to avoid /g statefulness bugs
  const tokenRegex = /(\S+)/g;
  let match: RegExpExecArray | null;

  while ((match = tokenRegex.exec(query)) !== null) {
    const token = match[1] ?? "";

    // field>N% or field<N%
    const compMatch = /^([a-z]+)([><])(\d+)%?$/i.exec(token);
    if (compMatch?.[1] && compMatch[2] && compMatch[3]) {
      const field = compMatch[1].toLowerCase();
      if (VALID_FIELDS.has(field)) {
        filters.push({
          field,
          operator: compMatch[2] === ">" ? "gt" : "lt",
          value: compMatch[3],
        });
        continue;
      }
    }

    // field:value
    const kvMatch = /^([a-z]+):(.+)$/i.exec(token);
    if (kvMatch?.[1] && kvMatch[2]) {
      const field = kvMatch[1].toLowerCase();
      if (VALID_FIELDS.has(field)) {
        filters.push({
          field,
          operator: "eq",
          value: kvMatch[2].toLowerCase(),
        });
        continue;
      }
    }

    freeTextParts.push(token);
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

function getFieldValue(row: InventoryRow, field: string): string | number | null {
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
    default:
      return null;
  }
}

function matchFilter(row: InventoryRow, filter: FilterCriteria): boolean {
  const { field, operator, value } = filter;

  if (operator === "eq") {
    // Special handling for type aliases
    if (field === "type") {
      const normalized = normalizeType(value);
      return normalized !== null && row.type === normalized;
    }

    const fieldValue = getFieldValue(row, field);
    if (fieldValue === null) return false;

    if (typeof fieldValue === "number") {
      return fieldValue === Number(value);
    }
    return fieldValue.includes(value);
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
 */
export function applyFilter(
  rows: InventoryRow[],
  parsed: ParsedQuery,
): InventoryRow[] {
  return rows.filter((row) => {
    // All structured filters must match
    for (const filter of parsed.filters) {
      if (!matchFilter(row, filter)) return false;
    }

    // Free text matches name
    if (parsed.freeText && !row.name.toLowerCase().includes(parsed.freeText)) {
      return false;
    }

    return true;
  });
}
