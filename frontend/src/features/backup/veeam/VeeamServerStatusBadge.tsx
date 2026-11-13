import { Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { StateText } from "../components/StateText";
import type { VeeamServer } from "../types/backup";
import { veeamServerStatus } from "./veeam-server-status";

/**
 * The one place that decides what a registered Veeam server's connection state
 * looks like.
 *
 * Shared rather than duplicated because the server's line and the server table
 * both show it, and a state added to one ternary but not the other would have
 * the line calling a server connected while its own row said otherwise. A
 * badge on the line; `plain` for a table cell, where it is a dot and the word.
 */
export function VeeamServerStatusBadge({
  server,
  plain = false,
}: {
  server: VeeamServer;
  plain?: boolean;
}) {
  const status = veeamServerStatus(server);
  if (plain) {
    switch (status) {
      case "Disabled":
        return <StateText tone="idle">Disabled</StateText>;
      case "Error":
        return <StateText tone="bad">Error</StateText>;
      case "Syncing":
        return (
          <StateText
            tone="running"
            title="Waiting for the first inventory sync"
          >
            Syncing
          </StateText>
        );
      default:
        return <StateText tone="ok">Connected</StateText>;
    }
  }
  switch (status) {
    case "Disabled":
      return <Badge variant="secondary">Disabled</Badge>;
    case "Error":
      return <Badge variant="destructive">Error</Badge>;
    case "Syncing":
      // Outline rather than a solid fill: this state is transient and resolves
      // on its own, so it should read as work in progress rather than as a
      // condition competing with Error for attention. The spinner is the part
      // that actually says "something is happening" — the whole point of the
      // state — and it turns in the data colour, as running work does.
      return (
        <Badge variant="outline" title="Waiting for the first inventory sync">
          <Loader2
            className="mr-1 h-3 w-3 animate-spin text-data"
            aria-hidden="true"
          />
          Syncing
        </Badge>
      );
    default:
      return <Badge variant="healthy">Connected</Badge>;
  }
}
