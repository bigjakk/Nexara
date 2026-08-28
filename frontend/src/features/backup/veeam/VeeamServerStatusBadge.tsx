import { Badge } from "@/components/ui/badge";
import type { VeeamServer } from "../types/backup";
import { veeamServerStatus } from "./veeam-server-status";

/**
 * The one place that decides what a registered Veeam server's connection state
 * looks like.
 *
 * Shared rather than duplicated because the panel header and the server table
 * now both show it, and a state added to one ternary but not the other would
 * have the header calling a server connected while its own row said otherwise.
 */
export function VeeamServerStatusBadge({ server }: { server: VeeamServer }) {
  switch (veeamServerStatus(server)) {
    case "Disabled":
      return <Badge variant="secondary">Disabled</Badge>;
    case "Error":
      return <Badge variant="destructive">Error</Badge>;
    default:
      return (
        <Badge variant="default" className="bg-emerald-600">
          Connected
        </Badge>
      );
  }
}
