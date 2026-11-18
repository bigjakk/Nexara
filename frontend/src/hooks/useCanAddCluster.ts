import { usePermissions } from "@/hooks/usePermissions";

/**
 * Adding a cluster takes manage:cluster across the whole install: POST
 * /clusters is a global check. The Add cluster dialog and every empty state
 * that offers it ask this.
 */
export function useCanAddCluster(): boolean {
  return usePermissions().hasGlobalPermission("manage", "cluster");
}
