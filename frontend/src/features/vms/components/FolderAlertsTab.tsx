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
import { AlertRow } from "@/features/alerts/components/AlertsTable";
import {
  useAcknowledgeAlert,
  useAlerts,
  useResolveAlert,
} from "@/features/alerts/api/alert-queries";
import { useAuth } from "@/hooks/useAuth";

interface FolderAlertsTabProps {
  clusterId: string;
  /** Current `vms.id` UUIDs of the folder's VMs — alerts are matched on
   * `alert.vm_id`. Node- and cluster-level alerts never match, which is the
   * point of a folder-scoped view.
   *
   * Known limitation: `alert_history.vm_id` is ON DELETE SET NULL and the
   * collector re-creates VM rows with new UUIDs on churn, so an alert whose
   * VM row churned drops out of this view (still visible on the global
   * Alerts page). Durable fix is backend — expose the Proxmox VMID on
   * alerts or rekey alert_history to (cluster_id, vmid) like 000068/000069
   * did for memberships and rules. */
  vmIds: Set<string>;
}

export function FolderAlertsTab({ clusterId, vmIds }: FolderAlertsTabProps) {
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
        (a) => a.vm_id !== undefined && vmIds.has(a.vm_id),
      ),
    [alerts, vmIds],
  );

  const formatDate = (s?: string) => {
    if (!s) return "—";
    return new Date(s).toLocaleString();
  };

  return (
    <div className="space-y-4">
      <div className="flex gap-2">
        <Select value={stateFilter} onValueChange={setStateFilter}>
          <SelectTrigger className="w-40">
            <SelectValue placeholder="All States" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All States</SelectItem>
            <SelectItem value="firing">Firing</SelectItem>
            <SelectItem value="pending">Pending</SelectItem>
            <SelectItem value="acknowledged">Acknowledged</SelectItem>
            <SelectItem value="resolved">Resolved</SelectItem>
          </SelectContent>
        </Select>

        <Select value={severityFilter} onValueChange={setSeverityFilter}>
          <SelectTrigger className="w-40">
            <SelectValue placeholder="All Severities" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Severities</SelectItem>
            <SelectItem value="critical">Critical</SelectItem>
            <SelectItem value="warning">Warning</SelectItem>
            <SelectItem value="info">Info</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
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
              <TableRow>
                <TableCell
                  colSpan={canAcknowledge ? 8 : 7}
                  className="py-8 text-center text-muted-foreground"
                >
                  Loading...
                </TableCell>
              </TableRow>
            )}
            {!isLoading && folderAlerts.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={canAcknowledge ? 8 : 7}
                  className="py-8 text-center text-muted-foreground"
                >
                  No alerts for VMs in this folder.
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
      </div>
    </div>
  );
}
