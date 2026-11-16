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

interface DRSRulesTableProps {
  clusterId: string;
  /** Controls for the panel's title row: the Add Rule dialog. */
  actions?: ReactNode;
}

export function DRSRulesTable({ clusterId, actions }: DRSRulesTableProps) {
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
        title="Placement rules"
        aside={
          allRules.length > 0
            ? `${String(allRules.length)}, DRS and Proxmox HA rules together`
            : undefined
        }
        actions={actions}
      >
        <QueryFailureNote
          query={manualQuery}
          subject="this cluster's DRS rules"
          identity={clusterId}
          className="mb-2 px-[18px]"
        />
        <QueryFailureNote
          query={haQuery}
          subject="this cluster's HA rules"
          identity={clusterId}
          className="mb-2 px-[18px]"
        />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Type</TableHead>
              <TableHead>VM IDs</TableHead>
              <TableHead>Node names</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-24">
                <span className="sr-only">Actions</span>
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
                    ? "Loading rules..."
                    : bothRead
                      ? "No rules configured"
                      : eitherFailed
                        ? "Rules could not be listed"
                        : "Rules have not been read yet"}
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
                      {rule.source === "ha" ? "HA" : "Manual"}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Badge variant={rule.enabled ? "healthy" : "secondary"}>
                      {rule.enabled ? "Enabled" : "Disabled"}
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
                        title={`${rule.rule_type} rule`}
                      />
                    )}
                    <Button
                      aria-label={`Delete ${rule.rule_type} rule for ${rule.ha_rule_name ?? (rule.vm_ids ?? []).join(", ")}`}
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
            <DialogTitle>Delete Rule</DialogTitle>
            <DialogDescription>
              {deleteTarget?.source === "ha"
                ? "This will delete the HA rule from Proxmox. This action cannot be undone."
                : "Are you sure you want to delete this rule? This action cannot be undone."}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setDeleteTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={isDeleting}
            >
              {isDeleting ? "Deleting..." : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
