import { useMemo, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Panel } from "@/components/page/Panel";
import { AlertRow } from "@/features/alerts/components/AlertsTable";
import {
  useAcknowledgeAlert,
  useAlerts,
  useResolveAlert,
} from "@/features/alerts/api/alert-queries";
import { useAuth } from "@/hooks/useAuth";
import { formatDateTime } from "@/lib/format";

interface FolderAlertsTabProps {
  clusterId: string;
  /** Proxmox VMIDs of the folder's VMs — alerts are matched on the stable
   * `alert.vm_vmid` (migration 000077), which survives collector churn of
   * the vms row. Node- and cluster-level alerts never match, which is the
   * point of a folder-scoped view. Pre-000077 rows whose vm_id had already
   * churned to NULL carry no vm_vmid and stay unmatchable (historical only). */
  vmids: Set<number>;
}

export function FolderAlertsTab({ clusterId, vmids }: FolderAlertsTabProps) {
  const [stateFilter, setStateFilter] = useState("");
  const [severityFilter, setSeverityFilter] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const { hasPermission } = useAuth();

  const filters: {
    state?: string | undefined;
    severity?: string | undefined;
    clusterId?: string | undefined;
  } = { clusterId };
  if (stateFilter && stateFilter !== "all") filters.state = stateFilter;
  if (severityFilter && severityFilter !== "all")
    filters.severity = severityFilter;

  const { data: alerts, isLoading } = useAlerts(filters);
  const ackMutation = useAcknowledgeAlert();
  const resolveMutation = useResolveAlert();
  const canAcknowledge = hasPermission("acknowledge", "alert");

  const folderAlerts = useMemo(
    () =>
      (alerts ?? []).filter(
        (a) => a.vm_vmid !== undefined && vmids.has(a.vm_vmid),
      ),
    [alerts, vmids],
  );

  const formatDate = (s?: string) => formatDateTime(s);

  const colSpan = canAcknowledge ? 8 : 7;

  return (
    <Panel
      flush
      title="Alerts"
      aside={folderAlerts.length > 0 ? String(folderAlerts.length) : undefined}
      actions={
        <>
          <Select value={stateFilter} onValueChange={setStateFilter}>
            <SelectTrigger className="h-8 w-36" aria-label="State">
              <SelectValue placeholder="All states" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All states</SelectItem>
              <SelectItem value="firing">Firing</SelectItem>
              <SelectItem value="pending">Pending</SelectItem>
              <SelectItem value="acknowledged">Acknowledged</SelectItem>
              <SelectItem value="resolved">Resolved</SelectItem>
            </SelectContent>
          </Select>

          <Select value={severityFilter} onValueChange={setSeverityFilter}>
            <SelectTrigger className="h-8 w-36" aria-label="Severity">
              <SelectValue placeholder="All severities" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All severities</SelectItem>
              <SelectItem value="critical">Critical</SelectItem>
              <SelectItem value="warning">Warning</SelectItem>
              <SelectItem value="info">Info</SelectItem>
            </SelectContent>
          </Select>
        </>
      }
    >
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="w-8" />
            <TableHead>State</TableHead>
            <TableHead>Severity</TableHead>
            <TableHead>Resource</TableHead>
            <TableHead>Metric</TableHead>
            <TableHead>Message</TableHead>
            <TableHead>Time</TableHead>
            {canAcknowledge && <TableHead>Actions</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={colSpan}
                className="py-8 text-center text-muted-foreground"
              >
                Loading...
              </TableCell>
            </TableRow>
          )}
          {!isLoading && folderAlerts.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={colSpan}
                className="py-8 text-center text-muted-foreground"
              >
                No alerts for the guests in this folder.
              </TableCell>
            </TableRow>
          )}
          {folderAlerts.map((alert) => (
            <AlertRow
              key={alert.id}
              alert={alert}
              expanded={expandedId === alert.id}
              onToggle={() => {
                setExpandedId(expandedId === alert.id ? null : alert.id);
              }}
              canAcknowledge={canAcknowledge}
              onAcknowledge={() => {
                ackMutation.mutate(alert.id);
              }}
              onResolve={() => {
                resolveMutation.mutate(alert.id);
              }}
              formatDate={formatDate}
            />
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}
