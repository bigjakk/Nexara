import { useMemo, useState, type ReactNode } from "react";
import { Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { QueryFailureNote } from "@/components/QueryStateNotice";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { ApiClientError } from "@/lib/api-client";
import {
  useDRSRules,
  useDeleteDRSRule,
  useHARules,
  useDeleteHARule,
} from "../api/drs-queries";
import type { DRSRule } from "../types/drs";
import { Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";

interface DRSRulesTableProps {
  clusterId: string;
  /** Controls for the panel's title row: the Add Rule dialog. */
  actions?: ReactNode;
}

export function DRSRulesTable({ clusterId, actions }: DRSRulesTableProps) {
  const { t } = useTranslation("clusters");
  const manualQuery = useDRSRules(clusterId);
  const haQuery = useHARules(clusterId);
  const manualRules = manualQuery.data;
  const haRules = haQuery.data;
  const deleteManualRule = useDeleteDRSRule(clusterId);
  const deleteHARule = useDeleteHARule(clusterId);
  const [deleteTarget, setDeleteTarget] = useState<DRSRule | null>(null);

  const allRules = useMemo(() => {
    const normalize = (r: DRSRule): DRSRule => ({
      ...r,
      vm_ids: r.vm_ids,
      node_names: r.node_names,
      source: r.source,
    });
    const manual = (manualRules ?? []).map(normalize);
    const ha = (haRules ?? []).map(normalize);
    return [...manual, ...ha];
  }, [manualRules, haRules]);

  // Both sources have to be readable before an empty table means anything.
  // `haRules ?? []` absorbs a failed HA read into the merge, so a token that
  // cannot list HA rules used to leave an operator looking at a table that
  // said, without qualification, that the cluster has no rules.
  const bothRead = manualQuery.isSuccess && haQuery.isSuccess;
  const stillLoading = manualQuery.isLoading || haQuery.isLoading;
  // errorUpdatedAt, not isError: TanStack clears `error` on every refetch of a
  // query with no data, but leaves the timestamp. Without this the row would
  // assert "could not be listed" for a query that is merely paused or
  // disabled — the same claim-what-you-did-not-read shape being fixed here.
  const eitherFailed =
    manualQuery.errorUpdatedAt > 0 || haQuery.errorUpdatedAt > 0;

  const handleDelete = () => {
    if (!deleteTarget) return;

    if (deleteTarget.source === "ha" && deleteTarget.ha_rule_name) {
      deleteHARule.mutate(deleteTarget.ha_rule_name, {
        onSuccess: () => {
          setDeleteTarget(null);
        },
      });
    } else {
      // Through the promise, not mutate()'s onError: the app's error toast
      // reports the failure, which it cannot see beside an onError given there.
      const id = deleteTarget.id;
      const close = () => {
        setDeleteTarget((cur) => (cur?.id === id ? null : cur));
      };
      void deleteManualRule.mutateAsync(id).then(close, (err: unknown) => {
        // A 404 means the rule is already gone — deleted from another tab
        // or by another operator. The hook refetches the list and the app's
        // mutation handler toasts the 404; the dialog has nothing left to
        // confirm, so it closes rather than offering the dead row again.
        if (err instanceof ApiClientError && err.status === 404) close();
      });
    }
  };

  const isDeleting = deleteManualRule.isPending || deleteHARule.isPending;

  return (
    <>
      <Panel
        flush
        title={t("drsRulesTable.placementRules")}
        aside={
          allRules.length > 0
            ? t("drsRulesTable.lengthDRSAndProxmoxHA", {
                length: String(allRules.length),
              })
            : undefined
        }
        actions={actions}
      >
        <QueryFailureNote
          query={manualQuery}
          subject={t("drsRulesTable.thisClusterSDRSRules")}
          identity={clusterId}
          className="mb-2 px-[18px]"
        />
        <QueryFailureNote
          query={haQuery}
          subject={t("drsRulesTable.thisClusterSHARules")}
          identity={clusterId}
          className="mb-2 px-[18px]"
        />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("drsRulesTable.type")}</TableHead>
              <TableHead>{t("drsRulesTable.vmIDs")}</TableHead>
              <TableHead>{t("drsRulesTable.nodeNames")}</TableHead>
              <TableHead>{t("drsRulesTable.source")}</TableHead>
              <TableHead>{t("drsRulesTable.status")}</TableHead>
              <TableHead className="w-24">
                <span className="sr-only">{t("drsRulesTable.actions")}</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {allRules.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="py-6 text-center text-muted-foreground"
                >
                  {stillLoading
                    ? t("drsRulesTable.loadingRules")
                    : bothRead
                      ? t("drsRulesTable.noRulesConfigured")
                      : eitherFailed
                        ? t("drsRulesTable.rulesCouldNotBeListed")
                        : t("drsRulesTable.rulesHaveNotBeenRead")}
                </TableCell>
              </TableRow>
            ) : (
              allRules.map((rule, idx) => (
                <TableRow
                  // An HA rule is keyed on its name: every one of them
                  // arrives with the same all-zero id.
                  key={
                    rule.ha_rule_name !== undefined
                      ? `ha-${rule.ha_rule_name}`
                      : rule.id || String(idx)
                  }
                >
                  <TableCell>
                    <Badge variant="outline">{rule.rule_type}</Badge>
                  </TableCell>
                  <TableCell className="font-mono text-sm">
                    {(rule.vm_ids ?? []).join(", ")}
                  </TableCell>
                  <TableCell className="text-sm">
                    {(rule.node_names ?? []).length > 0
                      ? (rule.node_names ?? []).join(", ")
                      : "-"}
                  </TableCell>
                  <TableCell>
                    <span className="text-muted-foreground">
                      {rule.source === "ha"
                        ? t("drsRulesTable.ha")
                        : t("drsRulesTable.manual")}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Badge variant={rule.enabled ? "healthy" : "secondary"}>
                      {rule.enabled
                        ? t("drsRulesTable.enabled")
                        : t("drsRulesTable.disabled")}
                    </Badge>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right">
                    {/* A Proxmox HA rule's History is the HA tab's, and needs
                        its view permission. */}
                    {rule.ha_rule_name !== undefined ? (
                      <ClusterHistoryButton
                        history={clusterHistories.haRule(
                          clusterId,
                          rule.ha_rule_name,
                        )}
                        title={rule.ha_rule_name}
                      />
                    ) : (
                      <ClusterHistoryButton
                        history={clusterHistories.drsRule(clusterId, rule.id)}
                        title={t("drsRulesTable.ruleTypeRule", {
                          ruleType: rule.rule_type,
                        })}
                      />
                    )}
                    <Button
                      aria-label={t(
                        "drsRulesTable.deleteRuleTypeRuleForHaRuleName",
                        {
                          ruleType: rule.rule_type,
                          haRuleName:
                            rule.ha_rule_name ?? (rule.vm_ids ?? []).join(", "),
                        },
                      )}
                      variant="ghost"
                      size="icon"
                      className="text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        setDeleteTarget(rule);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Panel>

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={() => {
          setDeleteTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("drsRulesTable.deleteRule")}</DialogTitle>
            <DialogDescription>
              {deleteTarget?.source === "ha"
                ? t("drsRulesTable.thisWillDeleteTheHA")
                : t("drsRulesTable.areYouSureYouWant")}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setDeleteTarget(null);
              }}
            >
              {t("drsRulesTable.cancel")}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={isDeleting}
            >
              {isDeleting
                ? t("drsRulesTable.deleting")
                : t("drsRulesTable.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
