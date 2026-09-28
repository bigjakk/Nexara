import { useState } from "react";
import type { NodePCIDevice } from "@/features/vms/api/vm-queries";
import {
  PCI_PATH_PATTERN,
  pciDeviceLabel,
  pciEntryForDevice,
  pciSlot,
} from "../lib/pci-mapping";

/** Why a typed PCI address is refused. */
const PCI_ADDRESS_HINT =
  "An address like 0000:01:00.0: domain, bus, slot and function, in lowercase hex.";

/**
 * A host PCI device picked on `node`: the device, or all of its functions, from
 * the node's device list — or, when the node lists none, an address typed in.
 * What the pick passes through (`path`), what the node calls it (`label`), and
 * the entry the server will build for it (`expected`, undefined when the list
 * has no record to build it from) follow from it. PCIDevicePicker renders it.
 */
export function usePCIPick(
  devices: readonly NodePCIDevice[] | undefined,
  node: string,
) {
  const [addressInput, setAddress] = useState("");
  const [allFunctions, setAllFunctions] = useState(false);

  const deviceList = devices ?? [];
  const hasDeviceList = deviceList.length > 0;
  // Once the node's list is there, only a device on it counts: an address
  // typed while the list was loading is not what the picker then shows.
  const address =
    hasDeviceList && !deviceList.some((d) => d.id === addressInput)
      ? ""
      : addressInput;
  const typedAddressError =
    !hasDeviceList && address !== "" && !PCI_PATH_PATTERN.test(address)
      ? PCI_ADDRESS_HINT
      : "";
  // The address the pick passes through: the device, or its slot for all of
  // its functions.
  const path =
    address !== "" && typedAddressError === ""
      ? allFunctions
        ? pciSlot(address)
        : address
      : "";
  // The whole device is named after function 0, as Proxmox checks it by.
  const namedDevice =
    deviceList.find(
      (d) => d.id === (allFunctions ? `${pciSlot(address)}.0` : address),
    ) ?? deviceList.find((d) => d.id === address);
  const label = namedDevice ? pciDeviceLabel(namedDevice) : "";
  const expected =
    path !== "" ? pciEntryForDevice(deviceList, node, path) : undefined;

  return {
    deviceList,
    hasDeviceList,
    address,
    setAddress,
    allFunctions,
    setAllFunctions,
    typedAddressError,
    path,
    label,
    expected,
  };
}

/** A pick, as usePCIPick returns it. */
export type PCIPick = ReturnType<typeof usePCIPick>;
