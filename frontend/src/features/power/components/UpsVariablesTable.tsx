import { Fragment, useMemo, useState } from "react";
import { Pencil, Search } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Input } from "@/components/ui/input";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import type { UpsWritableVar } from "../types/ups";

interface UpsVar {
  id: string;
  value: string;
  description: string;
  writable: UpsWritableVar | undefined;
}

type SortKey = "expand" | "name" | "value" | "description" | "set";

interface Ctx {
  expanded: Set<string>;
  /** Opens the editor, for a variable the caller may set here; undefined otherwise. */
  edit: ((v: UpsWritableVar) => void) | undefined;
  /** Why the row's variable cannot be set here, or null. */
  whyNot: string | null;
}

const COLUMNS: ColumnDef<UpsVar, SortKey, Ctx>[] = [
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
  {
    key: "set",
    label: "Writable",
    width: 110,
    sortValue: (v) => (v.writable === undefined ? 0 : 1),
    cell: (v, { edit, whyNot }) => {
      const w = v.writable;
      if (w === undefined) return null;
      if (edit === undefined) {
        return <Badge variant="outline" title={whyNot ?? undefined}>writable</Badge>;
      }
      return (
        <Button
          size="sm"
          variant="ghost"
          aria-label={`Set ${w.name}`}
          onClick={(e) => {
            e.stopPropagation();
            edit(w);
          }}
        >
          <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
          Set
        </Button>
      );
    },
  },
];

/**
 * Every variable upsd reported, with upsd's own description of each. The
 * descriptions are fetched a few per poll, so a new UPS fills them in over
 * its first minute. A variable upsd lets clients set is marked, with a Set
 * button where whyNotSet says nothing stops it, and the reason in its row
 * where something does.
 */
export function UpsVariablesTable({
  vars,
  descriptions,
  writable = [],
  whyNotSet,
  onSet,
}: {
  vars: Record<string, string>;
  descriptions: Record<string, string>;
  writable?: UpsWritableVar[];
  whyNotSet?: (v: UpsWritableVar) => string | null;
  onSet?: (v: UpsWritableVar) => void;
}) {
  const [filter, setFilter] = useState("");
  const all = useMemo<UpsVar[]>(() => {
    const rw = new Map(writable.map((w) => [w.name, w]));
    return Object.entries(vars).map(([id, value]) => ({ id, value, description: descriptions[id] ?? "", writable: rw.get(id) }));
  }, [vars, descriptions, writable]);
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
            {rows.map((v) => {
              // null from whyNotSet means nothing stops it; no whyNotSet, that nothing is offered here.
              const whyNot =
                v.writable === undefined ? null : whyNotSet === undefined ? "Setting it is not offered here" : whyNotSet(v.writable);
              return (
                <Fragment key={v.id}>
                  <TableRow className="cursor-pointer" onClick={() => { toggleExpand(v.id); }}>
                    <DataTableCells
                      row={v}
                      layout={layout}
                      ctx={{ expanded, edit: v.writable !== undefined && whyNot === null ? onSet : undefined, whyNot }}
                    />
                  </TableRow>
                  {expanded.has(v.id) && (
                    <ExpandedDetailRow colSpan={layout.columns.length}>
                      <p className="text-sm">{v.description || "upsd has no description for this variable."}</p>
                      <code className="select-all break-all font-mono text-xs">{v.value}</code>
                      {whyNot !== null && <p className="text-xs text-muted-foreground">upsd lets clients set it. {whyNot}.</p>}
                    </ExpandedDetailRow>
                  )}
                </Fragment>
              );
            })}
          </TableBody>
        </DataTableFrame>
      </div>
    </div>
  );
}
