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
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { Trash2 } from "lucide-react";
import {
  useClusterFirewallRules,
  useDeleteClusterFirewallRule,
} from "../api/network-queries";
import { CreateFirewallRuleDialog } from "./CreateFirewallRuleDialog";
import { ConfirmFirewallRuleDeleteDialog } from "./ConfirmFirewallRuleDeleteDialog";
import type { FirewallRule } from "../types/network";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { useTranslation } from "react-i18next";

interface FirewallRulesTableProps {
  clusterId: string;
}

export function FirewallRulesTable({ clusterId }: FirewallRulesTableProps) {
  const { t } = useTranslation("networks");
  const rulesQuery = useClusterFirewallRules(clusterId);
  const rules = rulesQuery.data;
  const deleteRule = useDeleteClusterFirewallRule(clusterId);
  const [pendingDelete, setPendingDelete] = useState<FirewallRule | null>(null);

  const disabled = (rules ?? []).filter((r) => !r.enable).length;

  return (
    <>
      <Panel
        flush
        title={t("firewallRulesTable.clusterRules")}
        aside={
          !rules || rules.length === 0
            ? undefined
            : disabled > 0
              ? t("firewallRulesTable.rulesDisabled", {
                  count: rules.length,
                  disabled: String(disabled),
                })
              : t("firewallRulesTable.rulesCount", { count: rules.length })
        }
        actions={<CreateFirewallRuleDialog clusterId={clusterId} />}
      >
        {!rules || rules.length === 0 ? (
          <div className="px-[18px] pb-1">
            <QueryStateNotice
              query={rulesQuery}
              subject={t("firewallRulesTable.theClusterFirewallRules")}
              empty={t("firewallRulesTable.noFirewallRulesConfigured")}
            />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-12">#</TableHead>
                <TableHead>{t("firewallRulesTable.direction")}</TableHead>
                <TableHead>{t("firewallRulesTable.action")}</TableHead>
                <TableHead>{t("firewallRulesTable.protocol")}</TableHead>
                <TableHead>{t("firewallRulesTable.source")}</TableHead>
                <TableHead>{t("firewallRulesTable.destination")}</TableHead>
                <TableHead>{t("firewallRulesTable.port")}</TableHead>
                <TableHead>{t("firewallRulesTable.macro")}</TableHead>
                <TableHead>{t("firewallRulesTable.state")}</TableHead>
                <TableHead>{t("firewallRulesTable.comment")}</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">
                    {t("firewallRulesTable.actions")}
                  </span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((rule) => (
                <TableRow
                  key={rule.pos}
                  className={rule.enable ? undefined : "text-muted-foreground"}
                >
                  <TableCell className="tabular-nums text-muted-foreground">
                    {rule.pos}
                  </TableCell>
                  <TableCell>{rule.type}</TableCell>
                  <TableCell className="font-mono text-[12.5px] font-semibold">
                    {rule.action}
                  </TableCell>
                  <TableCell>
                    {rule.proto || t("firewallRulesTable.any")}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.source || t("firewallRulesTable.any")}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.dest || t("firewallRulesTable.any")}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.dport || "-"}
                  </TableCell>
                  <TableCell>{rule.macro || "-"}</TableCell>
                  <TableCell>
                    <Badge variant={rule.enable ? "healthy" : "secondary"}>
                      {rule.enable
                        ? t("firewallRulesTable.enabled")
                        : t("firewallRulesTable.disabled")}
                    </Badge>
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate text-muted-foreground">
                    {rule.comment || "-"}
                  </TableCell>
                  <TableCell className="py-1 text-right">
                    <Button
                      aria-label={t("firewallRulesTable.deleteRulePos", {
                        pos: String(rule.pos),
                      })}
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        setPendingDelete(rule);
                      }}
                      disabled={deleteRule.isPending}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      <ConfirmFirewallRuleDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(rule) => {
          deleteRule.mutate({ pos: rule.pos, digest: rule.digest });
        }}
        owner={t("firewallRulesTable.theCluster")}
        current={rules}
      />
    </>
  );
}
