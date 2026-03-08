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

export function AlertRulesTable() {
  const { data: rules, isLoading } = useAlertRules();
  const updateMutation = useUpdateAlertRule();
  const deleteMutation = useDeleteAlertRule();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "alert");

  const toggleEnabled = (id: string, currentEnabled: boolean) => {
    updateMutation.mutate({ id, name: "", metric: "", operator: "", threshold: 0, enabled: !currentEnabled });
  };

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Severity</TableHead>
            <TableHead>Metric</TableHead>
            <TableHead>Condition</TableHead>
            <TableHead>Scope</TableHead>
            <TableHead>Duration</TableHead>
            <TableHead>Enabled</TableHead>
            {canManage && <TableHead>Actions</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell colSpan={canManage ? 8 : 7} className="text-center text-muted-foreground py-8">
                Loading...
              </TableCell>
            </TableRow>
          )}
          {!isLoading && (!rules || rules.length === 0) && (
            <TableRow>
              <TableCell colSpan={canManage ? 8 : 7} className="text-center text-muted-foreground py-8">
                No alert rules configured
              </TableCell>
            </TableRow>
          )}
          {rules?.map((rule) => (
            <TableRow key={rule.id}>
              <TableCell>
                <div>
                  <div className="font-medium">{rule.name}</div>
                  {rule.description && (
                    <div className="text-xs text-muted-foreground">{rule.description}</div>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <AlertSeverityBadge severity={rule.severity} />
              </TableCell>
              <TableCell className="text-sm">{rule.metric}</TableCell>
              <TableCell className="font-mono text-sm">
                {rule.operator} {rule.threshold}
              </TableCell>
              <TableCell className="text-sm capitalize">{rule.scope_type}</TableCell>
              <TableCell className="text-sm">
                {rule.duration_seconds >= 60
                  ? `${String(Math.floor(rule.duration_seconds / 60))}m`
                  : `${String(rule.duration_seconds)}s`}
              </TableCell>
              <TableCell>
                {canManage ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => { toggleEnabled(rule.id, rule.enabled); }}
                    disabled={updateMutation.isPending}
                  >
                    {rule.enabled ? (
                      <ToggleRight className="h-5 w-5 text-green-500" />
                    ) : (
                      <ToggleLeft className="h-5 w-5 text-muted-foreground" />
                    )}
                  </Button>
                ) : (
                  <span className={rule.enabled ? "text-green-500" : "text-muted-foreground"}>
                    {rule.enabled ? "Yes" : "No"}
                  </span>
                )}
              </TableCell>
              {canManage && (
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => { deleteMutation.mutate(rule.id); }}
                    disabled={deleteMutation.isPending}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
