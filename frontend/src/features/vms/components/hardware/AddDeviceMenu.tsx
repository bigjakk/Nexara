import { useState } from "react";
import { Plus, Network, Usb, Cpu, Monitor, HardDrive, Key, Shield, Dice1, FolderOpen, Terminal, Disc } from "lucide-react";
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
import type { VMConfig } from "../../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

interface ISOFile {
  volid: string;
  content: string;
}

interface AddDeviceMenuProps {
  config: VMConfig;
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
          <DropdownMenuItem onClick={() => { setDialog("cdrom"); }}>
            <Disc className="mr-2 h-4 w-4" /> CD/DVD Drive
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => { setDialog("nic"); }}>
            <Network className="mr-2 h-4 w-4" /> Network Device
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => { setDialog("usb"); }}>
            <Usb className="mr-2 h-4 w-4" /> USB Device
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => { setDialog("pci"); }}>
            <Cpu className="mr-2 h-4 w-4" /> PCI Device
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => { setDialog("serial"); }}>
            <Terminal className="mr-2 h-4 w-4" /> Serial Port
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => { setDialog("cloudinit"); }}>
            <Monitor className="mr-2 h-4 w-4" /> Cloud-Init Drive
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => { setDialog("rng"); }} disabled={hasRng}>
            <Dice1 className="mr-2 h-4 w-4" /> VirtIO RNG {hasRng && "(exists)"}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => { setDialog("virtiofs"); }}>
            <FolderOpen className="mr-2 h-4 w-4" /> VirtioFS Share
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => { setDialog("efi"); }} disabled={hasEfi}>
            <Key className="mr-2 h-4 w-4" /> EFI Disk {hasEfi && "(exists)"}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => { setDialog("tpm"); }} disabled={hasTpm}>
            <Shield className="mr-2 h-4 w-4" /> TPM State {hasTpm && "(exists)"}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {dialog === "nic" && (
        <AddNICDialog
          config={config}
          bridges={bridges}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "usb" && (
        <AddUSBDialog
          config={config}
          devices={usbDevices}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "pci" && (
        <AddPCIDialog
          config={config}
          devices={pciDevices}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "serial" && (
        <AddSerialDialog
          config={config}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "rng" && (
        <AddRNGDialog
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "virtiofs" && (
        <AddVirtioFSDialog
          config={config}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "efi" && (
        <AddEFIDialog
          diskStorages={diskStorages}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "tpm" && (
        <AddTPMDialog
          diskStorages={diskStorages}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "cloudinit" && (
        <AddCloudInitDialog
          config={config}
          diskStorages={diskStorages}
          onAdd={onAddDevice}
          onClose={() => { setDialog(null); }}
        />
      )}
      {dialog === "cdrom" && (
        <AddCDROMDialog
          config={config}
          isoFiles={isoFiles}
          onAdd={onAddCDROM}
          onClose={() => { setDialog(null); }}
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
              <select className={selectClass} value={model} onChange={(e) => { setModel(e.target.value); }}>
                {netModels.map((m) => (<option key={m.value} value={m.value}>{m.label}</option>))}
              </select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Bridge</Label>
              {bridges.length > 0 ? (
                <select className={selectClass} value={bridge} onChange={(e) => { setBridge(e.target.value); }}>
                  {bridges.map((b) => (<option key={b} value={b}>{b}</option>))}
                </select>
              ) : (
                <Input value={bridge} onChange={(e) => { setBridge(e.target.value); }} placeholder="vmbr0" />
              )}
            </div>
            <div className="space-y-1">
              <Label className="text-xs">VLAN Tag</Label>
              <Input type="number" min={1} max={4094} value={vlan} onChange={(e) => { setVlan(e.target.value); }} placeholder="None" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Rate (MB/s)</Label>
              <Input type="number" min={0} value={rate} onChange={(e) => { setRate(e.target.value); }} placeholder="Unlimited" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">MTU</Label>
              <Input type="number" min={0} value={mtu} onChange={(e) => { setMtu(e.target.value); }} placeholder="Default" />
            </div>
          </div>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1.5">
              <Checkbox id="add-nic-fw" checked={firewall} onCheckedChange={(v) => { setFirewall(v === true); }} />
              <Label htmlFor="add-nic-fw" className="cursor-pointer text-xs">Firewall</Label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!canAdd}>Add</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// USB Dialog
// ---------------------------------------------------------------------------

function AddUSBDialog({
  config,
  devices,
  onAdd,
  onClose,
}: {
  config: VMConfig;
  devices: NodeUSBDevice[] | undefined;
  onAdd: (key: string, value: string) => void;
  onClose: () => void;
}) {
  const [mode, setMode] = useState<"host" | "spice">("host");
  const [selectedDevice, setSelectedDevice] = useState("");
  const [usb3, setUsb3] = useState(true);

  const idx = findNextIndex(config, "usb", 13);
  const canAdd = idx >= 0 && (mode === "spice" || selectedDevice.length > 0);

  function handleAdd() {
    if (!canAdd) return;
    const val = buildUSB({
      host: mode === "host" ? selectedDevice : "",
      usb3,
      spice: mode === "spice",
    });
    onAdd(`usb${String(idx)}`, val);
    onClose();
  }

  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add USB Device (usb{String(idx)})</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="flex gap-2">
            <Button
              variant={mode === "host" ? "default" : "outline"}
              size="sm"
              onClick={() => { setMode("host"); }}
            >
              Host Device
            </Button>
            <Button
              variant={mode === "spice" ? "default" : "outline"}
              size="sm"
              onClick={() => { setMode("spice"); }}
            >
              SPICE Redirect
            </Button>
          </div>

          {mode === "host" && (
            <div className="space-y-1">
              <Label className="text-xs">Device</Label>
              {devices && devices.length > 0 ? (
                <select className={selectClass} value={selectedDevice} onChange={(e) => { setSelectedDevice(e.target.value); }}>
                  <option value="">Select a device...</option>
                  {devices
                    .filter((d) => d.class !== 9) // exclude hubs
                    .map((d) => {
                      const id = `${d.vendid}:${d.prodid}`;
                      const label = d.product || d.manufacturer || id;
                      return (
                        <option key={`${id}-${String(d.busnum)}-${String(d.devnum)}`} value={id}>
                          {label} ({id})
                        </option>
                      );
                    })}
                </select>
              ) : (
                <Input
                  value={selectedDevice}
                  onChange={(e) => { setSelectedDevice(e.target.value); }}
                  placeholder="vendor:product (e.g. 058f:6387)"
                />
              )}
            </div>
          )}

          {mode === "spice" && (
            <p className="text-xs text-muted-foreground">
              SPICE USB redirection allows passing host USB devices through the SPICE client.
            </p>
          )}

          <div className="flex items-center gap-1.5">
            <Checkbox id="add-usb-usb3" checked={usb3} onCheckedChange={(v) => { setUsb3(v === true); }} />
            <Label htmlFor="add-usb-usb3" className="cursor-pointer text-xs">USB 3.0</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!canAdd}>Add</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
    const val = buildPCI({ host: selectedDevice, pcie, rombar, xvga, mdev: "" });
    onAdd(`hostpci${String(idx)}`, val);
    onClose();
  }

  // Group by IOMMU group
  const grouped = new Map<number, NodePCIDevice[]>();
  if (devices) {
    for (const d of devices) {
      const group = d.iommugroup;
      if (!grouped.has(group)) grouped.set(group, []);
      grouped.get(group)!.push(d);
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
              <select className={selectClass} value={selectedDevice} onChange={(e) => { setSelectedDevice(e.target.value); }}>
                <option value="">Select a device...</option>
                {Array.from(grouped.entries())
                  .sort(([a], [b]) => a - b)
                  .map(([group, devs]) => (
                    <optgroup key={group} label={`IOMMU Group ${String(group)}`}>
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
                onChange={(e) => { setSelectedDevice(e.target.value); }}
                placeholder="PCI address (e.g. 02:00.0)"
              />
            )}
          </div>
          <div className="flex flex-wrap items-center gap-4">
            <div className="flex items-center gap-1.5">
              <Checkbox id="add-pci-pcie" checked={pcie} onCheckedChange={(v) => { setPcie(v === true); }} />
              <Label htmlFor="add-pci-pcie" className="cursor-pointer text-xs">PCIe</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="add-pci-rombar" checked={rombar} onCheckedChange={(v) => { setRombar(v === true); }} />
              <Label htmlFor="add-pci-rombar" className="cursor-pointer text-xs">ROM-BAR</Label>
            </div>
            <div className="flex items-center gap-1.5">
              <Checkbox id="add-pci-xvga" checked={xvga} onCheckedChange={(v) => { setXvga(v === true); }} />
              <Label htmlFor="add-pci-xvga" className="cursor-pointer text-xs">Primary GPU (x-vga)</Label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!canAdd}>Add</Button>
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
          <select className={selectClass} value={value} onChange={(e) => { setValue(e.target.value); }}>
            {serialOptions.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
          </select>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!canAdd}>Add</Button>
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
            <select className={selectClass} value={source} onChange={(e) => { setSource(e.target.value); }}>
              {rngSources.map((s) => (<option key={s.value} value={s.value}>{s.label}</option>))}
            </select>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label className="text-xs">Max Bytes</Label>
              <Input type="number" min={0} value={maxBytes} onChange={(e) => { setMaxBytes(e.target.value); }} />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Period (ms)</Label>
              <Input type="number" min={0} value={period} onChange={(e) => { setPeriod(e.target.value); }} />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
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
            <Input value={dirid} onChange={(e) => { setDirid(e.target.value); }} placeholder="share-name" />
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Cache Mode</Label>
            <select className={selectClass} value={cache} onChange={(e) => { setCache(e.target.value); }}>
              {virtiofsCacheModes.map((c) => (<option key={c.value} value={c.value}>{c.label}</option>))}
            </select>
          </div>
          <div className="flex items-center gap-1.5">
            <Checkbox id="add-vfs-dio" checked={directIo} onCheckedChange={(v) => { setDirectIo(v === true); }} />
            <Label htmlFor="add-vfs-dio" className="cursor-pointer text-xs">Direct I/O</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!canAdd}>Add</Button>
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
    onAdd("efidisk0", buildEFIDisk({ volume: `${storage}:1`, storage, efitype, preEnrolledKeys }));
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
            <select className={selectClass} value={storage} onChange={(e) => { setStorage(e.target.value); }}>
              <option value="">Select...</option>
              {diskStorages.map((s) => (<option key={s.storage} value={s.storage}>{s.storage} ({s.type})</option>))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">EFI Type</Label>
            <select className={selectClass} value={efitype} onChange={(e) => { setEfitype(e.target.value); }}>
              {efiTypes.map((t) => (<option key={t.value} value={t.value}>{t.label}</option>))}
            </select>
          </div>
          <div className="flex items-center gap-1.5">
            <Checkbox id="add-efi-keys" checked={preEnrolledKeys} onCheckedChange={(v) => { setPreEnrolledKeys(v === true); }} />
            <Label htmlFor="add-efi-keys" className="cursor-pointer text-xs">Pre-enrolled Keys (Secure Boot)</Label>
          </div>
          <p className="text-xs text-muted-foreground">
            BIOS will be set to OVMF (UEFI) when an EFI disk is added.
          </p>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!storage}>Add</Button>
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
    onAdd("tpmstate0", buildTPMState({ volume: `${storage}:1`, storage, version }));
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
            <select className={selectClass} value={storage} onChange={(e) => { setStorage(e.target.value); }}>
              <option value="">Select...</option>
              {diskStorages.map((s) => (<option key={s.storage} value={s.storage}>{s.storage} ({s.type})</option>))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Version</Label>
            <select className={selectClass} value={version} onChange={(e) => { setVersion(e.target.value); }}>
              {tpmVersions.map((v) => (<option key={v.value} value={v.value}>{v.label}</option>))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!storage}>Add</Button>
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
  const availableSlot = cloudInitBuses.find((b) => config[b.value] == null)?.value ?? "ide2";
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
            <select className={selectClass} value={storage} onChange={(e) => { setStorage(e.target.value); }}>
              <option value="">Select...</option>
              {diskStorages.map((s) => (<option key={s.storage} value={s.storage}>{s.storage} ({s.type})</option>))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">Disk Slot</Label>
            <select className={selectClass} value={slot} onChange={(e) => { setSlot(e.target.value); }}>
              {cloudInitBuses.map((b) => (
                <option key={b.value} value={b.value} disabled={config[b.value] != null}>
                  {b.label}{config[b.value] != null ? " (in use)" : ""}
                </option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!storage}>Add</Button>
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
  const availableSlot = cdromBusSlots.find((b) => config[b.value] == null)?.value ?? "ide2";
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
            <select className={selectClass} value={slot} onChange={(e) => { setSlot(e.target.value); }}>
              {cdromBusSlots.map((b) => (
                <option key={b.value} value={b.value} disabled={config[b.value] != null}>
                  {b.label}{config[b.value] != null ? " (in use)" : ""}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">ISO Image</Label>
            <select className={selectClass} value={iso} onChange={(e) => { setIso(e.target.value); }}>
              <option value="none">No media (empty drive)</option>
              {isoFiles.map((f) => (
                <option key={f.volid} value={f.volid}>{f.volid}</option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleAdd} disabled={!slotFree}>Add</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
