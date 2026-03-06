import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Play } from "lucide-react";
import { useApplyFirewallTemplate } from "../api/network-queries";
import type { FirewallTemplate } from "../types/network";

interface ApplyTemplateDialogProps {
  clusterId: string;
  template: FirewallTemplate;
}

export function ApplyTemplateDialog({
  clusterId,
  template,
}: ApplyTemplateDialogProps) {
  const [open, setOpen] = useState(false);
  const apply = useApplyFirewallTemplate(clusterId);

  const handleApply = () => {
    apply.mutate(template.id, {
      onSuccess: () => { setOpen(false); },
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" disabled={clusterId.length === 0}>
          <Play className="h-4 w-4" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Apply Template: {template.name}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            This will add {template.rules.length} firewall rule
            {template.rules.length !== 1 ? "s" : ""} to the cluster. Existing
            rules will not be removed.
          </p>

          <div className="space-y-2">
            {template.rules.map((rule, i) => (
              <div
                key={i}
                className="flex items-center gap-2 text-sm"
              >
                <Badge variant="outline">{rule.type}</Badge>
                <Badge
                  variant={
                    rule.action === "ACCEPT" ? "default" : "destructive"
                  }
                >
                  {rule.action}
                </Badge>
                <span>{rule.proto || "any"}</span>
                {rule.dport && (
                  <span className="font-mono">:{rule.dport}</span>
                )}
                {rule.comment && (
                  <span className="text-muted-foreground">
                    ({rule.comment})
                  </span>
                )}
              </div>
            ))}
          </div>

          {apply.isSuccess && (
            <p className="text-sm text-green-600">
              Applied {apply.data.applied}/{apply.data.total} rules
              successfully.
            </p>
          )}

          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>
              Cancel
            </Button>
            <Button onClick={handleApply} disabled={apply.isPending}>
              Apply to Cluster
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
