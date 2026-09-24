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

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <CreateFirewallRuleDialog clusterId={clusterId} />
      </div>

      {!rules || rules.length === 0 ? (
        <QueryStateNotice
          query={rulesQuery}
          subject="the cluster firewall rules"
          empty="No firewall rules configured."
        />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-16">#</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Protocol</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Destination</TableHead>
                <TableHead>D.Port</TableHead>
                <TableHead>Macro</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-16" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((rule) => (
                <TableRow key={rule.pos}>
                  <TableCell>{rule.pos}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{rule.type}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        rule.action === "ACCEPT" ? "default" : "destructive"
                      }
                    >
                      {rule.action}
                    </Badge>
                  </TableCell>
                  <TableCell>{rule.proto || "any"}</TableCell>
                  <TableCell className="font-mono text-sm">
                    {rule.source || "any"}
                  </TableCell>
                  <TableCell className="font-mono text-sm">
                    {rule.dest || "any"}
                  </TableCell>
                  <TableCell className="font-mono text-sm">
                    {rule.dport || "-"}
                  </TableCell>
                  <TableCell>{rule.macro || "-"}</TableCell>
                  <TableCell>
                    <Badge variant={rule.enable ? "default" : "secondary"}>
                      {rule.enable ? "Enabled" : "Disabled"}
                    </Badge>
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate text-sm text-muted-foreground">
                    {rule.comment || "-"}
                  </TableCell>
                  <TableCell>
                    <Button
                      aria-label={`Delete rule ${String(rule.pos)}`}
                      variant="ghost"
                      size="icon"
                      onClick={() => {
                        setPendingDelete(rule);
                      }}
                      disabled={deleteRule.isPending}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <ConfirmFirewallRuleDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(rule) => {
          deleteRule.mutate(rule.pos);
        }}
        owner="the cluster"
        current={rules}
      />
    </div>
  );
}
