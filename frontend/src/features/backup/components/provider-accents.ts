/**
 * Accent vocabulary for the backup providers Nexara integrates with.
 *
 * Split out from the components so the .tsx file exports components only —
 * mixing constant and component exports breaks React Fast Refresh.
 */

export type BackupProvider = "veeam" | "pbs";

/**
 * Static class lookup, not interpolation: Tailwind scans source text for whole
 * class names, so a template-built `bg-provider-${provider}` compiles to
 * nothing at all and the tile renders transparent.
 */
export const PROVIDER_FILL_CLASSES: Record<BackupProvider, string> = {
  veeam: "bg-provider-veeam text-provider-veeam-foreground",
  pbs: "bg-provider-pbs text-provider-pbs-foreground",
};

/**
 * Nominative use of the marks: we name the products to say what we talk to,
 * and disclaim the affiliation that naming them could otherwise imply.
 */
export const PROVIDER_TRADEMARK_OWNERS: Record<BackupProvider, string> = {
  veeam: "Veeam is a registered trademark of Veeam Software Group GmbH.",
  pbs: "Proxmox is a registered trademark of Proxmox Server Solutions GmbH.",
};
