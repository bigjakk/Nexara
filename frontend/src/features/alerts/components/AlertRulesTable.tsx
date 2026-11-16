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
import { UPS_METRICS } from "../lib/ups-metrics";
import { useTranslation, Trans } from "react-i18next";

export function AlertRulesTable() {
  const { t } = useTranslation("alerts");
  const { data: rules, isLoading } = useAlertRules();
  const updateMutation = useUpdateAlertRule();
  const deleteMutation = useDeleteAlertRule();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "alert");
  const [pendingDelete, setPendingDelete] = useState<AlertRule | null>(null);
  // A UPS's rule is named by its UPS, read only where the caller may list them.
  const { hasGlobalPermission } = usePermissions();
  const canViewUps = hasGlobalPermission("view", "ups");
  const upsDevices = useUpsDevices(
    canViewUps && (rules ?? []).some((r) => r.scope_type === "ups"),
  );
  // The server refuses to enable a UPS rule without a global view:ups (its
  // alerts name a UPS); turning one off stays open, so only Enable is withheld.
  const enableRefused = (rule: AlertRule) =>
    !rule.enabled && !canViewUps && UPS_METRICS.has(rule.metric);
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
          : t("alertRulesTable.rulesEnabled", {
              count: rules.length,
              enabled: String(enabledCount),
            })
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("alertRulesTable.name")}</TableHead>
            <TableHead>{t("alertRulesTable.severity")}</TableHead>
            <TableHead>{t("alertRulesTable.metric")}</TableHead>
            <TableHead>{t("alertRulesTable.condition")}</TableHead>
            <TableHead>{t("alertRulesTable.scope")}</TableHead>
            <TableHead>{t("alertRulesTable.duration")}</TableHead>
            <TableHead>{t("alertRulesTable.enabled")}</TableHead>
            {canManage && (
              <TableHead className="w-12">
                <span className="sr-only">{t("alertRulesTable.actions")}</span>
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
                {t("alertRulesTable.loading")}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && (!rules || rules.length === 0) && (
            <TableRow>
              <TableCell
                colSpan={canManage ? 8 : 7}
                className="py-8 text-center text-muted-foreground"
              >
                {t("alertRulesTable.noAlertRulesConfigured")}
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
                  <Trans
                    t={t}
                    i18nKey="alertRulesTable.upsSpanUpsIdSpan"
                    components={{
                      ups: (
                        <span className="font-mono">
                          {upsName(rule.ups_id)}
                        </span>
                      ),
                    }}
                  />
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
                {canManage && enableRefused(rule) ? (
                  <div>
                    <span>{t("alertRulesTable.no")}</span>
                    <div className="text-[12.5px] text-muted-foreground">
                      {t("alertRulesTable.enablingItTakesViewUps")}
                    </div>
                  </div>
                ) : canManage ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-8 px-2"
                    aria-label={
                      rule.enabled
                        ? t("alertRulesTable.disableRule")
                        : t("alertRulesTable.enableRule")
                    }
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
                  <span>
                    {rule.enabled
                      ? t("alertRulesTable.yes")
                      : t("alertRulesTable.no")}
                  </span>
                )}
              </TableCell>
              {canManage && (
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-8 px-2 text-muted-foreground hover:text-red-700 dark:hover:text-red-400"
                    aria-label={t("alertRulesTable.deleteRule")}
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
        title={(rule) =>
          t("alertRulesTable.deleteAlertRuleName", { name: rule.name })
        }
        // alert_history.rule_id is ON DELETE CASCADE (migration 000022), so
        // the rule's alert history goes with it.
        description={() => t("alertRulesTable.theRuleIsNoLonger")}
      />
    </Panel>
  );
}
