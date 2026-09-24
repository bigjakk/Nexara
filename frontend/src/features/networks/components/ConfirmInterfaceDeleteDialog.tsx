import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import type { NetworkInterface } from "../types/network";

interface ConfirmInterfaceDeleteDialogProps {
  target: NetworkInterface | null;
  nodeName: string;
  onClose: () => void;
  onConfirm: (iface: NetworkInterface) => void;
}

/**
 * Confirms deleting one network interface of a node.
 *
 * What Proxmox does with it, transcribed: delete_network (pve-manager
 * PVE/API2/Network.pm) reads the 'interfaces' file, drops the interface (and,
 * for an OVSIntPort/OVSBond, its name from its bridge's ovs_ports) and writes
 * the file back. PVE::INotify::write_file (pve-common src/PVE/INotify.pm)
 * redirects every write of /etc/network/interfaces to its shadow file
 * /etc/network/interfaces.new, so the running network is untouched until the
 * pending file is applied: reload_network_config (PUT /nodes/{node}/network,
 * Nexara's Apply) or the node's next boot (pve-manager
 * services/pvenetcommit.service moves interfaces.new into place).
 * revert_network_changes (DELETE /nodes/{node}/network, Nexara's Revert)
 * unlinks interfaces.new, discarding this and every other pending change on
 * the node.
 *
 * What applying does: reload_network_config renames interfaces.new over
 * interfaces and runs `ifreload -a`, and ifupdown2's reload
 * (ifupdown/ifupdownmain.py _reload_default) schedules "down" for every
 * interface present in the last config and absent from the new one —
 * except that Proxmox's ifupdown2 fork
 * (debian/patches/pve/0003-don-t-remove-bridge-is-tap-veth-are-still-plugged.patch)
 * skips a Linux bridge that still has a tap*, veth* or fwpr* port plugged
 * in, logging "cant remove bridge": Apply leaves it up, and it is gone only
 * after the next boot, when nothing creates it any more.
 * A physical NIC is not removed by any of this: the reader
 * (pve-common src/PVE/Network/Interfaces.pm __read_etc_network_interfaces)
 * adds every physical link `ip link` reports, so the NIC is listed again
 * with no settings of its own. delete_network checks no guest config, so
 * nothing stops a delete of a bridge a guest's NIC is attached to.
 */
export function ConfirmInterfaceDeleteDialog({
  target,
  nodeName,
  onClose,
  onConfirm,
}: ConfirmInterfaceDeleteDialogProps) {
  return (
    <ConfirmDeleteDialog
      target={target}
      onClose={onClose}
      onConfirm={onConfirm}
      title={(iface) => `Delete interface ${iface.iface} on ${nodeName}?`}
      description={(iface) => (
        <>
          <span className="block">
            Proxmox removes the configuration of {iface.iface} from the pending
            network configuration of {nodeName}. The running network does not
            change yet: the delete takes effect when the pending changes are
            applied, or when {nodeName} next boots.
          </span>
          <span className="mt-2 block">
            {iface.type === "eth"
              ? `${iface.iface} is a physical NIC, so it is not removed: once the change takes effect, its addresses and other settings are gone, and Proxmox lists it again with no configuration.`
              : iface.type === "bridge"
                ? `Once the change takes effect, ${iface.iface} is taken down with every address on it, unless a running guest is still plugged into it: then Apply leaves it up until ${nodeName} reboots, after which it is gone and guests configured on it cannot use it. Proxmox does not check guest configs.`
                : `Once the change takes effect, ${iface.iface} is taken down with every address on it. Proxmox does not check whether a guest's network device uses it.`}
          </span>
          <span className="mt-2 block">
            Until then, Revert undoes it — along with every other pending
            network change on {nodeName}.
          </span>
        </>
      )}
      confirmLabel="Delete Interface"
    />
  );
}
