import { useState } from "react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { WarningCallout } from "@/components/WarningCallout";
import { Eye, Play } from "lucide-react";
import { useApplyFirewallTemplate } from "../api/network-queries";
import type { FirewallTemplate } from "../types/network";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

interface ApplyTemplateDialogProps {
  clusterId: string;
  template: FirewallTemplate;
  /** Applying takes manage:network on the cluster. Without it the dialog
   *  only lists the template's rules, which nothing else on the page shows. */
  canApply: boolean;
}

function ruleCount(n: number): string {
  return i18n.t("networks:applyTemplateDialog.ruleCount", { count: n });
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
  canApply,
}: ApplyTemplateDialogProps) {
  const { t } = useTranslation("networks");
  const [open, setOpen] = useState(false);
  const apply = useApplyFirewallTemplate(clusterId);
  const blocks = template.rules.some(
    (r) => r.action === "DROP" || r.action === "REJECT",
  );
  const title = canApply
    ? t("applyTemplateDialog.applyTemplateName", { name: template.name })
    : t("applyTemplateDialog.rulesOfName", { name: template.name });

  const handleApply = () => {
    apply.mutate(template.id, {
      onSuccess: (data) => {
        setOpen(false);
        if (data.applied === data.total) {
          toast.success(
            t("applyTemplateDialog.addedAppliedFromNameAt", { applied: ruleCount(data.applied), name: template.name }),
          );
        } else {
          toast.warning(
            t("applyTemplateDialog.addedAppliedOfTotalFrom", { applied: String(data.applied), total: ruleCount(data.total), name: template.name, total2: String(data.total - data.applied) }),
          );
        }
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          aria-label={
            canApply
              ? title
              : t("applyTemplateDialog.viewRulesName", { name: template.name })
          }
          variant="ghost"
          size="icon"
          className="h-8 w-8"
          disabled={clusterId.length === 0}
        >
          {canApply ? (
            <Play className="h-4 w-4" />
          ) : (
            <Eye className="h-4 w-4" />
          )}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {canApply && (
            <p className="text-sm text-muted-foreground">
              {t("applyTemplateDialog.addsLengthAtTheTop", {
                length: ruleCount(template.rules.length),
              })}
            </p>
          )}

          {canApply && blocks && (
            <WarningCallout title={t("applyTemplateDialog.thisCanLockYouOut")}>
              <p>
                {t("applyTemplateDialog.rulesAreCheckedFromThe")}
              </p>
            </WarningCallout>
          )}

          <ol className="divide-y text-sm">
            {template.rules.map((rule, i) => (
              <li
                key={i}
                className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 py-1.5"
              >
                <span className="w-10 text-muted-foreground">{rule.type}</span>
                <span className="w-16 font-mono text-[12.5px] font-semibold">
                  {rule.action}
                </span>
                <span>
                  {rule.proto || t("applyTemplateDialog.any")}
                  {rule.dport && (
                    <span className="font-mono">:{rule.dport}</span>
                  )}
                </span>
                {rule.comment && (
                  <span className="text-muted-foreground">
                    ({rule.comment})
                  </span>
                )}
              </li>
            ))}
          </ol>

        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            {canApply
              ? t("applyTemplateDialog.cancel")
              : t("applyTemplateDialog.close")}
          </Button>
          {canApply && (
            <Button onClick={handleApply} disabled={apply.isPending}>
              {t("applyTemplateDialog.applyToCluster")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
