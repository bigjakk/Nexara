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
                      {tmpl.rules.length} rule{tmpl.rules.length !== 1 ? "s" : ""}
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
                        variant="ghost"
                        size="icon"
                        onClick={() => { deleteTemplate.mutate(tmpl.id); }}
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
    </div>
  );
}
