import type { VMResponse } from "@/types/api";
import type { HistoryGuestLink } from "@/features/history/types/history";

/** Each guest of a cluster by VMID, as a History's Guest column links it. */
export function guestLinksByVmid(
  vms: readonly VMResponse[] | undefined,
  clusterId: string,
): Map<number, HistoryGuestLink> {
  const links = new Map<number, HistoryGuestLink>();
  for (const vm of vms ?? []) {
    links.set(vm.vmid, {
      name: vm.name,
      path: `/inventory/${vm.type === "lxc" ? "lxc" : "qemu"}/${clusterId}/${vm.id}`,
    });
  }
  return links;
}
