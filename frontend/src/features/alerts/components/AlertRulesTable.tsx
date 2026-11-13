import { useState } from "react";
import { Trash2, ToggleLeft, ToggleRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { AlertSeverityBadge } from "./AlertSeverityBadge";
import {
  useAlertRules,
  useUpdateAlertRule,
  useDeleteAlertRule,
} from "../api/alert-queries";
import { useAuth } from "@/hooks/useAuth";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { Panel } from "@/components/page/Panel";
import type { AlertRule } from "@/types/api";
import { useUpsDevices } from "@/features/power/api/ups-queries";
import { usePermissions } from "@/hooks/usePermissions";

export function AlertRulesTable() {
  const { data: rules, isLoading } = useAlertRules();
  const updateMutation = useUpdateAlertRule();
  const deleteMutation = useDeleteAlertRule();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "alert");
  const [pendingDelete, setPendingDelete] = useState<AlertRule | null>(null);
  // A UPS's rule is named by its UPS, read only where the caller may list them.
  const { hasGlobalPermission } = usePermissions();
  const upsDevices = useUpsDevices(
    hasGlobalPermission("view", "ups") && (rules ?? []).some((r) => r.scope_type === "ups"),
  );
  const upsName = (id: string | undefined) => {
    const d = upsDevices.data?.find((u) => u.id === id);
    return d === undefined ? "" : `${d.name}@${d.server_name}`;
  };

  const toggleEnabled = (id: string, currentEnabled: boolean) => {
    updateMutation.mutate({ id, enabled: !currentEnabled });
  };

  const enabledCount = (rules ?? []).filter((r) => r.enabled).length;

  return (
    <Panel
      flush
      aside={
        rules === undefined || rules.length === 0
          ? undefined
          : `${String(rules.length)} ${rules.length === 1 ? "rule" : "rules"}, ${String(enabledCount)} enabled`
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Severity</TableHead>
            <TableHead>Metric</TableHead>
            <TableHead>Condition</TableHead>
            <TableHead>Scope</TableHead>
            <TableHead>Duration</TableHead>
            <TableHead>Enabled</TableHead>
            {canManage && (
              <TableHead className="w-12">
                <span className="sr-only">Actions</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell
                colSpan={canManage ? 8 : 7}
                className="py-8 text-center text-muted-foreground"
              >
                Loading...
              </TableCell>
            </TableRow>
          )}
          {!isLoading && (!rules || rules.length === 0) && (
            <TableRow>
              <TableCell
                colSpan={canManage ? 8 : 7}
                className="py-8 text-center text-muted-foreground"
              >
                No alert rules configured
              </TableCell>
            </TableRow>
          )}
          {rules?.map((rule) => (
            <TableRow
              key={rule.id}
              className={rule.enabled ? undefined : "text-muted-foreground"}
            >
              <TableCell>
                <div>
                  <div className="font-medium">{rule.name}</div>
                  {rule.description && (
                    <div className="text-[12.5px] text-muted-foreground">
                      {rule.description}
                    </div>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <AlertSeverityBadge severity={rule.severity} />
              </TableCell>
              <TableCell>{rule.metric}</TableCell>
              <TableCell className="whitespace-nowrap font-mono text-[13px]">
                {rule.operator} {rule.threshold}
              </TableCell>
              {rule.scope_type === "ups" ? (
                <TableCell>
                  UPS <span className="font-mono">{upsName(rule.ups_id)}</span>
                </TableCell>
              ) : (
                <TableCell className="capitalize">{rule.scope_type}</TableCell>
              )}
              <TableCell>
                {rule.duration_seconds >= 60
                  ? `${String(Math.floor(rule.duration_seconds / 60))}m`
                  : `${String(rule.duration_seconds)}s`}
              </TableCell>
              <TableCell>
                {canManage ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-8 px-2"
                    aria-label={rule.enabled ? "Disable rule" : "Enable rule"}
                    onClick={() => {
                      toggleEnabled(rule.id, rule.enabled);
                    }}
                    disabled={updateMutation.isPending}
                  >
                    {rule.enabled ? (
                      <ToggleRight className="h-5 w-5 text-primary" />
                    ) : (
                      <ToggleLeft className="h-5 w-5 text-muted-foreground" />
                    )}
                  </Button>
                ) : (
                  <span>{rule.enabled ? "Yes" : "No"}</span>
                )}
              </TableCell>
              {canManage && (
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-8 px-2 text-muted-foreground hover:text-red-700 dark:hover:text-red-400"
                    aria-label="Delete rule"
                    onClick={() => {
                      setPendingDelete(rule);
                    }}
                    disabled={deleteMutation.isPending}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(rule) => {
          deleteMutation.mutate(rule.id);
        }}
        title={(rule) => `Delete alert rule ${rule.name}?`}
        // alert_history.rule_id is ON DELETE CASCADE (migration 000022), so
        // the rule's alert history goes with it.
        description={() =>
          "The rule is no longer evaluated, and every alert it has raised — firing, acknowledged and resolved — is deleted with it. This cannot be undone."
        }
      />
    </Panel>
  );
}
