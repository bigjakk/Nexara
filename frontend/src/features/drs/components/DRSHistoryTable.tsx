import { useState } from "react";
import { Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useDRSHistory } from "../api/drs-queries";
import { DateTime } from "@/components/DateTime";
import { useTranslation, Trans } from "react-i18next";

interface DRSHistoryTableProps {
  clusterId: string;
}

function statusVariant(status: string): BadgeVariant {
  switch (status) {
    case "completed":
      return "healthy";
    case "failed":
    case "timeout":
      return "destructive";
    case "advisory":
    case "pending":
      return "secondary";
    case "cancelled":
      return "outline";
    default:
      return "secondary";
  }
}

function formatDate(dateStr: string | null) {
  return <DateTime value={dateStr} fallback="-" />;
}

export function DRSHistoryTable({ clusterId }: DRSHistoryTableProps) {
  const { t } = useTranslation("clusters");
  const [limit, setLimit] = useState(25);
  const { data: history, isLoading } = useDRSHistory(clusterId, limit);

  if (isLoading) {
    return (
      <Panel title={t("drsHistoryTable.migrations")}>
        <Skeleton className="h-48 w-full" />
      </Panel>
    );
  }

  return (
    <Panel
      flush
      title={t("drsHistoryTable.migrations")}
      aside={t("drsHistoryTable.whatDRSMovedOrRecommended")}
      actions={
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">
            {t("drsHistoryTable.show")}
          </span>
          <Select
            value={String(limit)}
            onValueChange={(v) => {
              setLimit(Number(v));
            }}
          >
            <SelectTrigger
              className="h-8 w-20"
              aria-label={t("drsHistoryTable.entriesToShow")}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="25">25</SelectItem>
              <SelectItem value="50">50</SelectItem>
              <SelectItem value="100">100</SelectItem>
            </SelectContent>
          </Select>
          <span className="text-sm text-muted-foreground">
            {t("drsHistoryTable.entries")}
          </span>
        </div>
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("drsHistoryTable.vm")}</TableHead>
            <TableHead>{t("drsHistoryTable.source")}</TableHead>
            <TableHead>{t("drsHistoryTable.target")}</TableHead>
            <TableHead>{t("drsHistoryTable.reason")}</TableHead>
            <TableHead>{t("drsHistoryTable.score")}</TableHead>
            <TableHead>{t("drsHistoryTable.status")}</TableHead>
            <TableHead>{t("drsHistoryTable.time")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {!history || history.length === 0 ? (
            <TableRow>
              <TableCell
                colSpan={7}
                className="py-6 text-center text-muted-foreground"
              >
                {t("drsHistoryTable.noHistoryEntries")}
              </TableCell>
            </TableRow>
          ) : (
            history.map((entry) => {
              const improvement =
                entry.score_before > 0
                  ? (
                      ((entry.score_before - entry.score_after) /
                        entry.score_before) *
                      100
                    ).toFixed(1)
                  : "0.0";

              return (
                <TableRow key={entry.id}>
                  <TableCell className="font-mono text-sm">
                    {entry.vm_type.toUpperCase()} {entry.vm_id}
                  </TableCell>
                  <TableCell>{entry.source_node}</TableCell>
                  <TableCell>{entry.target_node}</TableCell>
                  <TableCell className="max-w-48 truncate text-sm">
                    {entry.reason}
                  </TableCell>
                  <TableCell className="text-sm">
                    <Trans
                      t={t}
                      i18nKey="drsHistoryTable.scoreBeforeScoreAfterSpanImprovementSpan"
                      values={{
                        scoreBefore: entry.score_before.toFixed(2),
                        scoreAfter: entry.score_after.toFixed(2),
                        improvement,
                      }}
                      components={{
                        span: <span className="ml-1 text-muted-foreground" />,
                      }}
                    />
                  </TableCell>
                  <TableCell>
                    <Badge variant={statusVariant(entry.status)}>
                      {entry.status}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-sm">
                    {formatDate(entry.executed_at ?? entry.created_at)}
                  </TableCell>
                </TableRow>
              );
            })
          )}
        </TableBody>
      </Table>
    </Panel>
  );
}
