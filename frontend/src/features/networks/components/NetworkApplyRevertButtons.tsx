import { useState } from "react";
import { Check, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  useApplyNetworkConfig,
  useRevertNetworkConfig,
} from "../api/network-queries";

/**
 * Revert and Apply Config for one node's pending network configuration, each
 * behind a confirmation. Both act on every pending change, whoever made it:
 * Apply (PUT /nodes/{node}/network, pve-manager PVE/API2/Network.pm
 * reload_network_config) moves interfaces.new over interfaces and runs
 * `ifreload -a`; Revert (DELETE, revert_network_changes) deletes
 * interfaces.new.
 */
export function NetworkApplyRevertButtons({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const apply = useApplyNetworkConfig(clusterId, nodeName);
  const revert = useRevertNetworkConfig(clusterId, nodeName);
  const [pending, setPending] = useState<"apply" | "revert" | null>(null);

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        onClick={() => {
          setPending("revert");
        }}
        disabled={revert.isPending}
      >
        <RotateCcw className="mr-1 h-4 w-4" />
        Revert
      </Button>
      <Button
        variant="outline"
        size="sm"
        onClick={() => {
          setPending("apply");
        }}
        disabled={apply.isPending}
      >
        <Check className="mr-1 h-4 w-4" />
        Apply Config
      </Button>
      <ConfirmDeleteDialog
        target={pending}
        onClose={() => {
          setPending(null);
        }}
        onConfirm={(change) => {
          if (change === "apply") apply.mutate();
          else revert.mutate();
        }}
        title={(change) =>
          change === "apply"
            ? `Apply the pending network changes on ${nodeName}?`
            : `Discard the pending network changes on ${nodeName}?`
        }
        description={(change) =>
          change === "apply"
            ? `Proxmox makes every pending change to the network configuration of ${nodeName} live, including any made outside Nexara, and reloads its network. If a change is wrong — an address, a gateway, the ports of a bridge or bond — ${nodeName} can lose its network connection, Nexara's included, and need fixing from its console.`
            : `Proxmox discards every pending change to the network configuration of ${nodeName}, including any made outside Nexara. The running network does not change. Discarded changes cannot be got back: to redo one, make it again.`
        }
        confirmLabel={(change) =>
          change === "apply" ? "Apply Config" : "Revert"
        }
      />
    </>
  );
}
