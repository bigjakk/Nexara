import { useState } from "react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { WarningCallout } from "@/components/WarningCallout";
import { Play } from "lucide-react";
import { useApplyFirewallTemplate } from "../api/network-queries";
import type { FirewallTemplate } from "../types/network";

interface ApplyTemplateDialogProps {
  clusterId: string;
  template: FirewallTemplate;
}

function ruleCount(n: number): string {
  return `${String(n)} rule${n === 1 ? "" : "s"}`;
}

/**
 * Applying a template adds its rules at the top of the cluster's rule list,
 * in the template's order (the server sends them last first; see
 * handlers/firewall_templates.go ApplyTemplate), so they come before every
 * rule the cluster has now. The dialog says so, warns when one of them
 * drops or rejects, and reports how many Proxmox took.
 */
export function ApplyTemplateDialog({
  clusterId,
  template,
}: ApplyTemplateDialogProps) {
  const [open, setOpen] = useState(false);
  const apply = useApplyFirewallTemplate(clusterId);
  const blocks = template.rules.some(
    (r) => r.action === "DROP" || r.action === "REJECT",
  );

  const handleApply = () => {
    apply.mutate(template.id, {
      onSuccess: (data) => {
        setOpen(false);
        if (data.applied === data.total) {
          toast.success(
            `Added ${ruleCount(data.applied)} from ${template.name} at the top of the cluster's firewall rules.`,
          );
        } else {
          toast.warning(
            `Added ${String(data.applied)} of ${ruleCount(data.total)} from ${template.name}. Proxmox refused the other ${String(data.total - data.applied)}; check the cluster's rule list.`,
          );
        }
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          aria-label={`Apply template ${template.name}`}
          variant="ghost"
          size="icon"
          disabled={clusterId.length === 0}
        >
          <Play className="h-4 w-4" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Apply Template: {template.name}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Adds {ruleCount(template.rules.length)} at the top of the
            cluster&apos;s firewall rules, above every rule already there, in
            the order below. Existing rules are not changed or removed.
          </p>

          {blocks && (
            <WarningCallout title="This can lock you out of the cluster">
              <p>
                Rules are checked from the top, so these come before every rule
                the cluster has now. While the cluster&apos;s firewall is on, a
                DROP or REJECT here that matches the traffic you reach its
                nodes with — their web UI and API, SSH, or Nexara&apos;s own
                connection — cuts that access off.
              </p>
            </WarningCallout>
          )}

          <div className="space-y-2">
            {template.rules.map((rule, i) => (
              <div key={i} className="flex items-center gap-2 text-sm">
                <Badge variant="outline">{rule.type}</Badge>
                <Badge
                  variant={rule.action === "ACCEPT" ? "default" : "destructive"}
                >
                  {rule.action}
                </Badge>
                <span>{rule.proto || "any"}</span>
                {rule.dport && <span className="font-mono">:{rule.dport}</span>}
                {rule.comment && (
                  <span className="text-muted-foreground">
                    ({rule.comment})
                  </span>
                )}
              </div>
            ))}
          </div>

          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
              }}
            >
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
