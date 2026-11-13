import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { DetailField } from "@/components/DetailField";
import { DefGrid, Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ShieldOff, AlertTriangle } from "lucide-react";
import { ApiClientError } from "@/lib/api-client";
import { formatBytes } from "@/lib/format";
import { useVeeamGuestProtection } from "@/features/backup/api/backup-queries";
import type { VeeamGuestProtection } from "@/features/backup/types/backup";

function formatTime(value: string | null): string {
  if (value == null || value === "") return "Never";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "Unknown" : parsed.toLocaleString();
}

function ageLabel(value: string | null): string {
  if (value == null || value === "") return "";
  const parsed = new Date(value).getTime();
  if (Number.isNaN(parsed)) return "";
  const hours = Math.floor((Date.now() - parsed) / 3_600_000);
  if (hours < 1) return "under an hour ago";
  if (hours < 48) return `${String(hours)}h ago`;
  return `${String(Math.floor(hours / 24))}d ago`;
}

/**
 * How the guest was tied to its Veeam backup.
 *
 * A "name" match is a guess and is labelled as one. Veeam identifies a Proxmox
 * guest by its smbios1 uuid, and when that matches, the answer is certain; the
 * name tier only applies to guests whose uuid Nexara has affirmatively
 * recorded as absent, and a rebuilt host reuses its name. Presenting the two
 * identically would be the single most misleading thing this card could do.
 */
function MatchBadge({
  method,
}: {
  method: VeeamGuestProtection["match_method"];
}) {
  switch (method) {
    case "smbios":
      return (
        <span title="Matched on the guest's SMBIOS UUID">Verified match</span>
      );
    case "manual":
      return (
        <span title="An operator mapped this backup to this guest">
          Manual match
        </span>
      );
    case "name":
      return (
        <Badge
          variant="warning"
          className="gap-1"
          title="Matched by name only. This guest has no SMBIOS UUID recorded, so the backup may belong to a machine it replaced"
        >
          <AlertTriangle className="h-3 w-3" />
          Name match
        </Badge>
      );
    default:
      return null;
  }
}

function MalwareBadge({ status }: { status: string }) {
  if (status === "" || status === "Clean") return null;
  const severe = status === "Infected";
  return (
    <Badge
      variant={severe ? "destructive" : "warning"}
      className="gap-1"
      title="Veeam's verdict on the newest restore point"
    >
      <AlertTriangle className="h-3 w-3" />
      {status}
    </Badge>
  );
}

/**
 * The Veeam half of a guest's backup posture, beside the PBS panel.
 *
 * Renders nothing at all when Veeam has never seen this guest AND the caller
 * can see Veeam data — an empty card on every guest in a PBS-only estate is
 * noise. A 403 is likewise silent: a viewer without view:veeam on this cluster
 * is not missing anything they were meant to see.
 */
export function VeeamProtectionCard({
  clusterId,
  vmId,
}: {
  clusterId: string;
  vmId: string;
}) {
  const { data, isLoading, isError, error } = useVeeamGuestProtection(
    clusterId,
    vmId,
  );

  if (isLoading) {
    return <Skeleton className="h-32" />;
  }
  if (isError) {
    const status = error instanceof ApiClientError ? error.status : 0;
    if (status === 403 || status === 404) {
      return null;
    }
    return (
      <Panel title="Veeam protection">
        <p className="text-sm text-destructive">
          Could not load Veeam protection for this guest.
        </p>
      </Panel>
    );
  }
  if (!data || data.object_count === 0) {
    // Veeam has no backup object for this guest. That is not the same as
    // "unprotected" — the coverage report is what answers whether it SHOULD
    // be — so this card says nothing rather than implying a verdict.
    return null;
  }

  const matched = data.match_method !== "none";
  const scanned = data.malware_status !== "";

  return (
    <Panel
      flush
      title="Veeam protection"
      // Quiet when protected, as a healthy thing is everywhere else.
      actions={
        data.protected ? (
          <span className="flex items-center gap-[7px] text-sm text-muted-foreground">
            <span
              aria-hidden="true"
              className="h-[7px] w-[7px] rounded-full bg-emerald-500"
            />
            Protected
          </span>
        ) : (
          <Badge variant="destructive" className="gap-1">
            <ShieldOff className="h-3 w-3" />
            No restore points
          </Badge>
        )
      }
    >
      <div className="space-y-4 px-[18px] pb-1">
        {!data.protected && (
          <p className="max-w-[65ch] text-sm text-muted-foreground">
            Veeam knows this guest but holds no restore points for it: every one
            has been pruned. Nothing here can be recovered.
          </p>
        )}

        <DefGrid>
          <DetailField label="Newest point">
            {formatTime(data.latest_restore_point)}
            {ageLabel(data.latest_restore_point) !== "" && (
              <span className="ml-1 text-muted-foreground">
                ({ageLabel(data.latest_restore_point)})
              </span>
            )}
          </DetailField>
          <DetailField label="Restore points" variant="mono">
            {data.restore_point_count}
          </DetailField>
          <DetailField label="Size" variant="mono">
            {formatBytes(data.restore_point_bytes)}
          </DetailField>
          {matched && (
            <DetailField label="Matched to this guest by">
              <MatchBadge method={data.match_method} />
            </DetailField>
          )}
          {scanned && (
            <DetailField label="Malware scan">
              {data.malware_status === "Clean" ? (
                "Clean"
              ) : (
                <MalwareBadge status={data.malware_status} />
              )}
            </DetailField>
          )}
          {data.last_run_failed && (
            <DetailField label="Last run">
              <span className="text-red-700 dark:text-red-400">
                Failed or cancelled
              </span>
            </DetailField>
          )}
        </DefGrid>
      </div>

      {data.restore_points.length > 0 && (
        <div className="mt-3 border-t">
          {data.restore_point_count > data.restore_points.length && (
            // Said out loud. The summary above reports the true total, and
            // an operator scanning this list for a specific date would
            // otherwise conclude that point does not exist.
            <p className="px-[18px] pt-3 text-[12.5px] text-muted-foreground">
              Showing the newest {data.restore_points.length} of{" "}
              {data.restore_point_count}.
            </p>
          )}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Created</TableHead>
                <TableHead>Type</TableHead>
                {/*
                  No "Backup" column. The collector folds Veeam's listing to
                  one backup object per guest, so the object name is just the
                  guest's own — a column repeating "linux03" on every row
                  under a header promising to say which job produced the
                  point. The point's backup id is a bare UUID and no more
                  use. Better to omit it than to imply an answer.
                */}
                <TableHead>Malware</TableHead>
                <TableHead className="text-right">Size</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.restore_points.map((point) => (
                <TableRow key={point.id}>
                  <TableCell className="whitespace-nowrap tabular-nums">
                    {formatTime(point.creation_time)}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {point.point_type || "—"}
                  </TableCell>
                  <TableCell>
                    {point.malware_status === "Clean" ||
                    point.malware_status === "" ? (
                      <span className="text-muted-foreground">
                        {point.malware_status || "—"}
                      </span>
                    ) : (
                      <MalwareBadge status={point.malware_status} />
                    )}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[12.5px]">
                    {formatBytes(point.size_bytes)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Panel>
  );
}
