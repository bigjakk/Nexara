import { useState } from "react";
import {
  ChevronDown,
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
} from "../../lib/vm-config-parsers";
import type { NodeUSBDevice, NodePCIDevice } from "../../api/vm-queries";
import {
  useCreatePCIMapping,
  useCreateUSBMapping,
  useNodePCIMappings,
  useNodeUSBMappings,
  type PCIMapping,
  type USBMapping,
} from "@/features/mappings/api/mapping-queries";
import {
  cleanDeviceText,
  findReusableUSBMapping,
  MAPPING_ID_PATTERN,
  passthroughCandidates,
  pickedUSBDevice,
  suggestMappingName,
  USB_DEVICE_ID_HINT,
  USB_DEVICE_ID_PATTERN,
} from "@/features/mappings/lib/usb-mapping";
import {
  findCheckedPCIMappingAt,
  findReusablePCIMapping,
  parsePCIMappingEntry,
  pciPassRisks,
} from "@/features/mappings/lib/pci-mapping";
import { usePCIPick } from "@/features/mappings/hooks/usePCIPick";
import { usePCIRiskAck } from "@/features/mappings/hooks/usePCIRiskAck";
import { PCIDevicePicker } from "@/features/mappings/components/PCIDevicePicker";
import { PCIRiskConfirm } from "@/features/mappings/components/PCIRiskConfirm";
import { USBDevicePicker } from "@/features/mappings/components/USBDevicePicker";
import {
  MappingCheckList,
  PCIMappingCheckList,
} from "@/features/mappings/components/MappingCheckList";
import type { VMConfig } from "../../types/vm";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

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
  const { t } = useTranslation("vms");
  const [dialog, setDialog] = useState<DeviceDialog>(null);

  const hasEfi = config["efidisk0"] != null;
  const hasTpm = config["tpmstate0"] != null;
  const hasRng = config["rng0"] != null;

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm" className="gap-1.5">
            <Plus className="h-4 w-4" /> {t("addDeviceMenu.addDevice")}
            <ChevronDown className="h-3.5 w-3.5 opacity-70" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuItem onClick={onAddDisk}>
            <HardDrive className="mr-2 h-4 w-4" /> {t("addDeviceMenu.hardDisk")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("cdrom");
            }}
          >
            <Disc className="mr-2 h-4 w-4" /> {t("addDeviceMenu.cdDVDDrive")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("nic");
            }}
          >
            <Network className="mr-2 h-4 w-4" />{" "}
            {t("addDeviceMenu.networkDevice")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setDialog("usb");
            }}
          >
            <Usb className="mr-2 h-4 w-4" /> {t("addDeviceMenu.usbDevice")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("pci");
            }}
          >
            <Cpu className="mr-2 h-4 w-4" /> {t("addDeviceMenu.pciDevice")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("serial");
            }}
          >
            <Terminal className="mr-2 h-4 w-4" />{" "}
            {t("addDeviceMenu.serialPort")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setDialog("cloudinit");
            }}
          >
            <Monitor className="mr-2 h-4 w-4" />{" "}
            {t("addDeviceMenu.cloudInitDrive")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("rng");
            }}
            disabled={hasRng}
          >
            <Dice1 className="mr-2 h-4 w-4" />{" "}
            {hasRng
              ? t("addDeviceMenu.virtioRngExists")
              : t("addDeviceMenu.virtioRng")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("virtiofs");
            }}
          >
            <FolderOpen className="mr-2 h-4 w-4" />{" "}
            {t("addDeviceMenu.virtioFSShare")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setDialog("efi");
            }}
            disabled={hasEfi}
          >
            <Key className="mr-2 h-4 w-4" />{" "}
            {hasEfi
              ? t("addDeviceMenu.efiDiskExists")
              : t("addDeviceMenu.efiDisk")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              setDialog("tpm");
            }}
            disabled={hasTpm}
          >
            <Shield className="mr-2 h-4 w-4" />{" "}
            {hasTpm
              ? t("addDeviceMenu.tpmStateExists")
              : t("addDeviceMenu.tpmState")}
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
          clusterId={clusterId}
          nodeName={nodeName}
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>
            {t("addDeviceMenu.addNetworkDeviceNetIdx", { idx: String(idx) })}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label className="text-xs">{t("addDeviceMenu.model")}</Label>
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
              <Label className="text-xs">{t("addDeviceMenu.bridge")}</Label>
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
              <Label className="text-xs">{t("addDeviceMenu.vlanTag")}</Label>
              <Input
                type="number"
                min={1}
                max={4094}
                value={vlan}
                onChange={(e) => {
                  setVlan(e.target.value);
                }}
                placeholder={t("addDeviceMenu.none")}
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">{t("addDeviceMenu.rateMBS")}</Label>
              <Input
                type="number"
                min={0}
                value={rate}
                onChange={(e) => {
                  setRate(e.target.value);
                }}
                placeholder={t("addDeviceMenu.unlimited")}
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">{t("addDeviceMenu.mtu")}</Label>
              <Input
                type="number"
                min={0}
                value={mtu}
                onChange={(e) => {
                  setMtu(e.target.value);
                }}
                placeholder={t("addDeviceMenu.default")}
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
                {t("addDeviceMenu.firewall")}
              </Label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            {t("addDeviceMenu.add")}
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
      get label() {
        return i18n.t("vms:addDeviceMenu.mappedDevice");
      },
      get hint() {
        return i18n.t("vms:addDeviceMenu.aClusterResourceMapping");
      },
    },
    {
      value: "device",
      get label() {
        return i18n.t("vms:addDeviceMenu.hostDevice");
      },
      get hint() {
        return i18n.t("vms:addDeviceMenu.thisDeviceOnAnyPort");
      },
    },
    {
      // Not "whatever is on the port": a port mapping carries the device's
      // id too, and Proxmox refuses to start the VM when another device is
      // there (assert_valid, pve-guest-common src/PVE/Mapping/USB.pm). What
      // the port adds is telling two identical devices apart.
      value: "port",
      get label() {
        return i18n.t("vms:addDeviceMenu.hostUSBPort");
      },
      get hint() {
        return i18n.t("vms:addDeviceMenu.thisDeviceOnThisPort");
      },
    },
    {
      value: "spice",
      get label() {
        return i18n.t("vms:addDeviceMenu.spicePort");
      },
      get hint() {
        return i18n.t("vms:addDeviceMenu.aDeviceRedirectedFromThe");
      },
    },
  ];

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
  const { t } = useTranslation("vms");
  const { canManage } = usePermissions();
  const canCreateMapping = canManage("cluster");
  const mappingsQuery = useNodeUSBMappings(clusterId, nodeName);
  const createMapping = useCreateUSBMapping(clusterId);
  const settle = useSaveOutcome();
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
  const picked =
    mode === "device" || mode === "port"
      ? pickedUSBDevice(mode, deviceId, port, candidates)
      : null;

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
      nameError = t("addDeviceMenu.mappingNamePattern");
    } else if (mappings.some((m) => m.id === newName)) {
      nameError = t("addDeviceMenu.mappingNameTaken", { newName });
    }
  }
  const typedIdError =
    mode === "device" &&
    !hasDeviceList &&
    deviceId !== "" &&
    !USB_DEVICE_ID_PATTERN.test(deviceId)
      ? USB_DEVICE_ID_HINT
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

  function handleAdd() {
    if (!canAdd) return;
    const add = (value: string) => {
      onAdd(`usb${String(idx)}`, value);
      onClose();
    };
    if (mode === "spice") {
      add(buildUSB({ host: "", mapping: "", usb3, spice: true }));
    } else if (mode === "mapped") {
      add(buildUSB({ host: "", mapping: mappingId, usb3, spice: false }));
    } else {
      if (!picked) return;
      const reused = reusable?.id;
      if (reused !== undefined) {
        add(buildUSB({ host: "", mapping: reused, usb3, spice: false }));
        return;
      }
      // A create that lands after the dialog has gone stages nothing, and a
      // failure then is toasted.
      settle(
        createMapping.mutateAsync({
          mapping_id: newName,
          node: nodeName,
          device_id: picked.deviceId,
          ...(picked.path ? { path: picked.path } : {}),
          // Already cleaned: the label is deviceLabel's.
          description: picked.label,
        }),
        {
          action: t("addDeviceMenu.creatingUSBMappingNewName", { newName }),
          onSuccess: () => {
            add(buildUSB({ host: "", mapping: newName, usb3, spice: false }));
          },
          onError: () => undefined, // rendered below from createMapping.error
        },
      );
    }
  }

  const listingFailure = mappingsQuery.isError
    ? describeError(mappingsQuery.error)
      ? t("addDeviceMenu.couldNotListUsbMappingsReason", {
          reason: describeError(mappingsQuery.error),
        })
      : t("addDeviceMenu.couldNotListUsbMappings")
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
          <DialogTitle>
            {t("addDeviceMenu.addUSBDeviceUsbIdx", { idx: String(idx) })}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <fieldset className="grid gap-1.5" disabled={busy}>
            <legend className="sr-only">
              {t("addDeviceMenu.passThrough")}
            </legend>
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
                {t("addDeviceMenu.mapping")}
              </Label>
              {listingFailure ? (
                <p className="text-xs text-destructive">{listingFailure}</p>
              ) : mappingsQuery.isPending ? (
                <p className="text-xs text-muted-foreground">
                  {t("addDeviceMenu.loadingMappings")}
                </p>
              ) : mappings.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  {t("addDeviceMenu.thisClusterHasNoUSB")}{" "}
                  {canCreateMapping
                    ? t("addDeviceMenu.pickAHostDeviceOr")
                    : t("addDeviceMenu.creatingOneNeedsTheManage")}
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
                  <option value="">{t("addDeviceMenu.selectAMapping")}</option>
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
                {t("addDeviceMenu.device")}
              </Label>
              <USBDevicePicker
                id="add-usb-device"
                mode="device"
                devices={devices}
                value={deviceId}
                disabled={busy}
                onChange={(value) => {
                  setDeviceId(value);
                  clearCreateError();
                }}
                invalid={typedIdError !== ""}
              />
              {typedIdError && (
                <p className="text-xs text-destructive">{typedIdError}</p>
              )}
            </div>
          )}

          {mode === "port" && (
            <div className="space-y-1">
              <Label htmlFor="add-usb-port" className="text-xs">
                {t("addDeviceMenu.port")}
              </Label>
              <USBDevicePicker
                id="add-usb-port"
                mode="port"
                devices={devices}
                value={port}
                disabled={busy}
                onChange={(value) => {
                  setPort(value);
                  clearCreateError();
                }}
              />
            </div>
          )}

          {isHostMode && picked && listingFailure && (
            <p className="text-xs text-destructive">{listingFailure}</p>
          )}
          {isHostMode && picked && mappingsQuery.isPending && (
            <p className="text-xs text-muted-foreground">
              {t("addDeviceMenu.checkingTheClusterSUSB")}
            </p>
          )}
          {isHostMode && picked && reusable && (
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">
                {cleanDeviceText(reusable.description)
                  ? t("addDeviceMenu.usesTheExistingMappingDescribed", {
                      id: reusable.id,
                      description: cleanDeviceText(reusable.description),
                    })
                  : t("addDeviceMenu.usesTheExistingMapping", {
                      id: reusable.id,
                    })}
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
                {t("addDeviceMenu.mappingName")}
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
              {t("addDeviceMenu.proxmoxPassesAUSBDevice")}
              {needsCreate ? t("addDeviceMenu.whichNexaraCreatesNowEven") : ""}
              {t("addDeviceMenu.proxmoxWillNotStartThe")}
              {mode === "port" ? t("addDeviceMenu.orAnotherDeviceIsOn") : ""}.
            </p>
          )}
          {createDenied && (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              {t("addDeviceMenu.creatingAMappingNeedsThe")}
            </p>
          )}
          {slotsFull && (
            <p className="text-xs text-destructive">
              {t("addDeviceMenu.all14USBSlotsAre")}
            </p>
          )}
          {createMapping.isError && (
            <p className="text-xs text-destructive">
              {describeError(createMapping.error) ||
                t("addDeviceMenu.couldNotCreateTheMapping")}
            </p>
          )}

          {mode === "spice" && (
            <p className="text-xs text-muted-foreground">
              {t("addDeviceMenu.spiceUSBRedirectionAllowsPassing")}
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
              {t("addDeviceMenu.usb30")}
            </Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button
            onClick={() => {
              handleAdd();
            }}
            disabled={!canAdd}
          >
            {busy
              ? t("addDeviceMenu.creatingMapping")
              : needsCreate
                ? t("addDeviceMenu.createMappingAdd")
                : t("addDeviceMenu.add")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// PCI Dialog
// ---------------------------------------------------------------------------

type PCIMode = "mapped" | "device";

// Proxmox's own dialog offers the same two (pve-manager
// www/manager6/qemu/PCIEdit.js). Its raw one writes the host address, which
// qemu-server refuses to anyone but root@pam, so here it goes through a
// mapping instead — see AddPCIDialog.
const pciModes: ReadonlyArray<{ value: PCIMode; label: string; hint: string }> =
  [
    {
      value: "mapped",
      get label() {
        return i18n.t("vms:addDeviceMenu.mappedDevice");
      },
      get hint() {
        return i18n.t("vms:addDeviceMenu.aClusterResourceMapping");
      },
    },
    {
      value: "device",
      get label() {
        return i18n.t("vms:addDeviceMenu.hostDevice");
      },
      get hint() {
        return i18n.t("vms:addDeviceMenu.aPCIDeviceOfThis");
      },
    },
  ];

function pciMappingOptionLabel(m: PCIMapping): string {
  const description = cleanDeviceText(m.description);
  let label = description ? `${m.id} — ${description}` : m.id;
  if (m.checks.some((e) => e.severity === "error")) label += " (error)";
  else if (m.checks.length > 0) label += " (warning)";
  return label;
}

/**
 * Adds a hostpciN. A host device can only go through a cluster resource
 * mapping: qemu-server's check_hostpci_perm lets nobody but root@pam write a
 * hostpciN naming a host device, and Nexara connects with an API token, which
 * never is root@pam. So picking a host device reuses the mapping that already
 * passes exactly that device, or creates one (which needs manage:cluster) and
 * stages mapping=<name>. The server builds the new mapping's entry from the
 * node's own report of the device, since Proxmox refuses to start a VM whose
 * device does not match its mapping exactly.
 *
 * PCIe is offered only on the q35 machine type, as Proxmox's own dialog does:
 * qemu-server refuses to start any other VM with pcie=1 ("q35 machine model
 * is not enabled", print_hostpci_devices in src/PVE/QemuServer/PCI.pm).
 *
 * While a create is in flight the dialog is locked, for AddUSBDialog's reason.
 */
function AddPCIDialog({
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
  devices: NodePCIDevice[] | undefined;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation("vms");
  const { canManage } = usePermissions();
  const canCreateMapping = canManage("cluster");
  const mappingsQuery = useNodePCIMappings(clusterId, nodeName);
  const createMapping = useCreatePCIMapping(clusterId);
  const settle = useSaveOutcome();
  const busy = createMapping.isPending;

  // "q35" anywhere in the machine type — pc-q35-9.0, q35 — as Proxmox's
  // dialog tests it. The hardware panel passes the type alone, "pc" (i440fx)
  // when the VM sets none.
  const machine = config["machine"];
  const isQ35 = typeof machine === "string" && machine.includes("q35");

  const [mode, setMode] = useState<PCIMode>("device");
  const [mappingId, setMappingId] = useState("");
  // The host device picked, typed when the node's device list is not there.
  const pick = usePCIPick(devices, nodeName);
  // null until the operator types, so the suggestion follows the pick.
  const [nameInput, setNameInput] = useState<string | null>(null);
  const [pcie, setPcie] = useState(isQ35);
  const [rombar, setRombar] = useState(true);
  const [xvga, setXvga] = useState(false);

  const idx = findNextIndex(config, "hostpci", 15);
  const mappings = mappingsQuery.data ?? [];
  const deviceList = pick.deviceList;

  // The address a host pick passes through: the device, or its slot for all
  // of its functions.
  const pickedPath = mode === "device" ? pick.path : "";
  const label = pick.label;
  const expected = pickedPath !== "" ? pick.expected : undefined;

  // Reuse and the taken-name check both read the listing, so a host pick
  // waits for it, for AddUSBDialog's reason.
  const listingReady = mappingsQuery.isSuccess;
  // Without the device's record to compare with — a typed address, or a
  // function 0 the list leaves out — only a mapping Proxmox checked clean
  // here is known to pass this device.
  const reusable = !listingReady
    ? undefined
    : expected
      ? findReusablePCIMapping(mappings, expected.entry, expected.mdev)
      : pickedPath !== ""
        ? findCheckedPCIMappingAt(mappings, nodeName, pickedPath)
        : undefined;
  const needsCreate =
    pickedPath !== "" && listingReady && reusable === undefined;
  const suggestedName =
    pickedPath !== ""
      ? suggestMappingName(label, new Set(mappings.map((m) => m.id)), "pci")
      : "";
  const newName = nameInput ?? suggestedName;
  let nameError = "";
  if (needsCreate) {
    if (!MAPPING_ID_PATTERN.test(newName)) {
      nameError = t("addDeviceMenu.mappingNamePattern");
    } else if (mappings.some((m) => m.id === newName)) {
      nameError = t("addDeviceMenu.mappingNameTaken", { newName });
    }
  }

  const selectedMapping = mappings.find((m) => m.id === mappingId);
  const slotsFull = idx < 0;
  const createDenied = needsCreate && !canCreateMapping;

  // What the VM would take from the node: the picked device, or the devices
  // the mapping names here. The operator is asked before a device that looks
  // like one the node needs is added — asked, not stopped: passing a spare
  // disk controller or NIC through is what the dialog is for — and before one
  // the dialog cannot look at, which it cannot call safe. The tick holds only
  // for the pick it was given for.
  const passedPaths =
    mode === "device"
      ? pickedPath !== ""
        ? [pickedPath]
        : []
      : (selectedMapping?.map ?? [])
          .map(parsePCIMappingEntry)
          .filter((e) => e.node === nodeName)
          .map((e) => e.path);
  const hostRisks = pciPassRisks(passedPaths, deviceList, nodeName);
  const risk = usePCIRiskAck(`${mode}|${pickedPath}|${mappingId}`, hostRisks);
  const risksAccepted = risk.accepted;

  const canAdd =
    !slotsFull &&
    !busy &&
    risksAccepted &&
    ((mode === "mapped" && selectedMapping !== undefined) ||
      (mode === "device" &&
        pickedPath !== "" &&
        listingReady &&
        (reusable !== undefined || (!createDenied && nameError === ""))));

  // A new pick or name makes the last refusal stale. Never while a create is
  // in flight: resetting then would detach the dialog from it.
  function clearCreateError() {
    if (!createMapping.isPending) createMapping.reset();
  }

  function handleAdd() {
    if (!canAdd) return;
    if (mode === "mapped") {
      add(mappingId);
    } else if (reusable) {
      add(reusable.id);
    } else {
      // A create that lands after the dialog has gone stages nothing, and a
      // failure then is toasted.
      settle(
        createMapping.mutateAsync({
          mapping_id: newName,
          node: nodeName,
          path: pickedPath,
          // Already cleaned: pciDeviceLabel's.
          ...(label ? { description: label } : {}),
        }),
        {
          action: t("addDeviceMenu.creatingPCIMappingNewName", { newName }),
          onSuccess: () => {
            add(newName);
          },
          onError: () => undefined, // rendered below from createMapping.error
        },
      );
    }
  }

  function add(id: string) {
    onAdd(
      `hostpci${String(idx)}`,
      buildPCI({
        host: "",
        mapping: id,
        pcie: isQ35 && pcie,
        rombar,
        xvga,
        mdev: "",
        romfile: "",
      }),
    );
    onClose();
  }

  const listingFailure = mappingsQuery.isError
    ? describeError(mappingsQuery.error)
      ? t("addDeviceMenu.couldNotListPciMappingsReason", {
          reason: describeError(mappingsQuery.error),
        })
      : t("addDeviceMenu.couldNotListPciMappings")
    : "";

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {t("addDeviceMenu.addPCIDeviceHostpciIdx", { idx: String(idx) })}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <fieldset className="grid gap-1.5" disabled={busy}>
            <legend className="sr-only">
              {t("addDeviceMenu.passThrough")}
            </legend>
            {pciModes.map((m) => (
              <label
                key={m.value}
                className="flex items-baseline gap-2 text-sm"
              >
                <input
                  type="radio"
                  name="add-pci-mode"
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
              <Label htmlFor="add-pci-mapping" className="text-xs">
                {t("addDeviceMenu.mapping")}
              </Label>
              {listingFailure ? (
                <p className="text-xs text-destructive">{listingFailure}</p>
              ) : mappingsQuery.isPending ? (
                <p className="text-xs text-muted-foreground">
                  {t("addDeviceMenu.loadingMappings")}
                </p>
              ) : mappings.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  {t("addDeviceMenu.thisClusterHasNoPCI")}{" "}
                  {canCreateMapping
                    ? t("addDeviceMenu.pickAHostDeviceInstead")
                    : t("addDeviceMenu.creatingOneNeedsTheManage")}
                </p>
              ) : (
                <select
                  id="add-pci-mapping"
                  className={selectClass}
                  value={mappingId}
                  disabled={busy}
                  onChange={(e) => {
                    setMappingId(e.target.value);
                  }}
                >
                  <option value="">{t("addDeviceMenu.selectAMapping")}</option>
                  {mappings.map((m) => (
                    <option key={m.id} value={m.id}>
                      {pciMappingOptionLabel(m)}
                    </option>
                  ))}
                </select>
              )}
              {selectedMapping && (
                <PCIMappingCheckList
                  mapping={selectedMapping}
                  node={nodeName}
                />
              )}
            </div>
          )}

          {mode === "device" && (
            <PCIDevicePicker
              idPrefix="add-pci"
              pick={pick}
              disabled={busy}
              onPicked={clearCreateError}
            />
          )}

          {mode === "device" && pickedPath !== "" && listingFailure && (
            <p className="text-xs text-destructive">{listingFailure}</p>
          )}
          {mode === "device" &&
            pickedPath !== "" &&
            mappingsQuery.isPending && (
              <p className="text-xs text-muted-foreground">
                {t("addDeviceMenu.checkingTheClusterSPCI")}
              </p>
            )}
          {mode === "device" && reusable && (
            <div className="space-y-0.5">
              <p className="text-xs text-muted-foreground">
                {cleanDeviceText(reusable.description)
                  ? t("addDeviceMenu.usesTheExistingMappingDescribed", {
                      id: reusable.id,
                      description: cleanDeviceText(reusable.description),
                    })
                  : t("addDeviceMenu.usesTheExistingMapping", {
                      id: reusable.id,
                    })}
              </p>
              {reusable.checks.length > 0 && (
                <PCIMappingCheckList mapping={reusable} node={nodeName} />
              )}
            </div>
          )}
          {needsCreate && (
            <div className="space-y-1">
              <Label htmlFor="add-pci-mapping-name" className="text-xs">
                {t("addDeviceMenu.mappingName")}
              </Label>
              <Input
                id="add-pci-mapping-name"
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
          {mode === "device" && (
            <p className="text-xs text-muted-foreground">
              {t("addDeviceMenu.proxmoxPassesAPCIDevice")}
              {needsCreate ? t("addDeviceMenu.whichNexaraCreatesNowEven") : ""}
              {t("addDeviceMenu.proxmoxWillNotStartThe2")}
            </p>
          )}
          {createDenied && (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              {t("addDeviceMenu.creatingAMappingNeedsThe")}
            </p>
          )}
          <PCIRiskConfirm
            idPrefix="add-pci"
            node={nodeName}
            risks={hostRisks}
            accepted={risksAccepted}
            onAcceptedChange={risk.setAccepted}
            disabled={busy}
          />
          {slotsFull && (
            <p className="text-xs text-destructive">
              {t("addDeviceMenu.all16PCISlotsAre")}
            </p>
          )}
          {createMapping.isError && (
            <p className="text-xs text-destructive">
              {describeError(createMapping.error) ||
                t("addDeviceMenu.couldNotCreateTheMapping")}
            </p>
          )}

          <div className="flex flex-wrap items-center gap-4">
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-pci-pcie"
                checked={isQ35 && pcie}
                disabled={busy || !isQ35}
                onCheckedChange={(v) => {
                  setPcie(v === true);
                }}
              />
              <Label htmlFor="add-pci-pcie" className="cursor-pointer text-xs">
                {t("addDeviceMenu.pCIe")}
                {!isQ35 && (
                  <span className="ml-1 text-muted-foreground">
                    {t("addDeviceMenu.q35Only")}
                  </span>
                )}
              </Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-pci-rombar"
                checked={rombar}
                disabled={busy}
                onCheckedChange={(v) => {
                  setRombar(v === true);
                }}
              />
              <Label
                htmlFor="add-pci-rombar"
                className="cursor-pointer text-xs"
              >
                {t("addDeviceMenu.romBAR")}
              </Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox
                id="add-pci-xvga"
                checked={xvga}
                disabled={busy}
                onCheckedChange={(v) => {
                  setXvga(v === true);
                }}
              />
              <Label htmlFor="add-pci-xvga" className="cursor-pointer text-xs">
                {t("addDeviceMenu.primaryGPUXVga")}
              </Label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button
            onClick={() => {
              handleAdd();
            }}
            disabled={!canAdd}
          >
            {busy
              ? t("addDeviceMenu.creatingMapping")
              : needsCreate
                ? t("addDeviceMenu.createMappingAdd")
                : t("addDeviceMenu.add")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Serial Port Dialog
// ---------------------------------------------------------------------------

// $update_vm_api (src/PVE/API2/Qemu.pm) lets nobody but root@pam set a
// serialN to anything but a socket, and Nexara connects with an API token,
// which never is root@pam. So the host ports are listed, to say they exist
// and why they are not offered, but cannot be picked: Proxmox would refuse
// one, and the Save it is in.
function AddSerialDialog({
  config,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation("vms");
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
          <DialogTitle>
            {t("addDeviceMenu.addSerialPortSerialIdx", { idx: String(idx) })}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-1">
          <Label className="text-xs">{t("addDeviceMenu.type")}</Label>
          <select
            className={selectClass}
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
            }}
          >
            {serialOptions.map((o) => (
              <option
                key={o.value}
                value={o.value}
                disabled={o.value !== "socket"}
              >
                {o.value === "socket"
                  ? o.label
                  : t("addDeviceMenu.labelRootPamOnly", { label: o.label })}
              </option>
            ))}
          </select>
        </div>
        <p className="text-xs text-muted-foreground">
          {t("addDeviceMenu.proxmoxAttachesAHostSerial")}
        </p>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            {t("addDeviceMenu.add")}
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>{t("addDeviceMenu.addVirtIORNG")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.source")}</Label>
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
              <Label className="text-xs">{t("addDeviceMenu.maxBytes")}</Label>
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
              <Label className="text-xs">{t("addDeviceMenu.periodMs")}</Label>
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
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd}>{t("addDeviceMenu.add")}</Button>
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>
            {t("addDeviceMenu.addVirtioFSShareVirtiofsIdx", {
              idx: String(idx),
            })}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.directoryID")}</Label>
            <Input
              value={dirid}
              onChange={(e) => {
                setDirid(e.target.value);
              }}
              placeholder="share-name"
            />
          </div>
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.cacheMode")}</Label>
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
              {t("addDeviceMenu.directIO")}
            </Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!canAdd}>
            {t("addDeviceMenu.add")}
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>{t("addDeviceMenu.addEFIDisk")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.storage")}</Label>
            <select
              className={selectClass}
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
            >
              <option value="">{t("addDeviceMenu.select")}</option>
              {diskStorages.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage} ({s.type})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.efiType")}</Label>
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
              {t("addDeviceMenu.preEnrolledKeysSecureBoot")}
            </Label>
          </div>
          <p className="text-xs text-muted-foreground">
            {t("addDeviceMenu.biosWillBeSetTo")}
          </p>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!storage}>
            {t("addDeviceMenu.add")}
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>{t("addDeviceMenu.addTPMState")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.storage")}</Label>
            <select
              className={selectClass}
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
            >
              <option value="">{t("addDeviceMenu.select")}</option>
              {diskStorages.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage} ({s.type})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.version")}</Label>
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
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!storage}>
            {t("addDeviceMenu.add")}
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>{t("addDeviceMenu.addCloudInitDrive")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.storage")}</Label>
            <select
              className={selectClass}
              value={storage}
              onChange={(e) => {
                setStorage(e.target.value);
              }}
            >
              <option value="">{t("addDeviceMenu.select")}</option>
              {diskStorages.map((s) => (
                <option key={s.storage} value={s.storage}>
                  {s.storage} ({s.type})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.diskSlot")}</Label>
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
                  {config[b.value] != null ? t("addDeviceMenu.inUse") : ""}
                </option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!storage}>
            {t("addDeviceMenu.add")}
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
  const { t } = useTranslation("vms");
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
          <DialogTitle>{t("addDeviceMenu.addCDDVDDrive")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.busSlot")}</Label>
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
                  {config[b.value] != null ? t("addDeviceMenu.inUse") : ""}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">{t("addDeviceMenu.isoImage")}</Label>
            <select
              className={selectClass}
              value={iso}
              onChange={(e) => {
                setIso(e.target.value);
              }}
            >
              <option value="none">
                {t("addDeviceMenu.noMediaEmptyDrive")}
              </option>
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
            {t("addDeviceMenu.cancel")}
          </Button>
          <Button onClick={handleAdd} disabled={!slotFree}>
            {t("addDeviceMenu.add")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
