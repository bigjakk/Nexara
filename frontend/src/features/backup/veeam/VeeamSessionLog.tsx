import { Badge } from "@/components/ui/badge";
import { useVeeamSessionLogs } from "../api/backup-queries";
import type { VeeamSession } from "../types/backup";

interface VeeamSessionLogProps {
  serverId: string;
  session: VeeamSession;
  /** True while the row is expanded. Nothing is fetched until it is. */
  enabled: boolean;
}

function statusVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "Succeeded":
      return "default";
    case "Warning":
      return "outline";
    case "Failed":
      return "destructive";
    default:
      return "secondary";
  }
}

function formatTime(value: string | null): string {
  if (value == null || value === "") return "";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "";
  return parsed.toLocaleTimeString();
}

/**
 * A session's log, read live from the Veeam server.
 *
 * AN EMPTY LOG IS A RESULT, NOT A FAILURE. Veeam keeps no records at all for a
 * session that was stopped, so the empty state has to say which case it is —
 * otherwise "the run was cancelled" and "we could not reach the server" render
 * identically and an operator reads the wrong one.
 */
export function VeeamSessionLog({
  serverId,
  session,
  enabled,
}: VeeamSessionLogProps) {
  const { data, isLoading, isError, error } = useVeeamSessionLogs(
    serverId,
    session.veeam_id,
    enabled,
  );

  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading log…</p>;
  }

  if (isError) {
    return (
      <p className="text-xs text-destructive">
        Could not read the log from the Veeam server.{" "}
        {error instanceof Error ? error.message : ""}
      </p>
    );
  }

  const records = data ?? [];

  if (records.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        {session.nexara_stopped
          ? "Veeam keeps no log for a run that was stopped, so there is nothing to show for this one."
          : "Veeam returned no log records for this run. That is what a stopped run looks like — a run cancelled from the Veeam console is recorded exactly like a failure, with an empty log."}
      </p>
    );
  }

  return (
    <div>
      <p className="mb-2 text-xs font-medium text-muted-foreground">
        Session log
      </p>
      <ol className="space-y-1">
        {records.map((record) => (
          <li
            key={record.id}
            className="flex items-start gap-2 text-xs leading-relaxed"
          >
            <span className="w-16 shrink-0 font-mono text-muted-foreground">
              {formatTime(record.start_time)}
            </span>
            <Badge
              variant={statusVariant(record.status)}
              className="shrink-0 px-1.5 py-0 text-[10px]"
            >
              {record.status}
            </Badge>
            <span className="min-w-0">
              {record.title}
              {record.description !== "" && (
                <span className="text-muted-foreground">
                  {" — "}
                  {record.description}
                </span>
              )}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}
