import { Fragment, useMemo, useState } from "react";
import { Search } from "lucide-react";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Input } from "@/components/ui/input";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";

interface UpsVar {
  id: string;
  value: string;
  description: string;
}

type SortKey = "expand" | "name" | "value" | "description";

const COLUMNS: ColumnDef<UpsVar, SortKey, { expanded: Set<string> }>[] = [
  expandColumn("expand"),
  { key: "name", label: "Variable", width: 240, sortValue: (v) => v.id, cell: (v) => <span className="font-mono text-xs">{v.id}</span> },
  {
    key: "value",
    label: "Value",
    width: 200,
    sortValue: (v) => v.value,
    wrap: true,
    cell: (v) => <span className="break-all font-mono text-xs font-semibold">{v.value}</span>,
  },
  {
    key: "description",
    label: "Description",
    width: 360,
    sortValue: (v) => v.description || null,
    wrap: true,
    cell: (v) => <span className="text-sm text-muted-foreground">{v.description || "—"}</span>,
  },
];

/**
 * Every variable upsd reported, with upsd's own description of each. The
 * descriptions are fetched a few per poll, so a new UPS fills them in over
 * its first minute.
 */
export function UpsVariablesTable({
  vars,
  descriptions,
}: {
  vars: Record<string, string>;
  descriptions: Record<string, string>;
}) {
  const [filter, setFilter] = useState("");
  const all = useMemo<UpsVar[]>(
    () => Object.entries(vars).map(([id, value]) => ({ id, value, description: descriptions[id] ?? "" })),
    [vars, descriptions],
  );
  const q = filter.trim().toLowerCase();
  const shown = useMemo(
    () => (q === "" ? all : all.filter((v) => v.id.includes(q) || v.description.toLowerCase().includes(q))),
    [all, q],
  );
  const { layout, rows, toggle, directionFor } = useDataTable("ups-variables", COLUMNS, shown, (v) => v.id);
  const { expanded, toggle: toggleExpand } = useExpandedRows();

  return (
    <div className="space-y-3">
      <div className="relative max-w-sm">
        <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" aria-hidden="true" />
        <Input
          aria-label="Filter variables"
          placeholder={`Filter ${String(all.length)} variables`}
          className="pl-8"
          value={filter}
          onChange={(e) => { setFilter(e.target.value); }}
        />
      </div>
      <div className="rounded-md border">
        <DataTableFrame layout={layout}>
          <TableHeader>
            <DataTableHeadRow layout={layout} directionFor={directionFor} onSort={toggle} />
          </TableHeader>
          <TableBody>
            {rows.map((v) => (
              <Fragment key={v.id}>
                <TableRow className="cursor-pointer" onClick={() => { toggleExpand(v.id); }}>
                  <DataTableCells row={v} layout={layout} ctx={{ expanded }} />
                </TableRow>
                {expanded.has(v.id) && (
                  <ExpandedDetailRow colSpan={layout.columns.length}>
                    <p className="text-sm">{v.description || "upsd has no description for this variable."}</p>
                    <code className="select-all break-all font-mono text-xs">{v.value}</code>
                  </ExpandedDetailRow>
                )}
              </Fragment>
            ))}
          </TableBody>
        </DataTableFrame>
      </div>
    </div>
  );
}
