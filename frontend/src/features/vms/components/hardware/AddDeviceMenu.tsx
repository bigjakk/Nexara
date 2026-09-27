import { useState } from "react";
import {
  Plus,
  Network,
  Usb,
  Cpu,
  Monitor,
  HardDrive,
  Key,
  Shield,
  Dice1,
  FolderOpen,
  Terminal,
  Disc,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  rngSources,
  serialOptions,
  virtiofsCacheModes,
  efiTypes,
  tpmVersions,
  netModels,
} from "../../lib/vm-config-constants";
import {
  buildUSB,
  buildPCI,
  buildSerial,
  buildRNG,
  buildVirtioFS,
  buildEFIDisk,
  buildTPMState,
  buildNet,
  findReusableUSBMapping,
  MAPPING_ID_PATTERN,
  cleanDeviceText,
  suggestMappingName,
  usbMappingEntryFor,
} from "../../lib/vm-config-parsers";
import {
  useCreateUSBMapping,
  useNodeUSBMappings,
  type NodeUSBDevice,
  type NodePCIDevice,
  type USBMapping,
} from "../../api/vm-queries";
import type { VMConfig } from "../../types/vm";
import { usePermissions } from "@/hooks/usePermissions";
import { describeError } from "@/lib/api-error";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

interface ISOFile {
  volid: string;
  content: string;
}

interface AddDeviceMenuProps {
  config: VMConfig;
  /** For the USB dialog's mapping listing and create. */
  clusterId: string;
  /** The VM's node: mappings are checked, and created, for it. */
  nodeName: string;
  diskStorages: Array<{ storage: string; type: string; id: string }>;
  usbDevices: NodeUSBDevice[] | undefined;
  pciDevices: NodePCIDevice[] | undefined;
  bridges: string[];
  isoFiles: ISOFile[];
  onAddDevice: (key: string, value: string) => void;
  onAddCDROM: (key: string, isoVolid: string) => void;
  onAddDisk: () => void;
}

type DeviceDialog =
  | "nic"
  | "usb"
  | "pci"
  | "serial"
  | "rng"
  | "virtiofs"
  | "efi"
  | "tpm"
  | "cloudinit"
  | "cdrom"
  | null;

function findNextIndex(config: VMConfig, prefix: string, max: number): number {
  for (let i = 0; i <= max; i++) {
    if (config[`${prefix}${String(i)}`] == null) return i;
  }
  return -1;
}

