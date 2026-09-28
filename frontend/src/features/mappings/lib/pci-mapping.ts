import type { NodePCIDevice } from "@/features/vms/api/vm-queries";
import type { MappingCheck } from "../api/mapping-queries";
import { cleanDeviceText, entrySegments } from "./usb-mapping";

/*
 * Cluster PCI resource mappings: reading their node entries, and working out
 * the entry that passes a device through. The PCI counterpart of
 * usb-mapping.ts, for the same reason: qemu-server lets nobody but root@pam
 * set a hostpciN naming a host device (check_hostpci_perm,
 * src/PVE/API2/Qemu.pm), and Nexara connects with an API token.
 */

/**
 * One node entry of a PCI mapping's `map`, in pve-guest-common's $map_fmt
 * (src/PVE/Mapping/PCI.pm): `path` is domain:bus:slot[.function], `id` and
 * `subsystemId` are vendor:device, and `subsystemId` and `iommugroup` are ""
 * when the entry has none.
 */
export interface PCIMappingEntry {
  node: string;
  path: string;
  id: string;
  subsystemId: string;
  iommugroup: string;
}

export function parsePCIMappingEntry(raw: string): PCIMappingEntry {
  const entry: PCIMappingEntry = {
    node: "",
    path: "",
    id: "",
    subsystemId: "",
    iommugroup: "",
  };
  for (const s of entrySegments(raw)) {
    if (s.key === "node") entry.node = s.value;
    else if (s.key === "path") entry.path = s.value;
    else if (s.key === "id") entry.id = s.value;
    else if (s.key === "subsystem-id") entry.subsystemId = s.value;
    else if (s.key === "iommugroup") entry.iommugroup = s.value;
  }
  return entry;
}

/** A PCI address in Proxmox's form, as the Nexara route takes it: lowercase, domain required, function optional. */
export const PCI_PATH_PATTERN =
  /^[a-f0-9]{4,}:[a-f0-9]{2}:[a-f0-9]{2}(\.[a-f0-9])?$/;

/**
 * The slot of a device address — "0000:01:00.1" → "0000:01:00" — which maps
 * the whole device, every function passed through as one.
 */
export function pciSlot(address: string): string {
  return address.replace(/\.[a-f0-9]$/, "");
}

/**
 * Half a PCI id as the device listing reports it ("0x10DE"), as an entry holds
 * it ("10de"), or "" when it is not four hex digits.
 */
function pciHexId(raw: string | undefined): string {
  const v = (raw ?? "").toLowerCase().replace(/^0x/, "");
  return /^[0-9a-f]{4}$/.test(v) ? v : "";
}

/**
 * The entry that passes node's device at `path` through, and whether the
 * mapping needs its mdev flag — worked out from the node's device listing the
 * way the server builds the one it writes (proxmox.PCIMapEntryForDevice), so
 * the dialog can find a mapping that already passes exactly that device. The
 * two must agree: Proxmox refuses to start a VM whose device does not match
 * its entry exactly (assert_valid, pve-guest-common src/PVE/Mapping/PCI.pm).
 *
 * A path without a function is the whole device, checked against function 0,
 * so that function's record is read. Undefined when the listing has no record
 * for it, or one whose ids are not vendor:device.
 */
export function pciEntryForDevice(
  devices: readonly NodePCIDevice[],
  node: string,
  path: string,
): { entry: PCIMappingEntry; mdev: boolean } | undefined {
  const record = /\.[a-f0-9]$/.test(path) ? path : `${path}.0`;
  const d = devices.find((dev) => dev.id === record);
  if (!d) return undefined;
  const vendor = pciHexId(d.vendor);
  const device = pciHexId(d.device);
  if (vendor === "" || device === "") return undefined;
  let subsystemId = "";
  if (d.subsystem_vendor && d.subsystem_device) {
    const subVendor = pciHexId(d.subsystem_vendor);
    const subDevice = pciHexId(d.subsystem_device);
    if (subVendor === "" || subDevice === "") return undefined;
    subsystemId = `${subVendor}:${subDevice}`;
  }
  return {
    entry: {
      node,
      path,
      id: `${vendor}:${device}`,
      subsystemId,
      // -1 is no group, and no group is no key.
      iommugroup: d.iommugroup >= 0 ? String(d.iommugroup) : "",
    },
    mdev: d.mdev === true,
  };
}

/**
 * The mapping whose entries for the node are exactly `expected` — its only
 * one there — with the mdev flag the device needs. Adding a device again then
 * reuses its mapping rather than minting a second one for the same hardware.
 *
 * Exact, as Proxmox checks it: the same path, the same lowercase ids, the
 * same IOMMU group, and the mdev flag equal to the device's capability — a
 * mapping that differs in any of them refuses to start the VM. And its ONLY
 * entry for the node: with more, qemu-server may hand the VM another of them
 * (the first one not in use), so it does not pass this device. Its entries
 * for other nodes do not matter to a VM on this one.
 */
export function findReusablePCIMapping<
  M extends { map: readonly string[]; mdev: boolean },
>(
  mappings: readonly M[],
  expected: PCIMappingEntry,
  mdev: boolean,
): M | undefined {
  return mappings.find((m) => {
    if (m.mdev !== mdev) return false;
    const entries = m.map
      .map(parsePCIMappingEntry)
      .filter((e) => e.node === expected.node);
    const [e] = entries;
    return (
      entries.length === 1 &&
      e !== undefined &&
      e.path === expected.path &&
      e.id === expected.id &&
      e.subsystemId === expected.subsystemId &&
      e.iommugroup === expected.iommugroup
    );
  });
}

