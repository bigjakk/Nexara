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

interface FirewallRulesTableProps {
  clusterId: string;
}

export function FirewallRulesTable({ clusterId }: FirewallRulesTableProps) {
  const { data: rules, isLoading } = useClusterFirewallRules(clusterId);
  const deleteRule = useDeleteClusterFirewallRule(clusterId);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <CreateFirewallRuleDialog clusterId={clusterId} />
      </div>

      {!rules || rules.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No firewall rules configured.
        </p>
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
                      variant="ghost"
                      size="icon"
                      onClick={() => { deleteRule.mutate(rule.pos); }}
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
    </div>
  );
}
