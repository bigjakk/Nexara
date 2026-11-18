import { usePermissions } from "@/hooks/usePermissions";

/**
 * Adding a PBS server takes manage:pbs (POST /pbs-servers), and its first step,
 * the certificate fetch, an install-wide manage on cluster, pbs or veeam. The
 * Add PBS server dialog and the Backup page's empty state ask this.
 */
export function useCanAddPBSServer(): boolean {
  const { hasPermission, hasGlobalPermission } = usePermissions();
  return (
    hasPermission("manage", "pbs") &&
    (hasGlobalPermission("manage", "cluster") ||
      hasGlobalPermission("manage", "pbs") ||
      hasGlobalPermission("manage", "veeam"))
  );
}