/**
 * For a pick whose device the dialog cannot read — a typed address, or a
 * whole device whose function 0 the node's list leaves out — the mapping
 * whose only entry for `node` is at `path` and which Proxmox checked clean
 * on that node. The listing's check is Proxmox's own assert_valid, the check
 * that refuses a start, run against the device there, mdev flag included: a
 * clean one is an exact one.
 */
export function findCheckedPCIMappingAt<
  M extends { map: readonly string[]; checks: readonly MappingCheck[] },
>(mappings: readonly M[], node: string, path: string): M | undefined {
  return mappings.find((m) => {
    if (m.checks.length > 0) return false;
    const entries = m.map
      .map(parsePCIMappingEntry)
      .filter((e) => e.node === node);
    const [e] = entries;
    return entries.length === 1 && e !== undefined && e.path === path;
  });
}

/** How a device is named in the picker, and in a new mapping's description. */
export function pciDeviceLabel(d: NodePCIDevice): string {
  return cleanDeviceText(d.device_name || d.vendor_name || "") || d.id;
}

/**
 * The devices a path passes through: the device, or every function of the
 * slot when it names the whole device — for each path of a ";"-joined list,
 * which Proxmox passes as one device.
 */
export function pciDevicesAt(
  devices: readonly NodePCIDevice[],
  path: string,
): NodePCIDevice[] {
  return path.split(";").flatMap((p) =>
    /\.[a-f0-9]$/.test(p)
      ? devices.filter((d) => d.id === p)
      : devices.filter((d) => pciSlot(d.id) === p),
  );
}

/**
 * Why passing `passed` through may take something the node itself needs, for
 * the dialog to warn about and ask before it adds the device. When the VM
 * starts, Proxmox unbinds the device from the node's own driver — and every
 * other device in its IOMMU group but a PCI-to-PCI bridge with it
 * (pci_dev_group_bind_to_vfio, pve-common src/PVE/SysFSTools.pm, skips only
 * a device with a pci_bus directory) — without looking at whether the node is
 * using them, and its own dialog warns about none of it. The classes named
 * are the ones a node most often cannot do without: its disks' controller,
 * its network, its USB. `devices` is the node's list as the dialog has it,
 * which leaves out memory controllers, processors and bridges of every kind.
 */
export function pciHostRisks(
  passed: readonly NodePCIDevice[],
  devices: readonly NodePCIDevice[],
): string[] {
  const why: string[] = [];
  // A device named twice — by overlapping entries — is weighed once.
  const unique = [...new Map(passed.map((d) => [d.id, d])).values()];
  for (const d of unique) {
    const cls = d.class.toLowerCase().replace(/^0x/, "");
    if (cls.startsWith("01")) {
      why.push(
        `${d.id} is a storage controller: if the node's own disks are on it, the node loses them.`,
      );
    } else if (cls.startsWith("02")) {
      why.push(
        `${d.id} is a network controller: if the node's management or cluster network runs over it, the node loses that network.`,
      );
    } else if (cls.startsWith("0c03")) {
      why.push(
        `${d.id} is a USB controller: the node loses every USB device on it.`,
      );
    }
  }
  const passedIds = new Set(unique.map((d) => d.id));
  const groups = new Set(
    unique.map((d) => d.iommugroup).filter((g) => g >= 0),
  );
  const peers = devices.filter(
    (o) => groups.has(o.iommugroup) && !passedIds.has(o.id),
  );
  if (peers.length > 0) {
    why.push(
      `Proxmox takes every device in the IOMMU group away from the node when the VM starts, so ${peers
        .map((p) => p.id)
        .join(", ")} ${peers.length === 1 ? "goes" : "go"} too.`,
    );
  }
  return why;
}

/**
 * Everything to ask about before a VM on `node` is given the devices at
 * `paths`: what pciHostRisks finds, and each path whose device the node's list
 * does not hold (pciUnreadPaths), since what the dialog cannot look at it
 * cannot call safe. Empty when there is nothing to ask.
 */
export function pciPassRisks(
  paths: readonly string[],
  devices: readonly NodePCIDevice[],
  node: string,
): string[] {
  const passed = paths.flatMap((p) => pciDevicesAt(devices, p));
  return [
    ...pciHostRisks(passed, devices),
    ...pciUnreadPaths(paths, devices).map(
      (p) =>
        `Nexara cannot tell what ${p} is, or what shares its IOMMU group: check in Proxmox that ${node} does not need it.`,
    ),
  ];
}

/**
 * The paths among `paths` — each of a ";"-joined list on its own — whose
 * device the node's list does not hold: a typed address, one in a class the
 * list leaves out, one the node no longer has. The dialog asks about these
 * too, since what it cannot look at it cannot call safe.
 *
 * A whole device counts as read only when its function 0 is listed: every
 * present device has one, it is the function Proxmox checks the entry
 * against, and the one the list most often hides — the ISA bridge of a
 * chipset slot, whose other functions it does show.
 */
export function pciUnreadPaths(
  paths: readonly string[],
  devices: readonly NodePCIDevice[],
): string[] {
  const listed = (id: string) => devices.some((d) => d.id === id);
  return [
    ...new Set(
      paths
        .flatMap((p) => p.split(";"))
        .filter(
          (p) => p !== "" && !listed(/\.[a-f0-9]$/.test(p) ? p : `${p}.0`),
        ),
    ),
  ];
}
