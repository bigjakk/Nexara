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
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  useFirewallTemplates,
  useDeleteFirewallTemplate,
} from "../api/network-queries";
import { CreateTemplateDialog } from "./CreateTemplateDialog";
import { ApplyTemplateDialog } from "./ApplyTemplateDialog";
import type { FirewallTemplate } from "../types/network";

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

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <CreateTemplateDialog />
      </div>

      {!templates || templates.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No firewall templates. Create one to define reusable rule sets.
        </p>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Rules</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead className="w-32" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {templates.map((tmpl: FirewallTemplate) => (
                <TableRow key={tmpl.id}>
                  <TableCell className="font-medium">{tmpl.name}</TableCell>
                  <TableCell className="max-w-[300px] truncate text-sm text-muted-foreground">
                    {tmpl.description || "-"}
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">
                      {tmpl.rules.length} rule
                      {tmpl.rules.length !== 1 ? "s" : ""}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {new Date(tmpl.updated_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1">
                      <ApplyTemplateDialog
                        clusterId={clusterId}
                        template={tmpl}
                      />
                      <Button
                        aria-label={`Delete template ${tmpl.name}`}
                        variant="ghost"
                        size="icon"
                        onClick={() => {
                          setPendingDelete(tmpl);
                        }}
                        disabled={deleteTemplate.isPending}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

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
    </div>
  );
}
