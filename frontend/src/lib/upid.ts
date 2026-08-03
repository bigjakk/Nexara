/**
 * Extract the numeric guest VMID from a Proxmox UPID
 * (`UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user>@<realm>:`).
 * The `id` field is the task's target object — the VMID for guest tasks, a
 * storage/other identifier (or empty) for non-guest tasks. Returns null when
 * the field is not a plain integer, so non-guest tasks never match a VM.
 */
export function upidVmid(upid: string): number | null {
  const parts = upid.split(":");
  if (parts.length < 8 || parts[0] !== "UPID") return null;
  const id = parts[6];
  if (id === undefined || id === "" || !/^\d+$/.test(id)) return null;
  return Number.parseInt(id, 10);
}
