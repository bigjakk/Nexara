import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";

import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ConfirmFirewallRuleDeleteDialog } from "@/features/networks/components/ConfirmFirewallRuleDeleteDialog";
import {
  useCreateNodeFirewallRule,
  useDeleteNodeFirewallRule,
  useNodeFirewallLog,
  useNodeFirewallRules,
  type NodeFirewallRuleResponse,
} from "../../api/cluster-queries";
import { NoValue } from "./StateText";

export function FirewallTab({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const [showLog, setShowLog] = useState(false);
  const rulesQuery = useNodeFirewallRules(clusterId, nodeName);
  const rules = rulesQuery.data;
  const deleteRule = useDeleteNodeFirewallRule(clusterId, nodeName);
  const [pendingDelete, setPendingDelete] =
    useState<NodeFirewallRuleResponse | null>(null);
  const createRule = useCreateNodeFirewallRule(clusterId, nodeName);
  const logQuery = useNodeFirewallLog(clusterId, nodeName);
  const logEntries = logQuery.data;
  // A quick-add button only sets this; the rule is sent from its confirmation.
  const [pendingQuickAdd, setPendingQuickAdd] = useState<
    "ACCEPT" | "DROP" | null
  >(null);
  const count = rules?.length ?? 0;

  // The rule names no source, destination, protocol or port, so it matches
  // every inbound packet, and Proxmox's create_rule puts it first in the list
  // (see internal/proxmox/client_firewall.go).
  const handleQuickAdd = (action: "ACCEPT" | "DROP") => {
    createRule.mutate({
      type: "in",
      action,
      enable: 1,
    });
  };

  return (
    <div className="space-y-4">
      <Panel
        flush
        title="Firewall rules"
        aside={
          count > 0
            ? `${String(count)} ${count === 1 ? "rule" : "rules"}, checked from the top`
            : undefined
        }
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5"
              onClick={() => {
                setPendingQuickAdd("ACCEPT");
              }}
              disabled={createRule.isPending}
            >
              <Plus className="h-4 w-4" />
              Add accept rule
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5"
              onClick={() => {
                setPendingQuickAdd("DROP");
              }}
              disabled={createRule.isPending}
            >
              <Plus className="h-4 w-4" />
              Add drop rule
            </Button>
            <Button
              variant="ghost"
              size="sm"
              aria-expanded={showLog}
              onClick={() => {
                setShowLog(!showLog);
              }}
            >
              {showLog ? "Hide log" : "Show log"}
            </Button>
          </>
        }
      >
        {!rules || rules.length === 0 ? (
          <div className="px-[18px] pb-1">
            <QueryStateNotice
              query={rulesQuery}
              subject="this node's firewall rules"
              empty="No firewall rules configured."
              skeletonClassName="h-32 w-full"
            />
          </div>
        ) : (
          <Table className="whitespace-nowrap">
            <TableHeader>
              <TableRow>
                <TableHead>#</TableHead>
                <TableHead>Direction</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Macro</TableHead>
                <TableHead>Protocol</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Destination</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((rule) => (
                <TableRow
                  key={rule.pos}
                  className={rule.enable ? undefined : "text-muted-foreground"}
                >
                  <TableCell className="font-mono text-[12.5px] text-muted-foreground">
                    {rule.pos}
                  </TableCell>
                  <TableCell>{rule.type}</TableCell>
                  <TableCell className="font-medium">{rule.action}</TableCell>
                  <TableCell>{rule.macro || <NoValue />}</TableCell>
                  <TableCell>{rule.proto || <NoValue />}</TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.source || <NoValue />}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.dest || <NoValue />}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {rule.dport || rule.sport || <NoValue />}
                  </TableCell>
                  <TableCell>{rule.enable ? "Yes" : "No"}</TableCell>
                  <TableCell className="min-w-[10rem] whitespace-normal text-muted-foreground">
                    {rule.comment || <NoValue />}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      aria-label={`Delete firewall rule ${String(rule.pos)}`}
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        setPendingDelete(rule);
                      }}
                      disabled={deleteRule.isPending}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      {showLog && (
        <Panel title="Firewall log">
          {!logEntries || logEntries.length === 0 ? (
            <QueryStateNotice
              query={logQuery}
              subject="this node's firewall log"
              empty="No log entries."
              skeletonClassName="h-32 w-full"
            />
          ) : (
            <div className="max-h-[400px] overflow-auto rounded-md bg-foreground/[0.05] p-3">
              <pre className="text-xs leading-relaxed">
                {logEntries.map((entry) => (
                  <div key={entry.n}>{entry.t}</div>
                ))}
              </pre>
            </div>
          )}
        </Panel>
      )}

      <ConfirmFirewallRuleDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(rule) => {
          deleteRule.mutate({ pos: rule.pos, digest: rule.digest });
        }}
        owner={`node ${nodeName}`}
        current={rules}
      />

      <ConfirmDeleteDialog
        target={pendingQuickAdd}
        onClose={() => {
          setPendingQuickAdd(null);
        }}
        onConfirm={handleQuickAdd}
        title={(action) =>
          action === "DROP"
            ? `Drop all inbound traffic to ${nodeName}?`
            : `Accept all inbound traffic to ${nodeName}?`
        }
        description={(action) =>
          action === "DROP"
            ? `Proxmox adds an enabled DROP rule at the top of the rule list of node ${nodeName} that matches every inbound packet: any source, protocol and port. The firewall service applies it within about 10 seconds. While the firewall is on for ${nodeName}, this can cut off access to the node — its web UI and API, SSH, and Nexara's own connection to it.`
            : `Proxmox adds an enabled ACCEPT rule at the top of the rule list of node ${nodeName} that matches every inbound packet: any source, protocol and port. The firewall service applies it within about 10 seconds. Rules are checked from the top, so while the firewall is on for ${nodeName} it lets every inbound connection to the node through, whatever the rules below it and the inbound policy say. To undo it, delete the rule.`
        }
        confirmLabel={(action) =>
          action === "DROP" ? "Add drop rule" : "Add accept rule"
        }
      />
    </div>
  );
}
