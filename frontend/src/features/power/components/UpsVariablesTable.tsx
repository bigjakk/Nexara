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
import { Panel } from "@/components/page/Panel";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import type { UpsWritableVar } from "../types/ups";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

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
  { key: "name", get label() { return i18n.t("power:upsVariablesTable.variable"); }, width: 240, sortValue: (v) => v.id, cell: (v) => <span className="font-mono text-xs">{v.id}</span> },
  {
    key: "value",
    get label() { return i18n.t("power:upsVariablesTable.value"); },
    width: 200,
    sortValue: (v) => v.value,
    wrap: true,
    cell: (v) => <span className="break-all font-mono text-xs font-semibold">{v.value}</span>,
  },
  {
    key: "description",
    get label() { return i18n.t("power:upsVariablesTable.description"); },
    width: 460,
    sortValue: (v) => v.description || null,
    wrap: true,
    cell: (v) => <span className="text-sm text-muted-foreground">{v.description || "—"}</span>,
  },
  {
    key: "set",
    get label() { return i18n.t("power:upsVariablesTable.writable"); },
    width: 110,
    sortValue: (v) => (v.writable === undefined ? 0 : 1),
    cell: (v, { edit, whyNot }) => {
      const w = v.writable;
      if (w === undefined) return null;
      if (edit === undefined) {
        return <Badge variant="outline" className="font-medium text-muted-foreground" title={whyNot ?? undefined}>{i18n.t("power:upsVariablesTable.writable")}</Badge>;
      }
      return (
        <Button
          size="sm"
          variant="ghost"
          aria-label={i18n.t("power:upsVariablesTable.setName", { name: w.name })}
          onClick={(e) => {
            e.stopPropagation();
            edit(w);
          }}
        >
          <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
          {i18n.t("power:upsVariablesTable.set")}
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
  const { t } = useTranslation("power");
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
    <Panel
      flush
      title={t("upsVariablesTable.variables")}
      aside={q === "" ? t("upsVariablesTable.lengthFromUpsd", { length: String(all.length) }) : t("upsVariablesTable.lengthOfLength2", { length: String(shown.length), length2: String(all.length) })}
      actions={
        <div className="relative w-64 max-w-full">
          <Search className="absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <Input
            aria-label={t("upsVariablesTable.filterVariables")}
            placeholder={t("upsVariablesTable.filterLengthVariables", { length: String(all.length) })}
            className="h-8 pl-8"
            value={filter}
            onChange={(e) => { setFilter(e.target.value); }}
          />
        </div>
      }
    >
      {rows.length === 0 && (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          {all.length === 0 ? t("upsVariablesTable.upsdReportedNoVariablesFor") : t("upsVariablesTable.noVariableMatchesTheFilter")}
        </p>
      )}
      {rows.length > 0 && (
        <DataTableFrame layout={layout}>
          <TableHeader>
            <DataTableHeadRow layout={layout} directionFor={directionFor} onSort={toggle} />
          </TableHeader>
          <TableBody>
            {rows.map((v) => {
              // null from whyNotSet means nothing stops it; no whyNotSet, that nothing is offered here.
              const whyNot =
                v.writable === undefined ? null : whyNotSet === undefined ? t("upsVariablesTable.settingItIsNotOffered") : whyNotSet(v.writable);
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
                      <p className="text-sm">{v.description || t("upsVariablesTable.upsdHasNoDescriptionFor")}</p>
                      <code className="select-all break-all font-mono text-xs">{v.value}</code>
                      {whyNot !== null && <p className="text-xs text-muted-foreground">{t("upsVariablesTable.upsdLetsClientsSetIt", { whyNot })}</p>}
                    </ExpandedDetailRow>
                  )}
                </Fragment>
              );
            })}
          </TableBody>
        </DataTableFrame>
      )}
    </Panel>
  );
}
