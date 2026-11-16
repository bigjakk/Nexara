import { useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
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
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DefGrid, Panel } from "@/components/page/Panel";
import { AlertStateBadge } from "./AlertStateBadge";
import { AlertSeverityBadge } from "./AlertSeverityBadge";
import {
  useAlerts,
  useAcknowledgeAlert,
  useResolveAlert,
} from "../api/alert-queries";
import { useAuth } from "@/hooks/useAuth";
import type { AlertInstance } from "@/types/api";
import { DateTime } from "@/components/DateTime";
import { useTranslation } from "react-i18next";

export function AlertsTable() {
  const { t } = useTranslation("alerts");
  const [stateFilter, setStateFilter] = useState<string>("");
  const [severityFilter, setSeverityFilter] = useState<string>("");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const { hasPermission } = useAuth();

  const filters: { state?: string | undefined; severity?: string | undefined } =
    {};
  if (stateFilter && stateFilter !== "all") filters.state = stateFilter;
  if (severityFilter && severityFilter !== "all")
    filters.severity = severityFilter;
  const filtered =
    filters.state !== undefined || filters.severity !== undefined;

  const { data: alerts, isLoading } = useAlerts(filters);

  const ackMutation = useAcknowledgeAlert();
  const resolveMutation = useResolveAlert();

  const canAcknowledge = hasPermission("acknowledge", "alert");

  const formatDate = (s?: string) => <DateTime value={s} />;

  return (
    <Panel
      flush
      aside={
        alerts === undefined || alerts.length === 0
          ? undefined
          : filtered
            ? t("alertsTable.alertsMatch", { count: alerts.length })
            : t("alertsTable.alertsCount", { count: alerts.length })
      }
      actions={
        <>
          <Select value={stateFilter} onValueChange={setStateFilter}>
            <SelectTrigger
              className="h-8 w-40"
              aria-label={t("alertsTable.filterByState")}
            >
              <SelectValue placeholder={t("alertsTable.allStates")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("alertsTable.allStates")}</SelectItem>
              <SelectItem value="firing">{t("alertsTable.firing")}</SelectItem>
              <SelectItem value="pending">
                {t("alertsTable.pending")}
              </SelectItem>
              <SelectItem value="acknowledged">
                {t("alertsTable.acknowledged")}
              </SelectItem>
              <SelectItem value="resolved">
                {t("alertsTable.resolved")}
              </SelectItem>
            </SelectContent>
          </Select>

          <Select value={severityFilter} onValueChange={setSeverityFilter}>
            <SelectTrigger
              className="h-8 w-40"
              aria-label={t("alertsTable.filterBySeverity")}
            >
              <SelectValue placeholder={t("alertsTable.allSeverities")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">
                {t("alertsTable.allSeverities")}
              </SelectItem>
              <SelectItem value="critical">
                {t("alertsTable.critical")}
              </SelectItem>
              <SelectItem value="warning">
                {t("alertsTable.warning")}
              </SelectItem>
              <SelectItem value="info">{t("alertsTable.info")}</SelectItem>
            </SelectContent>
          </Select>
        </>
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-8" />
            <TableHead>{t("alertsTable.state")}</TableHead>
            <TableHead>{t("alertsTable.severity")}</TableHead>
            <TableHead>{t("alertsTable.resource")}</TableHead>
            <TableHead>{t("alertsTable.metric")}</TableHead>
            <TableHead>{t("alertsTable.message")}</TableHead>
            <TableHead>{t("alertsTable.time")}</TableHead>
            {canAcknowledge && (
              <TableHead>{t("alertsTable.actions")}</TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell
                colSpan={canAcknowledge ? 8 : 7}
                className="py-8 text-center text-muted-foreground"
              >
                {t("alertsTable.loading")}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && (!alerts || alerts.length === 0) && (
            <TableRow>
              <TableCell
                colSpan={canAcknowledge ? 8 : 7}
                className="py-8 text-center text-muted-foreground"
              >
                {t("alertsTable.noAlertsFound")}
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

export function AlertRow({
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
  formatDate: (s?: string) => ReactNode;
}) {
  const { t } = useTranslation("alerts");
  return (
    <>
      <TableRow className="cursor-pointer" onClick={onToggle}>
        <TableCell className="text-muted-foreground">
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
        <TableCell className="max-w-xs truncate" title={alert.message}>
          {alert.message}
        </TableCell>
        <TableCell className="whitespace-nowrap text-muted-foreground">
          {formatDate(alert.fired_at ?? alert.pending_at)}
        </TableCell>
        {canAcknowledge && (
          <TableCell
            onClick={(e) => {
              e.stopPropagation();
            }}
          >
            {/* Acknowledge is the common answer to a firing alert; Resolve
                closes it by hand, so it is the quieter of the two. */}
            <div className="flex gap-1">
              {alert.state === "firing" && (
                <Button variant="outline" size="sm" onClick={onAcknowledge}>
                  {t("alertsTable.acknowledge")}
                </Button>
              )}
              {(alert.state === "firing" || alert.state === "acknowledged") && (
                <Button variant="ghost" size="sm" onClick={onResolve}>
                  {t("alertsTable.resolve")}
                </Button>
              )}
            </div>
          </TableCell>
        )}
      </TableRow>

      {expanded && (
        <ExpandedDetailRow colSpan={canAcknowledge ? 8 : 7}>
          <DefGrid>
            <DetailField label={t("alertsTable.currentValue")}>
              {alert.current_value.toFixed(2)}
            </DetailField>
            <DetailField label={t("alertsTable.threshold")}>
              {alert.threshold.toFixed(2)}
            </DetailField>
            <DetailField label={t("alertsTable.escalationLevel")}>
              {alert.escalation_level}
            </DetailField>
            <DetailField label={t("alertsTable.pendingAt")}>
              {formatDate(alert.pending_at)}
            </DetailField>
            <DetailField label={t("alertsTable.firedAt")}>
              {formatDate(alert.fired_at)}
            </DetailField>
            <DetailField label={t("alertsTable.acknowledgedAt")}>
              {formatDate(alert.acknowledged_at)}
            </DetailField>
            <DetailField label={t("alertsTable.resolvedAt")}>
              {formatDate(alert.resolved_at)}
            </DetailField>
          </DefGrid>
          <DefGrid className="grid-cols-[repeat(auto-fit,minmax(min(19rem,100%),1fr))]">
            <DetailField label={t("alertsTable.alertID")} variant="mono">
              <span className="text-xs">{alert.id}</span>
            </DetailField>
            <DetailField label={t("alertsTable.ruleID")} variant="mono">
              <span className="text-xs">{alert.rule_id}</span>
            </DetailField>
            {alert.cluster_id && (
              <DetailField label={t("alertsTable.cluster")} variant="mono">
                <span className="text-xs">{alert.cluster_id}</span>
              </DetailField>
            )}
          </DefGrid>
        </ExpandedDetailRow>
      )}
    </>
  );
}
