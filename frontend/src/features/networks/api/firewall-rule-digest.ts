import { toast } from "sonner";
import { ApiClientError } from "@/lib/api-client";

/**
 * Firewall rules are addressed BY POSITION, so every rule update and delete
 * carries the digest of the rule list the row came from. Every rule a listing
 * returns carries the same digest — pve-firewall's get_rules stamps one digest
 * of the whole list onto each item (copy_list_with_digest, src/PVE/Firewall.pm)
 * — and Proxmox's update_rule and delete_rule refuse the write when the list's
 * digest no longer matches (PVE::Tools::assert_if_modified, run before the
 * position is even looked at). Nexara answers that refusal with 409
 * (mapFirewallRuleError, internal/api/handlers/firewall.go), and nothing was
 * changed.
 *
 * Proxmox only compares when a digest is sent, so a write without one is
 * unconditional: it changes whatever rule sits at that position now. The SPA
 * never sends a write without one — an empty digest is refused here, before
 * any request.
 */
export function requireRuleDigest(digest: string): string {
  if (digest === "") {
    throw new Error(
      "Nothing was sent: this rule came without the rule list's digest, so Proxmox could not check that the list is unchanged. Reload the list and try again.",
    );
  }
  return digest;
}

/** The server refused the write because the rule list changed since it was read. */
export function isStaleRuleList(error: Error): boolean {
  return error instanceof ApiClientError && error.status === 409;
}

/**
 * The onError of a rule update or delete: says what happened, and on a stale
 * list reloads it — returned, so the mutation stays pending (and the table's
 * buttons disabled) until the fresh list is in.
 *
 * `outcome` is what did not happen ("Nothing was deleted"), `owner` whose list
 * it is ("the cluster", "node pve-01").
 */
export function onRuleWriteError(
  error: Error,
  outcome: string,
  owner: string,
  reload: () => Promise<void>,
): Promise<void> | undefined {
  if (!isStaleRuleList(error)) {
    toast.error(error.message.length > 0 ? error.message : "Request failed");
    return undefined;
  }
  toast.error(
    `${outcome}: the rule list of ${owner} changed since it was loaded, so Proxmox refused the change. The list has been reloaded — check it and try again.`,
  );
  return reload();
}
