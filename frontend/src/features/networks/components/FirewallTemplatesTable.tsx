import { useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { Skeleton } from "@/components/ui/skeleton";
import { Trash2 } from "lucide-react";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  useFirewallTemplates,
  useDeleteFirewallTemplate,
} from "../api/network-queries";
import { CreateTemplateDialog } from "./CreateTemplateDialog";
import { ApplyTemplateDialog } from "./ApplyTemplateDialog";
import type { FirewallTemplate } from "../types/network";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { DateTime } from "@/components/DateTime";

interface FirewallTemplatesTableProps {
  clusterId: string;
}

export function FirewallTemplatesTable({
  clusterId,
}: FirewallTemplatesTableProps) {
  const { data: templates, isLoading } = useFirewallTemplates();
  const deleteTemplate = useDeleteFirewallTemplate();
  const [pendingDelete, setPendingDelete] = useState<FirewallTemplate | null>(
    null,
  );

  const count = templates?.length ?? 0;

  return (
    <>
      <Panel
        flush
        title="Templates"
        aside={
          isLoading
            ? "Loading..."
            : count === 0
              ? "Reusable rule sets, shared by every cluster"
              : `${String(count)} ${count === 1 ? "template" : "templates"}, shared by every cluster`
        }
        actions={<CreateTemplateDialog />}
      >
        {isLoading ? (
          <div className="px-[18px] pb-1">
            <Skeleton className="h-20 w-full" />
          </div>
        ) : !templates || templates.length === 0 ? (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            No firewall templates. Create one to define reusable rule sets.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Rules</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {templates.map((tmpl: FirewallTemplate) => (
                <TableRow key={tmpl.id}>
                  <TableCell className="font-medium">
                    {tmpl.name}
                    <ClusterHistoryButton
                      history={clusterHistories.firewallTemplate(tmpl.id)}
                      title={tmpl.name}
                    />
                  </TableCell>
                  <TableCell className="max-w-[300px] truncate text-muted-foreground">
                    {tmpl.description || "-"}
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {tmpl.rules.length} rule
                    {tmpl.rules.length !== 1 ? "s" : ""}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    <DateTime value={tmpl.updated_at} kind="date" />
                  </TableCell>
                  <TableCell className="py-1">
                    <div className="flex items-center justify-end gap-1">
                      <ApplyTemplateDialog
                        clusterId={clusterId}
                        template={tmpl}
                      />
                      <Button
                        aria-label={`Delete template ${tmpl.name}`}
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-muted-foreground hover:text-destructive"
                        onClick={() => {
                          setPendingDelete(tmpl);
                        }}
                        disabled={deleteTemplate.isPending}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      {/* A template is a Nexara row, not Proxmox state: the handler
          (NetworkHandler.DeleteTemplate) deletes it from firewall_templates
          and nothing else. Apply copied its rules into the cluster firewall
          as ordinary rules (CreateClusterFirewallRule, one per rule) and kept
          no link back to the template, so those rules stay. Templates are not
          per-cluster: /api/v1/firewall-templates is one list. */}
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(tmpl) => {
          deleteTemplate.mutate(tmpl.id);
        }}
        title={(tmpl) => `Delete firewall template ${tmpl.name}?`}
        description={(tmpl) =>
          `Nexara deletes the template ${tmpl.name} and its ${String(tmpl.rules.length)} rule${tmpl.rules.length !== 1 ? "s" : ""} for every cluster. Rules already applied from it are ordinary Proxmox firewall rules and stay in place. It cannot be undone: to get the template back, create it again.`
        }
        confirmLabel="Delete Template"
      />
    </>
  );
}
