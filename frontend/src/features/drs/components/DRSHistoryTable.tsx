import { useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useDRSHistory } from "../api/drs-queries";

interface DRSHistoryTableProps {
  clusterId: string;
}

function statusVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "completed":
      return "default";
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

function formatDate(dateStr: string | null): string {
  if (!dateStr) return "-";
  return new Date(dateStr).toLocaleString();
}

export function DRSHistoryTable({ clusterId }: DRSHistoryTableProps) {
  const [limit, setLimit] = useState(25);
  const { data: history, isLoading } = useDRSHistory(clusterId, limit);

  if (isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <span className="text-sm text-muted-foreground">Show</span>
        <Select
          value={String(limit)}
          onValueChange={(v) => { setLimit(Number(v)); }}
        >
          <SelectTrigger className="w-20">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="25">25</SelectItem>
            <SelectItem value="50">50</SelectItem>
            <SelectItem value="100">100</SelectItem>
          </SelectContent>
        </Select>
        <span className="text-sm text-muted-foreground">entries</span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>VM</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Target</TableHead>
              <TableHead>Reason</TableHead>
              <TableHead>Score</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Time</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {!history || history.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="text-center text-muted-foreground"
                >
                  No history entries
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
                      {entry.score_before.toFixed(2)} &rarr;{" "}
                      {entry.score_after.toFixed(2)}
                      <span className="ml-1 text-muted-foreground">
                        ({improvement}%)
                      </span>
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
      </div>
    </div>
  );
}
