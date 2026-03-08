import { useState } from "react";
import { ChevronDown, ChevronRight, CheckCircle, Eye } from "lucide-react";
import { Button } from "@/components/ui/button";
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
import { AlertStateBadge } from "./AlertStateBadge";
import { AlertSeverityBadge } from "./AlertSeverityBadge";
import {
  useAlerts,
  useAcknowledgeAlert,
  useResolveAlert,
} from "../api/alert-queries";
import { useAuth } from "@/hooks/useAuth";
import type { AlertInstance } from "@/types/api";

export function AlertsTable() {
  const [stateFilter, setStateFilter] = useState<string>("");
  const [severityFilter, setSeverityFilter] = useState<string>("");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const { hasPermission } = useAuth();

  const filters: { state?: string | undefined; severity?: string | undefined } = {};
  if (stateFilter && stateFilter !== "all") filters.state = stateFilter;
  if (severityFilter && severityFilter !== "all") filters.severity = severityFilter;

  const { data: alerts, isLoading } = useAlerts(filters);

  const ackMutation = useAcknowledgeAlert();
  const resolveMutation = useResolveAlert();

  const canAcknowledge = hasPermission("acknowledge", "alert");

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
                <TableCell colSpan={canAcknowledge ? 8 : 7} className="text-center text-muted-foreground py-8">
                  Loading...
                </TableCell>
              </TableRow>
            )}
            {!isLoading && (!alerts || alerts.length === 0) && (
              <TableRow>
                <TableCell colSpan={canAcknowledge ? 8 : 7} className="text-center text-muted-foreground py-8">
                  No alerts found
                </TableCell>
              </TableRow>
            )}
            {alerts?.map((alert) => (
              <AlertRow
                key={alert.id}
                alert={alert}
                expanded={expandedId === alert.id}
                onToggle={() => {
                  setExpandedId(expandedId === alert.id ? null : alert.id);
                }}
                canAcknowledge={canAcknowledge}
                onAcknowledge={() => { ackMutation.mutate(alert.id); }}
                onResolve={() => { resolveMutation.mutate(alert.id); }}
                formatDate={formatDate}
              />
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function AlertRow({
  alert,
  expanded,
  onToggle,
  canAcknowledge,
  onAcknowledge,
  onResolve,
  formatDate,
}: {
  alert: AlertInstance;
  expanded: boolean;
  onToggle: () => void;
  canAcknowledge: boolean;
  onAcknowledge: () => void;
  onResolve: () => void;
  formatDate: (s?: string) => string;
}) {
  return (
    <>
      <TableRow
        className="cursor-pointer hover:bg-muted/50"
        onClick={onToggle}
      >
        <TableCell>
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </TableCell>
        <TableCell>
          <AlertStateBadge state={alert.state} />
        </TableCell>
        <TableCell>
          <AlertSeverityBadge severity={alert.severity} />
        </TableCell>
        <TableCell className="font-medium">{alert.resource_name}</TableCell>
        <TableCell className="text-muted-foreground">{alert.metric}</TableCell>
        <TableCell className="max-w-xs truncate text-sm">
          {alert.message}
        </TableCell>
        <TableCell className="text-sm text-muted-foreground">
          {formatDate(alert.fired_at ?? alert.pending_at)}
        </TableCell>
        {canAcknowledge && (
          <TableCell onClick={(e) => { e.stopPropagation(); }}>
            <div className="flex gap-1">
              {alert.state === "firing" && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={onAcknowledge}
                  title="Acknowledge"
                >
                  <Eye className="h-3 w-3" />
                </Button>
              )}
              {(alert.state === "firing" || alert.state === "acknowledged") && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={onResolve}
                  title="Resolve"
                >
                  <CheckCircle className="h-3 w-3" />
                </Button>
              )}
            </div>
          </TableCell>
        )}
      </TableRow>

      {expanded && (
        <TableRow>
          <TableCell colSpan={canAcknowledge ? 8 : 7} className="bg-muted/30 p-4">
            <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm md:grid-cols-4">
              <div>
                <span className="text-muted-foreground">Alert ID:</span>{" "}
                <span className="font-mono text-xs">{alert.id}</span>
              </div>
              <div>
                <span className="text-muted-foreground">Rule ID:</span>{" "}
                <span className="font-mono text-xs">{alert.rule_id}</span>
              </div>
              <div>
                <span className="text-muted-foreground">Current Value:</span>{" "}
                {alert.current_value.toFixed(2)}
              </div>
              <div>
                <span className="text-muted-foreground">Threshold:</span>{" "}
                {alert.threshold.toFixed(2)}
              </div>
              <div>
                <span className="text-muted-foreground">Pending At:</span>{" "}
                {formatDate(alert.pending_at)}
              </div>
              <div>
                <span className="text-muted-foreground">Fired At:</span>{" "}
                {formatDate(alert.fired_at)}
              </div>
              <div>
                <span className="text-muted-foreground">Acknowledged At:</span>{" "}
                {formatDate(alert.acknowledged_at)}
              </div>
              <div>
                <span className="text-muted-foreground">Resolved At:</span>{" "}
                {formatDate(alert.resolved_at)}
              </div>
              <div>
                <span className="text-muted-foreground">Escalation Level:</span>{" "}
                {alert.escalation_level}
              </div>
              {alert.cluster_id && (
                <div>
                  <span className="text-muted-foreground">Cluster:</span>{" "}
                  <span className="font-mono text-xs">{alert.cluster_id}</span>
                </div>
              )}
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
