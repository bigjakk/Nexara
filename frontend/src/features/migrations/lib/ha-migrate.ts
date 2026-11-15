/**
 * Whether migrating this guest hands a stopped guest to the HA manager.
 *
 * For an HA-managed guest Proxmox's migrate call only queues a request with
 * the HA manager (an hamigrate task that ends OK at once). pve-ha-manager
 * moves a stopped service offline from its node's LRM (Manager.pm,
 * next_state_stopped; LRM.pm, exec_resource_agent). When that migration cannot
 * run, the guest stays where it is and only the HA log on its node says why:
 * nothing in the migrate call or its task reports it. A running guest is
 * migrated live, and an "ignored" one is not HA-managed for its migrate call.
 */
export function movesStoppedHAGuest(status: string, haState: string): boolean {
  const ha = haState.trim().toLowerCase();
  return ha !== "" && ha !== "ignored" && status.toLowerCase() !== "running";
}
