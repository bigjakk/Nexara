import { Loader2 } from "lucide-react";
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
    case "Syncing":
      // Outline rather than a solid fill: this state is transient and resolves
      // on its own, so it should read as work in progress rather than as a
      // condition competing with Error for attention. The spinner is the part
      // that actually says "something is happening" — the whole point of the
      // state — so it is not decorative and is not hidden from assistive tech.
      return (
        <Badge variant="outline" title="Waiting for the first inventory sync">
          <Loader2 className="mr-1 h-3 w-3 animate-spin" aria-hidden="true" />
          Syncing
        </Badge>
      );
    default:
      return (
        <Badge variant="default" className="bg-emerald-600">
          Connected
        </Badge>
      );
  }
}
