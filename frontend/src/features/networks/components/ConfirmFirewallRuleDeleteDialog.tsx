import { toast } from "sonner";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import type { FirewallRule } from "../types/network";

/** The rule as its table row shows it, so the operator sees WHICH rule sits at
 *  the position they are about to delete. */
function ruleSummary(rule: FirewallRule): string {
  const parts = [`${rule.type} ${rule.action}`];
  if (rule.macro) parts.push(`macro ${rule.macro}`);
  if (rule.proto) parts.push(`proto ${rule.proto}`);
  parts.push(`source ${rule.source || "any"}`);
  parts.push(`dest ${rule.dest || "any"}`);
  if (rule.sport) parts.push(`sport ${rule.sport}`);
  if (rule.dport) parts.push(`dport ${rule.dport}`);
  if (rule.iface) parts.push(`iface ${rule.iface}`);
  if (rule.log) parts.push(`log ${rule.log}`);
  if (!rule.enable) parts.push("disabled");
  if (rule.comment) parts.push(`comment "${rule.comment}"`);
  return parts.join(", ");
}

/** Every field of the two rules is the same — including ones the summary
 *  leaves out, and the list digest, so a list that changed anywhere counts:
 *  Proxmox would refuse that delete anyway. */
function sameRule(a: FirewallRule, b: FirewallRule): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const k of keys) {
    if (a[k as keyof FirewallRule] !== b[k as keyof FirewallRule]) {
      return false;
    }
  }
  return true;
}

interface ConfirmFirewallRuleDeleteDialogProps {
  target: FirewallRule | null;
  onClose: () => void;
  onConfirm: (rule: FirewallRule) => void;
  /** Whose rule list this is, e.g. "the cluster" or "node pve-01". */
  owner: string;
  /** The rule list as it is cached now, to check the dialog's rule against
   *  at confirm time. */
  current: FirewallRule[] | undefined;
}

/**
 * Confirms deleting one cluster or node firewall rule.
 *
 * A rule is deleted BY POSITION: Proxmox's delete_rule (pve-firewall
 * src/PVE/API2/Firewall/Rules.pm) splices out whatever rule is at {pos} in the
 * list as it stands when the request arrives. What makes that safe is the
 * digest the delete carries — the digest of the list this row came from
 * (onConfirm's rule.digest; see api/firewall-rule-digest.ts). delete_rule runs
 * PVE::Tools::assert_if_modified against it before touching the list, so if
 * the list changed after this page loaded, Proxmox refuses the delete, the
 * server answers 409, nothing is removed, and the list is reloaded.
 *
 * The dialog still spells out the rule the operator is looking at, and at
 * confirm still compares it with the rule now at that position in the cached
 * list — which a refetch may have changed while the dialog was open — sending
 * nothing on any difference. That check costs no request; the digest is what
 * covers a change the cache has not seen.
 *
 * The delete is saved to the firewall config at once, with no apply step; the
 * pve-firewall service re-reads the config on its update loop, every 10 s
 * ($updatetime in pve-firewall src/PVE/Service/pve_firewall.pm).
 */
export function ConfirmFirewallRuleDeleteDialog({
  target,
  onClose,
  onConfirm,
  owner,
  current,
}: ConfirmFirewallRuleDeleteDialogProps) {
  return (
    <ConfirmDeleteDialog
      target={target}
      onClose={onClose}
      onConfirm={(rule) => {
        const now = current?.find((r) => r.pos === rule.pos);
        if (now === undefined || !sameRule(rule, now)) {
          toast.error(
            `Nothing was deleted: the rule list of ${owner} changed while you were confirming. Check the list and try again.`,
          );
          return;
        }
        onConfirm(rule);
      }}
      title={(rule) => `Delete firewall rule #${String(rule.pos)}?`}
      description={(rule) => (
        <>
          <span className="block font-mono text-xs">{ruleSummary(rule)}</span>
          <span className="mt-2 block">
            Rules are deleted by position — this is position {rule.pos} of the
            rule list of {owner}. Nexara sends Proxmox a check that the list is
            unchanged, so if it has changed since this page loaded, nothing is
            deleted and the list is reloaded. The rules below it move up one
            position.
          </span>
          <span className="mt-2 block">
            The change is saved at once, with no apply step; the firewall
            service reads it on its next update, every 10 seconds. It cannot be
            undone: to get the rule back, create it again.
          </span>
        </>
      )}
      confirmLabel="Delete Rule"
    />
  );
}
