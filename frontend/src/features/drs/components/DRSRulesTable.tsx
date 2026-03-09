import { useMemo, useState } from "react";
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
import { Skeleton } from "@/components/ui/skeleton";
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
}

export function DRSRulesTable({ clusterId }: DRSRulesTableProps) {
  const { data: manualRules, isLoading: loadingManual } =
    useDRSRules(clusterId);
  const { data: haRules, isLoading: loadingHA } = useHARules(clusterId);
  const deleteManualRule = useDeleteDRSRule(clusterId);
  const deleteHARule = useDeleteHARule(clusterId);
  const [deleteTarget, setDeleteTarget] = useState<DRSRule | null>(null);

  const allRules = useMemo(() => {
    const normalize = (r: DRSRule): DRSRule => ({
      ...r,
      vm_ids: r.vm_ids ?? [],
      node_names: r.node_names ?? [],
      source: r.source,
    });
    const manual = (manualRules ?? []).map(normalize);
    const ha = (haRules ?? []).map(normalize);
    return [...manual, ...ha];
  }, [manualRules, haRules]);

  const isLoading = loadingManual || loadingHA;

  if (isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }

  const handleDelete = () => {
    if (!deleteTarget) return;

    if (deleteTarget.source === "ha" && deleteTarget.ha_rule_name) {
      deleteHARule.mutate(deleteTarget.ha_rule_name, {
        onSuccess: () => { setDeleteTarget(null); },
      });
    } else {
      deleteManualRule.mutate(deleteTarget.id, {
        onSuccess: () => { setDeleteTarget(null); },
      });
    }
  };

  const isDeleting = deleteManualRule.isPending || deleteHARule.isPending;

  return (
    <>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Type</TableHead>
              <TableHead>VM IDs</TableHead>
              <TableHead>Node Names</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {allRules.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="text-center text-muted-foreground"
                >
                  No rules configured
                </TableCell>
              </TableRow>
            ) : (
              allRules.map((rule, idx) => (
                <TableRow key={rule.id || `ha-${rule.ha_rule_name ?? String(idx)}`}>
                  <TableCell>
                    <Badge
                      variant={
                        rule.rule_type === "affinity"
                          ? "default"
                          : rule.rule_type === "anti-affinity"
                            ? "destructive"
                            : "secondary"
                      }
                    >
                      {rule.rule_type}
                    </Badge>
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
                    <Badge
                      variant={
                        rule.source === "ha" ? "outline" : "default"
                      }
                    >
                      {rule.source === "ha" ? "HA" : "Manual"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={rule.enabled ? "default" : "secondary"}>
                      {rule.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => { setDeleteTarget(rule); }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={() => { setDeleteTarget(null); }}
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
            <Button variant="outline" onClick={() => { setDeleteTarget(null); }}>
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
