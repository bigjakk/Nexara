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

interface FirewallRulesTableProps {
  clusterId: string;
}

export function FirewallRulesTable({ clusterId }: FirewallRulesTableProps) {
  const rulesQuery = useClusterFirewallRules(clusterId);
  const rules = rulesQuery.data;
  const deleteRule = useDeleteClusterFirewallRule(clusterId);
  const [pendingDelete, setPendingDelete] = useState<FirewallRule | null>(null);

  const disabled = (rules ?? []).filter((r) => !r.enable).length;

  return (
    <>
      <Panel
        flush
        title="Cluster rules"
        aside={
          !rules || rules.length === 0
            ? undefined
            : `${String(rules.length)} ${rules.length === 1 ? "rule" : "rules"}${disabled > 0 ? `, ${String(disabled)} disabled` : ""}`
        }
        actions={<CreateFirewallRuleDialog clusterId={clusterId} />}
      >
        {!rules || rules.length === 0 ? (
          <div className="px-[18px] pb-1">
            <QueryStateNotice
              query={rulesQuery}
              subject="the cluster firewall rules"
              empty="No firewall rules configured."
            />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-12">#</TableHead>
                <TableHead>Direction</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Protocol</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Destination</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Macro</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
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
                  <TableCell>{rule.proto || "any"}</TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.source || "any"}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.dest || "any"}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.dport || "-"}
                  </TableCell>
                  <TableCell>{rule.macro || "-"}</TableCell>
                  <TableCell>
                    <Badge variant={rule.enable ? "healthy" : "secondary"}>
                      {rule.enable ? "Enabled" : "Disabled"}
                    </Badge>
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate text-muted-foreground">
                    {rule.comment || "-"}
                  </TableCell>
                  <TableCell className="py-1 text-right">
                    <Button
                      aria-label={`Delete rule ${String(rule.pos)}`}
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
        owner="the cluster"
        current={rules}
      />
    </>
  );
}
