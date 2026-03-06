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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useDRSRules, useDeleteDRSRule } from "../api/drs-queries";
import { Trash2 } from "lucide-react";

interface DRSRulesTableProps {
  clusterId: string;
}

export function DRSRulesTable({ clusterId }: DRSRulesTableProps) {
  const { data: rules, isLoading } = useDRSRules(clusterId);
  const deleteRule = useDeleteDRSRule(clusterId);
  const [deleteId, setDeleteId] = useState<string | null>(null);

  if (isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }

  const handleDelete = () => {
    if (deleteId) {
      deleteRule.mutate(deleteId, {
        onSuccess: () => setDeleteId(null),
      });
    }
  };

  return (
    <>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Type</TableHead>
              <TableHead>VM IDs</TableHead>
              <TableHead>Node Names</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {!rules || rules.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="text-center text-muted-foreground"
                >
                  No rules configured
                </TableCell>
              </TableRow>
            ) : (
              rules.map((rule) => (
                <TableRow key={rule.id}>
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
                    {rule.vm_ids.join(", ")}
                  </TableCell>
                  <TableCell className="text-sm">
                    {rule.node_names.length > 0
                      ? rule.node_names.join(", ")
                      : "-"}
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
                      onClick={() => setDeleteId(rule.id)}
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

      <Dialog open={deleteId !== null} onOpenChange={() => setDeleteId(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Rule</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete this rule? This action cannot be
              undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteId(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteRule.isPending}
            >
              {deleteRule.isPending ? "Deleting..." : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