export function AddDeviceMenu({
  config,
  clusterId,
  nodeName,
  diskStorages,
  usbDevices,
  pciDevices,
  bridges,
  isoFiles,
  onAddDevice,
  onAddCDROM,
  onAddDisk,
}: AddDeviceMenuProps) {
  const [dialog, setDialog] = useState<DeviceDialog>(null);

  const hasEfi = config["efidisk0"] != null;
  const hasTpm = config["tpmstate0"] != null;
  const hasRng = config["rng0"] != null;

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" className="h-7 gap-1 text-xs">
            <Plus className="h-3 w-3" /> Add Device
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-48">
          <DropdownMenuItem onClick={onAddDisk}>
            <HardDrive className="mr-2 h-4 w-4" /> Hard Disk
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("cdrom");
            }}
          >
            <Disc className="mr-2 h-4 w-4" /> CD/DVD Drive
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("nic");
            }}
          >
            <Network className="mr-2 h-4 w-4" /> Network Device
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setDialog("usb");
            }}
          >
            <Usb className="mr-2 h-4 w-4" /> USB Device
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("pci");
            }}
          >
            <Cpu className="mr-2 h-4 w-4" /> PCI Device
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("serial");
            }}
          >
            <Terminal className="mr-2 h-4 w-4" /> Serial Port
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setDialog("cloudinit");
            }}
          >
            <Monitor className="mr-2 h-4 w-4" /> Cloud-Init Drive
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("rng");
            }}
            disabled={hasRng}
          >
            <Dice1 className="mr-2 h-4 w-4" /> VirtIO RNG {hasRng && "(exists)"}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("virtiofs");
            }}
          >
            <FolderOpen className="mr-2 h-4 w-4" /> VirtioFS Share
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setDialog("efi");
            }}
            disabled={hasEfi}
          >
            <Key className="mr-2 h-4 w-4" /> EFI Disk {hasEfi && "(exists)"}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("tpm");
            }}
            disabled={hasTpm}
          >
            <Shield className="mr-2 h-4 w-4" /> TPM State {hasTpm && "(exists)"}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {dialog === "nic" && (
        <AddNICDialog
          config={config}
          bridges={bridges}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "usb" && (
        <AddUSBDialog
          config={config}
          clusterId={clusterId}
          nodeName={nodeName}
          devices={usbDevices}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "pci" && (
        <AddPCIDialog
          config={config}
          devices={pciDevices}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "serial" && (
        <AddSerialDialog
          config={config}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "rng" && (
        <AddRNGDialog
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "virtiofs" && (
        <AddVirtioFSDialog
          config={config}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "efi" && (
        <AddEFIDialog
          diskStorages={diskStorages}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "tpm" && (
        <AddTPMDialog
          diskStorages={diskStorages}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "cloudinit" && (
        <AddCloudInitDialog
          config={config}
          diskStorages={diskStorages}
          onAdd={onAddDevice}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog === "cdrom" && (
        <AddCDROMDialog
          config={config}
          isoFiles={isoFiles}
          onAdd={onAddCDROM}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// NIC Dialog
// ---------------------------------------------------------------------------

function AddNICDialog({
  config,
  bridges,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  bridges: string[];
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [model, setModel] = useState("virtio");
  const [bridge, setBridge] = useState(bridges[0] ?? "vmbr0");
  const [firewall, setFirewall] = useState(true);
  const [vlan, setVlan] = useState("");
  const [rate, setRate] = useState("");
  const [mtu, setMtu] = useState("");

  const idx = findNextIndex(config, "net", 31);
  const canAdd = idx >= 0 && bridge.length > 0;

  function handleAdd() {
    if (!canAdd) return;
    const val = buildNet({
      model,
      mac: "",
      bridge,
      firewall,
      vlanTag: vlan,
      rateLimit: rate,
      mtu,
      multiqueue: "",
      linkDown: false,
    });
    onAdd(`net${String(idx)}`, val);
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add Network Device (net{String(idx)})</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label className="text-xs">Model</Label>
              <select
                className={selectClass}
                value={model}
                onChange={(e) => {
                  setModel(e.target.value);
                }}
              >
                {netModels.map((m) => (
                  <option key={m.value} value={m.value}>
                    {m.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Bridge</Label>
              {bridges.length > 0 ? (
                <select
                  className={selectClass}
                  value={bridge}
                  onChange={(e) => {
                    setBridge(e.target.value);
                  }}
                >
                  {bridges.map((b) => (
                    <option key={b} value={b}>
                      {b}
                    </option>
                  ))}
                </select>
              ) : (
                <Input
                  value={bridge}
                  onChange={(e) => {
                    setBridge(e.target.value);
                  }}
                  placeholder="vmbr0"
                />
              )}
            </div>
            <div className="space-y-1">
              <Label className="text-xs">VLAN Tag</Label>
              <Input
                type="number"
                min={1}
                max={4094}
                value={vlan}
                onChange={(e) => {
                  setVlan(e.target.value);
                }}
                placeholder="None"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Rate (MB/s)</Label>
              <Input
                type="number"
                min={0}
                value={rate}
                onChange={(e) => {
                  setRate(e.target.value);
                }}
                placeholder="Unlimited"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">MTU</Label>
              <Input
                type="number"
                min={0}
                value={mtu}
                onChange={(e) => {
                  setMtu(e.target.value);
                }}
                placeholder="Default"
              />
            </div>
          </div>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-nic-fw"
                checked={firewall}
                onCheckedChange={(v) => {
                  setFirewall(v === true);
                }}
              />
              <Label htmlFor="add-nic-fw" className="cursor-pointer text-xs">
                Firewall
              </Label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// USB Dialog
// ---------------------------------------------------------------------------

type USBMode = "mapped" | "device" | "port" | "spice";

// Proxmox's own dialog offers the same four (pve-manager
// www/manager6/qemu/USBEdit.js). Its two raw ones write host=<id> and
// host=<bus>-<port>, which qemu-server refuses to anyone but root@pam, so here
// they go through a mapping instead — see AddUSBDialog.
const usbModes: ReadonlyArray<{ value: USBMode; label: string; hint: string }> =
  [
    {
      value: "mapped",
      label: "Mapped device",
      hint: "A cluster resource mapping",
    },
    {
      value: "device",
      label: "Host device",
      hint: "This device, on any port",
    },
    {
      // Not "whatever is on the port": a port mapping carries the device's
      // id too, and Proxmox refuses to start the VM when another device is
      // there (assert_valid, pve-guest-common src/PVE/Mapping/USB.pm). What
      // the port adds is telling two identical devices apart.
      value: "port",
      label: "Host USB port",
      hint: "This device, on this port",
    },
    {
      value: "spice",
      label: "SPICE port",
      hint: "A device redirected from the SPICE client",
    },
  ];

const USB_DEVICE_ID_PATTERN = /^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$/;

const usbDeviceId = (d: NodeUSBDevice) => `${d.vendid}:${d.prodid}`;
const usbPortPath = (d: NodeUSBDevice) => `${String(d.busnum)}-${d.usbpath}`;

/**
 * The devices a passthrough picker offers, filtered as Proxmox's USBSelector
 * (pve-manager www/manager6/form/USBSelector.js) filters them: no root hub
 * (it has no usbpath), nothing without a product id, and no hub (class 9).
 */
function passthroughCandidates(
  devices: NodeUSBDevice[] | undefined,
): NodeUSBDevice[] {
  return (devices ?? []).filter(
    (d) => d.usbpath !== "" && d.prodid !== "" && d.class !== 9,
  );
}

/** The device's own name, cleaned — it is the device's to say — or its id. */
function deviceLabel(d: NodeUSBDevice | undefined, id: string): string {
  return (
    cleanDeviceText(d?.product ?? "") ||
    cleanDeviceText(d?.manufacturer ?? "") ||
    `USB ${id}`
  );
}

function mappingOptionLabel(m: USBMapping): string {
  const description = cleanDeviceText(m.description);
  let label = description ? `${m.id} — ${description}` : m.id;
  if (m.errors.some((e) => e.severity === "error")) label += " (error)";
  else if (m.errors.length > 0) label += " (warning)";
  return label;
}

/**
 * Adds a usbN. A real device can only go through a cluster resource mapping:
 * qemu-server's check_usb_perm lets nobody but root@pam write host=<device>,
 * and Nexara connects with an API token, which never is root@pam. So picking
 * a host device or port reuses the mapping that already passes exactly that,
 * or creates one (which needs manage:cluster) and stages mapping=<name>.
 *
 * While a create is in flight the dialog is locked — no close, no Cancel, no
 * edits — because the create cannot be recalled: closing then would leave it
 * to finish and stage a device the operator had already walked away from,
 * into a slot another add may have taken meanwhile.
 */
function AddUSBDialog({
  config,
  clusterId,
  nodeName,
  devices,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  clusterId: string;
  nodeName: string;
  devices: NodeUSBDevice[] | undefined;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const { canManage } = usePermissions();
  const canCreateMapping = canManage("cluster");
  const mappingsQuery = useNodeUSBMappings(clusterId, nodeName);
  const createMapping = useCreateUSBMapping(clusterId);
  const busy = createMapping.isPending;

  const [mode, setMode] = useState<USBMode>("device");
  const [mappingId, setMappingId] = useState("");
  const [deviceId, setDeviceId] = useState("");
  const [port, setPort] = useState("");
  // null until the operator types, so the suggestion follows the pick.
  const [nameInput, setNameInput] = useState<string | null>(null);
  const [usb3, setUsb3] = useState(true);

  const idx = findNextIndex(config, "usb", 13);
  const mappings = mappingsQuery.data ?? [];
  const candidates = passthroughCandidates(devices);
  const hasDeviceList = candidates.length > 0;
  const isHostMode = mode === "device" || mode === "port";

  // What a host pick passes through: a device id and, for a port, its path.
  // A typed id is lowercased to match the node's sysfs, which is what
  // Proxmox compares a mapping's id against when the VM starts.
  let picked: { deviceId: string; path: string; label: string } | null = null;
  if (mode === "device" && USB_DEVICE_ID_PATTERN.test(deviceId)) {
    const id = deviceId.toLowerCase();
    const d = candidates.find((c) => usbDeviceId(c) === id);
    picked = { deviceId: id, path: "", label: deviceLabel(d, id) };
  } else if (mode === "port") {
    // Proxmox's own mapping editor refuses an empty port the same way: the
    // mapping needs the id of the device on it (window/USBMapEdit.js).
    const d = candidates.find((c) => usbPortPath(c) === port);
    if (d) {
      picked = {
        deviceId: usbDeviceId(d),
        path: port,
        label: deviceLabel(d, usbDeviceId(d)),
      };
    }
  }

  // Reuse and the taken-name check both read the listing, so a host pick
  // waits for it: deciding on a listing still loading, or one that failed,
  // would mint a duplicate of a mapping that exists, or a 409.
  const listingReady = mappingsQuery.isSuccess;
  const reusable =
    picked && listingReady
      ? findReusableUSBMapping(mappings, nodeName, picked.deviceId, picked.path)
      : undefined;
  const needsCreate = picked !== null && listingReady && reusable === undefined;
  const suggestedName = picked
    ? suggestMappingName(picked.label, new Set(mappings.map((m) => m.id)))
    : "";
  const newName = nameInput ?? suggestedName;
  let nameError = "";
  if (needsCreate) {
    if (!MAPPING_ID_PATTERN.test(newName)) {
      nameError =
        "Start with a letter; use letters, digits, '_' and '-' (2 to 128 characters).";
    } else if (mappings.some((m) => m.id === newName)) {
      nameError = `A mapping named "${newName}" already exists for other hardware.`;
    }
  }
  const typedIdError =
    mode === "device" &&
    !hasDeviceList &&
    deviceId !== "" &&
    !USB_DEVICE_ID_PATTERN.test(deviceId)
      ? "Enter vendor:product as four hex digits each, without 0x (e.g. 1234:5678)."
      : "";

  const selectedMapping = mappings.find((m) => m.id === mappingId);
  const slotsFull = idx < 0;
  const createDenied = needsCreate && !canCreateMapping;

  const canAdd =
    !slotsFull &&
    !busy &&
    (mode === "spice" ||
      (mode === "mapped" && selectedMapping !== undefined) ||
      (isHostMode &&
        picked !== null &&
        listingReady &&
        (reusable !== undefined || (!createDenied && nameError === ""))));

  // A new pick or name makes the last refusal stale. Never while a create is
  // in flight: resetting then would detach the dialog from it.
  function clearCreateError() {
    if (!createMapping.isPending) createMapping.reset();
  }

  async function handleAdd() {
    if (!canAdd) return;
    let value: string;
    if (mode === "spice") {
      value = buildUSB({ host: "", mapping: "", usb3, spice: true });
    } else if (mode === "mapped") {
      value = buildUSB({ host: "", mapping: mappingId, usb3, spice: false });
    } else {
      if (!picked) return;
      let id = reusable?.id;
      if (id === undefined) {
        try {
          await createMapping.mutateAsync({
            mapping_id: newName,
            node: nodeName,
            device_id: picked.deviceId,
            ...(picked.path ? { path: picked.path } : {}),
            // Already cleaned: the label is deviceLabel's.
            description: picked.label,
          });
        } catch {
          return; // rendered below from createMapping.error
        }
        id = newName;
      }
      value = buildUSB({ host: "", mapping: id, usb3, spice: false });
    }
    onAdd(`usb${String(idx)}`, value);
    onClose();
  }

  const listingFailure = mappingsQuery.isError
    ? `Could not list the cluster's USB mappings${
        describeError(mappingsQuery.error)
          ? `: ${describeError(mappingsQuery.error)}`
          : "."
      }`
    : "";

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add USB Device (usb{String(idx)})</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <fieldset className="grid gap-1.5" disabled={busy}>
            <legend className="sr-only">Pass through</legend>
            {usbModes.map((m) => (
              <label
                key={m.value}
                className="flex items-baseline gap-2 text-sm"
              >
                <input
                  type="radio"
                  name="add-usb-mode"
                  value={m.value}
                  checked={mode === m.value}
                  onChange={() => {
                    setMode(m.value);
                    clearCreateError();
                  }}
                  className="h-4 w-4 translate-y-0.5 accent-primary"
                />
                <span>
                  {m.label}
                  <span className="ml-1.5 text-xs text-muted-foreground">
                    {m.hint}
                  </span>
                </span>
              </label>
            ))}
          </fieldset>

          {mode === "mapped" && (
            <div className="space-y-1">
              <Label htmlFor="add-usb-mapping" className="text-xs">
                Mapping
              </Label>
              {listingFailure ? (
                <p className="text-xs text-destructive">{listingFailure}</p>
              ) : mappingsQuery.isPending ? (
                <p className="text-xs text-muted-foreground">
                  Loading mappings…
                </p>
              ) : mappings.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  This cluster has no USB mappings yet.{" "}
                  {canCreateMapping
                    ? "Pick a host device or port instead, and Nexara creates one."
                    : "Creating one needs the Manage Cluster permission; ask an administrator."}
                </p>
              ) : (
                <select
                  id="add-usb-mapping"
                  className={selectClass}
                  value={mappingId}
                  disabled={busy}
                  onChange={(e) => {
                    setMappingId(e.target.value);
                  }}
                >
                  <option value="">Select a mapping...</option>
                  {mappings.map((m) => (
                    <option key={m.id} value={m.id}>
                      {mappingOptionLabel(m)}
                    </option>
                  ))}
                </select>
              )}
              {selectedMapping && (
                <MappingCheckList mapping={selectedMapping} node={nodeName} />
              )}
            </div>
          )}

          {mode === "device" && (
            <div className="space-y-1">
              <Label htmlFor="add-usb-device" className="text-xs">
                Device
              </Label>
              {hasDeviceList ? (
                <select
                  id="add-usb-device"
                  className={selectClass}
                  value={deviceId}
                  disabled={busy}
                  onChange={(e) => {
                    setDeviceId(e.target.value);
                    clearCreateError();
                  }}
                >
                  <option value="">Select a device...</option>
                  {candidates.map((d) => (
                    <option
                      key={`${usbDeviceId(d)}-${String(d.busnum)}-${String(d.devnum)}`}
                      value={usbDeviceId(d)}
                    >
                      {deviceLabel(d, usbDeviceId(d))} ({usbDeviceId(d)})
                    </option>
                  ))}
                </select>
              ) : (
                <Input
                  id="add-usb-device"
                  value={deviceId}
                  disabled={busy}
                  onChange={(e) => {
                    setDeviceId(e.target.value.trim());
                    clearCreateError();
                  }}
                  placeholder="vendor:product (e.g. 1234:5678)"
                  aria-invalid={typedIdError !== ""}
                />
              )}
              {typedIdError && (
                <p className="text-xs text-destructive">{typedIdError}</p>
              )}
            </div>
          )}

          {mode === "port" && (
            <div className="space-y-1">
              <Label htmlFor="add-usb-port" className="text-xs">
                Port
              </Label>
              {hasDeviceList ? (
                <select
                  id="add-usb-port"
                  className={selectClass}
                  value={port}
                  disabled={busy}
                  onChange={(e) => {
                    setPort(e.target.value);
                    clearCreateError();
                  }}
                >
                  <option value="">Select a port...</option>
                  {candidates.map((d) => (
                    <option key={usbPortPath(d)} value={usbPortPath(d)}>
                      {deviceLabel(d, usbDeviceId(d))} ({usbPortPath(d)})
                    </option>
                  ))}
                </select>
              ) : (
                <p className="text-xs text-muted-foreground">
                  {devices === undefined
                    ? "The node's USB devices are not available: still loading, or they could not be read."
                    : "The node lists no USB device on a port that can be passed through."}
                </p>
              )}
            </div>
          )}

          {isHostMode && picked && listingFailure && (
            <p className="text-xs text-destructive">{listingFailure}</p>
          )}
          {isHostMode && picked && mappingsQuery.isPending && (
            <p className="text-xs text-muted-foreground">
              Checking the cluster's USB mappings…
            </p>
          )}
          {isHostMode && picked && reusable && (
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">
                Uses the existing mapping “{reusable.id}”
                {cleanDeviceText(reusable.description)
                  ? ` (${cleanDeviceText(reusable.description)})`
                  : ""}
                .
              </p>
              {/* A device that is not plugged in now is reused all the same —
                  a new mapping would report the same — so say so here. */}
              {reusable.errors.length > 0 && (
                <MappingCheckList mapping={reusable} node={nodeName} />
              )}
            </div>
          )}
          {needsCreate && (
            <div className="space-y-1">
              <Label htmlFor="add-usb-mapping-name" className="text-xs">
                Mapping name
              </Label>
              <Input
                id="add-usb-mapping-name"
                value={newName}
                disabled={busy}
                onChange={(e) => {
                  setNameInput(e.target.value);
                  clearCreateError();
                }}
                aria-invalid={nameError !== ""}
              />
              {nameError && (
                <p className="text-xs text-destructive">{nameError}</p>
              )}
            </div>
          )}
          {isHostMode && (
            <p className="text-xs text-muted-foreground">
              Proxmox passes a USB device straight through only for root@pam,
              and Nexara connects with an API token, so the device goes through
              a cluster resource mapping
              {needsCreate
                ? ", which Nexara creates now, even if you then discard this change"
                : ""}
              . Proxmox will not start the VM while the device is missing
              {mode === "port" ? " or another device is on the port" : ""}.
            </p>
          )}
          {createDenied && (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              Creating a mapping needs the Manage Cluster permission. Pick an
              existing mapping instead, or ask an administrator.
            </p>
          )}
          {slotsFull && (
            <p className="text-xs text-destructive">
              All 14 USB slots are in use.
            </p>
          )}
          {createMapping.isError && (
            <p className="text-xs text-destructive">
              {describeError(createMapping.error) ||
                "Could not create the mapping."}
            </p>
          )}

          {mode === "spice" && (
            <p className="text-xs text-muted-foreground">
              SPICE USB redirection allows passing host USB devices through the
              SPICE client.
            </p>
          )}

          <div className="flex items-center gap-1.5">
            <Checkbox
              id="add-usb-usb3"
              checked={usb3}
              disabled={busy}
              onCheckedChange={(v) => {
                setUsb3(v === true);
              }}
            />
            <Label htmlFor="add-usb-usb3" className="cursor-pointer text-xs">
              USB 3.0
            </Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              void handleAdd();
            }}
            disabled={!canAdd}
          >
            {busy
              ? "Creating mapping…"
              : needsCreate
                ? "Create mapping & add"
                : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * What Proxmox reported for a mapping against the VM's node, or, when it
 * reported nothing, what the mapping passes through there.
 */
function MappingCheckList({
  mapping,
  node,
}: {
  mapping: USBMapping;
  node: string;
}) {
  if (mapping.errors.length === 0) {
    const entry = usbMappingEntryFor(mapping.map, node);
    if (!entry) return null;
    return (
      <p className="text-xs text-muted-foreground">
        On {node}: {entry.id}
        {entry.path ? ` on port ${entry.path}` : ""}
      </p>
    );
  }
  return (
    <ul className="space-y-0.5">
      {mapping.errors.map((e) => (
        <li
          key={`${e.severity}:${e.message}`}
          className={
            e.severity === "error"
              ? "text-xs text-destructive"
              : "text-xs text-amber-700 dark:text-amber-400"
          }
        >
          {e.message}
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------------
// PCI Dialog
// ---------------------------------------------------------------------------

function AddPCIDialog({
  config,
  devices,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  devices: NodePCIDevice[] | undefined;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [selectedDevice, setSelectedDevice] = useState("");
  const [pcie, setPcie] = useState(true);
  const [rombar, setRombar] = useState(true);
  const [xvga, setXvga] = useState(false);

  const idx = findNextIndex(config, "hostpci", 15);
  const canAdd = idx >= 0 && selectedDevice.length > 0;

  function handleAdd() {
    if (!canAdd) return;
    const val = buildPCI({
      host: selectedDevice,
      pcie,
      rombar,
      xvga,
      mdev: "",
    });
    onAdd(`hostpci${String(idx)}`, val);
    onClose();
  }

  // Group by IOMMU group
  const grouped = new Map<number, NodePCIDevice[]>();
  if (devices) {
    for (const d of devices) {
      const group = d.iommugroup;
      let list = grouped.get(group);
      if (!list) {
        list = [];
        grouped.set(group, list);
      }
      list.push(d);
    }
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Add PCI Device (hostpci{String(idx)})</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Device</Label>
            {devices && devices.length > 0 ? (
              <select
                className={selectClass}
                value={selectedDevice}
                onChange={(e) => {
                  setSelectedDevice(e.target.value);
                }}
              >
                <option value="">Select a device...</option>
                {Array.from(grouped.entries())
                  .sort(([a], [b]) => a - b)
                  .map(([group, devs]) => (
                    <optgroup
                      key={group}
                      label={`IOMMU Group ${String(group)}`}
                    >
                      {devs.map((d) => (
                        <option key={d.id} value={d.id}>
                          {d.id} — {d.device_name || d.vendor_name || "Unknown"}
                        </option>
                      ))}
                    </optgroup>
                  ))}
              </select>
            ) : (
              <Input
                value={selectedDevice}
                onChange={(e) => {
                  setSelectedDevice(e.target.value);
                }}
                placeholder="PCI address (e.g. 02:00.0)"
              />
            )}
          </div>
          <div className="flex flex-wrap items-center gap-4">
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-pci-pcie"
                checked={pcie}
                onCheckedChange={(v) => {
                  setPcie(v === true);
                }}
              />
              <Label htmlFor="add-pci-pcie" className="cursor-pointer text-xs">
                PCIe
              </Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-pci-rombar"
                checked={rombar}
                onCheckedChange={(v) => {
                  setRombar(v === true);
                }}
              />
              <Label
                htmlFor="add-pci-rombar"
                className="cursor-pointer text-xs"
              >
                ROM-BAR
              </Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-pci-xvga"
                checked={xvga}
                onCheckedChange={(v) => {
                  setXvga(v === true);
                }}
              />
              <Label htmlFor="add-pci-xvga" className="cursor-pointer text-xs">
                Primary GPU (x-vga)
              </Label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Serial Port Dialog
// ---------------------------------------------------------------------------

function AddSerialDialog({
  config,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [value, setValue] = useState("socket");

  const idx = findNextIndex(config, "serial", 3);
  const canAdd = idx >= 0;

  function handleAdd() {
    if (!canAdd) return;
    onAdd(`serial${String(idx)}`, buildSerial(value));
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Add Serial Port (serial{String(idx)})</DialogTitle>
        </DialogHeader>
        <div className="space-y-1">
          <Label className="text-xs">Type</Label>
          <select
            className={selectClass}
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
            }}
          >
            {serialOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// RNG Dialog
// ---------------------------------------------------------------------------

function AddRNGDialog({
  onAdd,
  onClose,
}: {
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [source, setSource] = useState("/dev/urandom");
  const [maxBytes, setMaxBytes] = useState("1024");
  const [period, setPeriod] = useState("1000");

  function handleAdd() {
    onAdd("rng0", buildRNG({ source, maxBytes, period }));
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Add VirtIO RNG</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Source</Label>
            <select
              className={selectClass}
              value={source}
              onChange={(e) => {
                setSource(e.target.value);
              }}
            >
              {rngSources.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label className="text-xs">Max Bytes</Label>
              <Input
                type="number"
                min={0}
                value={maxBytes}
                onChange={(e) => {
                  setMaxBytes(e.target.value);
                }}
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Period (ms)</Label>
              <Input
                type="number"
                min={0}
                value={period}
                onChange={(e) => {
                  setPeriod(e.target.value);
                }}
              />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd}>Add</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// VirtioFS Dialog
// ---------------------------------------------------------------------------

function AddVirtioFSDialog({
  config,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [dirid, setDirid] = useState("");
  const [cache, setCache] = useState("auto");
  const [directIo, setDirectIo] = useState(false);

  const idx = findNextIndex(config, "virtiofs", 9);
  const canAdd = idx >= 0 && dirid.length > 0;

  function handleAdd() {
    if (!canAdd) return;
    onAdd(`virtiofs${String(idx)}`, buildVirtioFS({ dirid, cache, directIo }));
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Add VirtioFS Share (virtiofs{String(idx)})</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Directory ID</Label>
            <Input
              value={dirid}
              onChange={(e) => {
                setDirid(e.target.value);
              }}
              placeholder="share-name"
            />
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Cache Mode</Label>
            <select
              className={selectClass}
              value={cache}
              onChange={(e) => {
                setCache(e.target.value);
              }}
            >
              {virtiofsCacheModes.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-1.5">
            <Checkbox
              id="add-vfs-dio"
              checked={directIo}
              onCheckedChange={(v) => {
                setDirectIo(v === true);
              }}
            />
            <Label htmlFor="add-vfs-dio" className="cursor-pointer text-xs">
              Direct I/O
            </Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// EFI Disk Dialog
// ---------------------------------------------------------------------------

function AddEFIDialog({
  diskStorages,
  onAdd,
  onClose,
}: {
  diskStorages: Array<{ storage: string; type: string }>;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [storage, setStorage] = useState(diskStorages[0]?.storage ?? "");
  const [efitype, setEfitype] = useState("4m");
  const [preEnrolledKeys, setPreEnrolledKeys] = useState(true);

  function handleAdd() {
    if (!storage) return;
    onAdd(
      "efidisk0",
      buildEFIDisk({
        volume: `${storage}:1`,
        storage,
        efitype,
        preEnrolledKeys,
      }),
    );
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Add EFI Disk</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Storage</Label>
            <select
              className={selectClass}
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
            >
              <option value="">Select...</option>
              {diskStorages.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage} ({s.type})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">EFI Type</Label>
            <select
              className={selectClass}
              value={efitype}
              onChange={(e) => {
                setEfitype(e.target.value);
              }}
            >
              {efiTypes.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-1.5">
            <Checkbox
              id="add-efi-keys"
              checked={preEnrolledKeys}
              onCheckedChange={(v) => {
                setPreEnrolledKeys(v === true);
              }}
            />
            <Label htmlFor="add-efi-keys" className="cursor-pointer text-xs">
              Pre-enrolled Keys (Secure Boot)
            </Label>
          </div>
          <p className="text-xs text-muted-foreground">
            BIOS will be set to OVMF (UEFI) when an EFI disk is added.
          </p>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!storage}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// TPM State Dialog
// ---------------------------------------------------------------------------

function AddTPMDialog({
  diskStorages,
  onAdd,
  onClose,
}: {
  diskStorages: Array<{ storage: string; type: string }>;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [storage, setStorage] = useState(diskStorages[0]?.storage ?? "");
  const [version, setVersion] = useState("v2.0");

  function handleAdd() {
    if (!storage) return;
    onAdd(
      "tpmstate0",
      buildTPMState({ volume: `${storage}:1`, storage, version }),
    );
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Add TPM State</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Storage</Label>
            <select
              className={selectClass}
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
            >
              <option value="">Select...</option>
              {diskStorages.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage} ({s.type})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Version</Label>
            <select
              className={selectClass}
              value={version}
              onChange={(e) => {
                setVersion(e.target.value);
              }}
            >
              {tpmVersions.map((v) => (
                <option key={v.value} value={v.value}>
                  {v.label}
                </option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!storage}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Cloud-Init Drive Dialog
// ---------------------------------------------------------------------------

const cloudInitBuses = [
  { value: "ide2", label: "IDE 2" },
  { value: "ide0", label: "IDE 0" },
  { value: "scsi1", label: "SCSI 1" },
  { value: "sata0", label: "SATA 0" },
];

function AddCloudInitDialog({
  config,
  diskStorages,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  diskStorages: Array<{ storage: string; type: string }>;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [storage, setStorage] = useState(diskStorages[0]?.storage ?? "");
  // Find first available slot
  const availableSlot =
    cloudInitBuses.find((b) => config[b.value] == null)?.value ?? "ide2";
  const [slot, setSlot] = useState(availableSlot);

  function handleAdd() {
    if (!storage) return;
    onAdd(slot, `${storage}:cloudinit`);
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Add Cloud-Init Drive</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Storage</Label>
            <select
              className={selectClass}
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
            >
              <option value="">Select...</option>
              {diskStorages.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage} ({s.type})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Disk Slot</Label>
            <select
              className={selectClass}
              value={slot}
              onChange={(e) => {
                setSlot(e.target.value);
              }}
            >
              {cloudInitBuses.map((b) => (
                <option
                  key={b.value}
                  value={b.value}
                  disabled={config[b.value] != null}
                >
                  {b.label}
                  {config[b.value] != null ? " (in use)" : ""}
                </option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!storage}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// CD/DVD Drive Dialog
// ---------------------------------------------------------------------------

const cdromBusSlots = [
  { value: "ide0", label: "IDE 0" },
  { value: "ide1", label: "IDE 1" },
  { value: "ide2", label: "IDE 2" },
  { value: "ide3", label: "IDE 3" },
  { value: "sata0", label: "SATA 0" },
  { value: "sata1", label: "SATA 1" },
  { value: "sata2", label: "SATA 2" },
  { value: "sata3", label: "SATA 3" },
  { value: "sata4", label: "SATA 4" },
  { value: "sata5", label: "SATA 5" },
];

function AddCDROMDialog({
  config,
  isoFiles,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  isoFiles: ISOFile[];
  onAdd: (key: string, isoVolid: string) => void;
  onClose: () => void;
}) {
  const availableSlot =
    cdromBusSlots.find((b) => config[b.value] == null)?.value ?? "ide2";
  const [slot, setSlot] = useState(availableSlot);
  const [iso, setIso] = useState("none");

  const slotFree = config[slot] == null;

  function handleAdd() {
    if (!slotFree) return;
    onAdd(slot, iso);
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add CD/DVD Drive</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">Bus/Slot</Label>
            <select
              className={selectClass}
              value={slot}
              onChange={(e) => {
                setSlot(e.target.value);
              }}
            >
              {cdromBusSlots.map((b) => (
                <option
                  key={b.value}
                  value={b.value}
                  disabled={config[b.value] != null}
                >
                  {b.label}
                  {config[b.value] != null ? " (in use)" : ""}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">ISO Image</Label>
            <select
              className={selectClass}
              value={iso}
              onChange={(e) => {
                setIso(e.target.value);
              }}
            >
              <option value="none">No media (empty drive)</option>
              {isoFiles.map((f) => (
                <option key={f.volid} value={f.volid}>
                  {f.volid}
                </option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleAdd} disabled={!slotFree}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
