import { useEffect, useState, useMemo } from "react";
import {
  Loader2,
  Save,
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Plus,
  Trash2,
  Network,
  HardDrive,
  ArrowRightLeft,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  useContainerConfig,
  useSetResourceConfig,
  useResizeContainerDisk,
  useMoveContainerVolume,
} from "../api/vm-queries";
import { useNodeBridges } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import { useTaskLogStore } from "@/stores/task-log-store";
import { parseKVString, buildKVString } from "../lib/vm-config-parsers";
import type { VMConfig } from "../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

interface ContainerResourcesPanelProps {
  clusterId: string;
  ctId: string;
  ctStatus: string;
  nodeName: string;
}

function str(val: unknown): string {
  if (val == null) return "";
  if (typeof val === "string") return val;
  if (typeof val === "number" || typeof val === "boolean") return String(val);
  return JSON.stringify(val);
}

function num(val: unknown): number {
  if (val == null) return 0;
  const n = Number(val);
  return Number.isNaN(n) ? 0 : n;
}

// ---------------------------------------------------------------------------
// LXC network parser/builder
// ---------------------------------------------------------------------------

interface CTNetEdit {
  name: string;
  bridge: string;
  hwaddr: string;
  ip: string;
  gw: string;
  ip6: string;
  gw6: string;
  firewall: boolean;
  rate: string;
  mtu: string;
  tag: string;
}

function parseCTNet(raw: string): CTNetEdit {
  const result: CTNetEdit = {
    name: "",
    bridge: "",
    hwaddr: "",
    ip: "",
    gw: "",
    ip6: "",
    gw6: "",
    firewall: false,
    rate: "",
    mtu: "",
    tag: "",
  };
  if (!raw) return result;
  const kv = parseKVString(raw);
  result.name = kv.get("name") ?? "";
  result.bridge = kv.get("bridge") ?? "";
  result.hwaddr = kv.get("hwaddr") ?? "";
  result.ip = kv.get("ip") ?? "";
  result.gw = kv.get("gw") ?? "";
  result.ip6 = kv.get("ip6") ?? "";
  result.gw6 = kv.get("gw6") ?? "";
  result.firewall = kv.get("firewall") === "1";
  result.rate = kv.get("rate") ?? "";
  result.mtu = kv.get("mtu") ?? "";
  result.tag = kv.get("tag") ?? "";
  return result;
}

function buildCTNet(n: CTNetEdit): string {
  const m = new Map<string, string>();
  if (n.name) m.set("name", n.name);
  if (n.bridge) m.set("bridge", n.bridge);
  if (n.hwaddr) m.set("hwaddr", n.hwaddr);
  if (n.ip) m.set("ip", n.ip);
  if (n.gw) m.set("gw", n.gw);
  if (n.ip6) m.set("ip6", n.ip6);
  if (n.gw6) m.set("gw6", n.gw6);
  if (n.firewall) m.set("firewall", "1");
  if (n.rate) m.set("rate", n.rate);
  if (n.mtu) m.set("mtu", n.mtu);
  if (n.tag) m.set("tag", n.tag);
  return buildKVString(m);
}

// ---------------------------------------------------------------------------
// LXC mount point parser
// ---------------------------------------------------------------------------

interface CTMountPoint {
  volume: string;
  path: string;
  size: string;
  acl: boolean;
  backup: boolean;
  quota: boolean;
  ro: boolean;
  replicate: boolean;
}

function parseMountPoint(raw: string): CTMountPoint {
  const result: CTMountPoint = {
    volume: "",
    path: "",
    size: "",
    acl: false,
    backup: true,
    quota: false,
    ro: false,
    replicate: true,
  };
  if (!raw) return result;
  const kv = parseKVString(raw);
  // First bare value is the volume
  const parts = raw.split(",");
  if (parts.length > 0) {
    const first = parts[0] ?? "";
    if (!first.includes("=")) {
      result.volume = first;
    } else {
      // volume=xxx format
      const volMatch = raw.match(/^([^,]+)/);
      if (volMatch) result.volume = volMatch[1] ?? "";
    }
  }
  result.path = kv.get("mp") ?? "";
  result.size = kv.get("size") ?? "";
  result.acl = kv.get("acl") === "1";
  result.backup = kv.get("backup") !== "0";
  result.quota = kv.get("quota") === "1";
  result.ro = kv.get("ro") === "1";
  result.replicate = kv.get("replicate") !== "0";
  return result;
}

// ---------------------------------------------------------------------------
// LXC features parser/builder
// ---------------------------------------------------------------------------

interface CTFeatures {
  nesting: boolean;
  fuse: boolean;
  keyctl: boolean;
  mknod: boolean;
  mount: string;
}

function parseFeatures(raw: string): CTFeatures {
  const result: CTFeatures = {
    nesting: false,
    fuse: false,
    keyctl: false,
    mknod: false,
    mount: "",
  };
  if (!raw) return result;
  const kv = parseKVString(raw);
  result.nesting = kv.get("nesting") === "1";
  result.fuse = kv.get("fuse") === "1";
  result.keyctl = kv.get("keyctl") === "1";
  result.mknod = kv.get("mknod") === "1";
  result.mount = kv.get("mount") ?? "";
  return result;
}

function buildFeatures(f: CTFeatures): string {
  const m = new Map<string, string>();
  if (f.nesting) m.set("nesting", "1");
  if (f.fuse) m.set("fuse", "1");
  if (f.keyctl) m.set("keyctl", "1");
  if (f.mknod) m.set("mknod", "1");
  if (f.mount) m.set("mount", f.mount);
  return buildKVString(m);
}

// ---------------------------------------------------------------------------
// Startup order parser/builder
// ---------------------------------------------------------------------------

interface StartupOrder {
  order: string;
  up: string;
  down: string;
}

function parseStartupOrder(raw: string): StartupOrder {
  const result: StartupOrder = { order: "", up: "", down: "" };
  if (!raw) return result;
  const kv = parseKVString(raw);
  result.order = kv.get("order") ?? "";
  result.up = kv.get("up") ?? "";
  result.down = kv.get("down") ?? "";
  return result;
}

function buildStartupOrder(s: StartupOrder): string {
  const m = new Map<string, string>();
  if (s.order) m.set("order", s.order);
  if (s.up) m.set("up", s.up);
  if (s.down) m.set("down", s.down);
  return buildKVString(m);
}

// ---------------------------------------------------------------------------
// Section component
// ---------------------------------------------------------------------------

interface SectionProps {
  title: string;
  children: React.ReactNode;
  defaultOpen?: boolean;
}

function Section({ title, children, defaultOpen = true }: SectionProps) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Card className="overflow-hidden">
      <CardHeader
        className="cursor-pointer select-none px-3 py-2"
        onClick={() => {
          setOpen(!open);
        }}
      >
        <CardTitle className="flex items-center gap-2 text-xs font-medium">
          {open ? (
            <ChevronDown className="h-3.5 w-3.5" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" />
          )}
          {title}
        </CardTitle>
      </CardHeader>
      {open && (
        <CardContent className="px-3 pb-3 pt-0">{children}</CardContent>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Move Volume Dialog
// ---------------------------------------------------------------------------

function MoveVolumeDialog({
  clusterId,
  ctId,
  volumeKey,
  currentStorage,
  storageOptions,
}: {
  clusterId: string;
  ctId: string;
  volumeKey: string;
  currentStorage: string;
  storageOptions: string[];
}) {
  const [open, setOpen] = useState(false);
  const [targetStorage, setTargetStorage] = useState("");
  const [deleteOriginal, setDeleteOriginal] = useState(true);
  const moveMutation = useMoveContainerVolume();
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const filteredOptions = storageOptions.filter((s) => s !== currentStorage);

  function handleMove() {
    if (!targetStorage) return;
    moveMutation.mutate(
      {
        clusterId,
        ctId,
        volume: volumeKey,
        storage: targetStorage,
        deleteOriginal,
      },
      {
        onSuccess: (data) => {
          if (data.upid) {
            setFocusedTask({
              clusterId,
              upid: data.upid,
              description: `Move ${volumeKey} → ${targetStorage}`,
            });
          }
          setOpen(false);
          setTargetStorage("");
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button
        size="sm"
        variant="outline"
        className="h-8 gap-1"
        onClick={() => {
          setOpen(true);
        }}
      >
        <ArrowRightLeft className="h-3 w-3" />
        Move Storage
      </Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move Volume: {volumeKey}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {currentStorage && (
            <p className="text-sm text-muted-foreground">
              Current storage: <span className="font-mono font-medium">{currentStorage}</span>
            </p>
          )}
          <div className="space-y-2">
            <Label htmlFor="ct-target-storage">Target Storage</Label>
            <select
              id="ct-target-storage"
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={targetStorage}
              onChange={(e) => {
                setTargetStorage(e.target.value);
              }}
            >
              <option value="">Select storage...</option>
              {filteredOptions.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="ct-delete-original"
              checked={deleteOriginal}
              onCheckedChange={(v) => {
                setDeleteOriginal(v === true);
              }}
            />
            <Label htmlFor="ct-delete-original">
              Delete original after move completes
            </Label>
          </div>
          <Button
            onClick={handleMove}
            disabled={!targetStorage || moveMutation.isPending}
            className="w-full"
          >
            {moveMutation.isPending ? "Moving..." : "Move Volume"}
          </Button>
          {moveMutation.isError && (
            <p className="text-sm text-destructive">
              {moveMutation.error.message}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Root FS info row
// ---------------------------------------------------------------------------

function RootFSRow({
  config,
  clusterId,
  ctId,
  storageOptions,
}: {
  config: VMConfig;
  clusterId: string;
  ctId: string;
  storageOptions: string[];
}) {
  const raw = str(config["rootfs"]);
  const kv = parseKVString(raw);
  const volume = raw.split(",")[0] ?? "";
  const currentStorage = volume.split(":")[0] ?? "";
  const currentSize = kv.get("size") ?? "";

  const [resizeSize, setResizeSize] = useState("");
  const resizeMutation = useResizeContainerDisk();

  return (
    <div className="space-y-2">
      <div className="grid grid-cols-3 gap-2 text-sm">
        <div>
          <span className="text-muted-foreground">Volume:</span>{" "}
          <span className="font-mono">{volume || "--"}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Size:</span>{" "}
          <span className="font-medium">{currentSize || "--"}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Storage:</span>{" "}
          <span className="font-medium">{currentStorage || "--"}</span>
        </div>
      </div>
      <div className="flex items-end gap-2">
        <div className="flex-1 max-w-[200px]">
          <Label className="text-xs">Resize To</Label>
          <Input
            placeholder="e.g. 32G"
            value={resizeSize}
            onChange={(e) => {
              setResizeSize(e.target.value);
            }}
            className="h-8 text-sm"
          />
        </div>
        <Button
          size="sm"
          variant="outline"
          className="h-8 gap-1"
          disabled={!resizeSize.trim() || resizeMutation.isPending}
          onClick={() => {
            resizeMutation.mutate(
              {
                clusterId,
                ctId,
                disk: "rootfs",
                size: resizeSize.trim(),
              },
              {
                onSuccess: () => {
                  setResizeSize("");
                },
              },
            );
          }}
        >
          {resizeMutation.isPending ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <HardDrive className="h-3 w-3" />
          )}
          Resize
        </Button>
        <MoveVolumeDialog
          clusterId={clusterId}
          ctId={ctId}
          volumeKey="rootfs"
          currentStorage={currentStorage}
          storageOptions={storageOptions}
        />
        {resizeMutation.isError && (
          <span className="text-xs text-destructive">
            {resizeMutation.error.message}
          </span>
        )}
        {resizeMutation.isSuccess && (
          <span className="text-xs text-green-600">Resized</span>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Mount point row (read-only for now, resize support)
// ---------------------------------------------------------------------------

function MountPointRow({
  deviceKey,
  mp,
  clusterId,
  ctId,
  storageOptions,
}: {
  deviceKey: string;
  mp: CTMountPoint;
  clusterId: string;
  ctId: string;
  storageOptions: string[];
}) {
  const [resizeSize, setResizeSize] = useState("");
  const resizeMutation = useResizeContainerDisk();
  const currentStorage = mp.volume.split(":")[0] ?? "";

  return (
    <div className="space-y-1 rounded border p-2">
      <div className="flex items-center gap-2">
        <Badge variant="outline" className="font-mono text-xs">
          {deviceKey}
        </Badge>
        <span className="text-xs text-muted-foreground">{mp.path}</span>
      </div>
      <div className="grid grid-cols-3 gap-2 text-xs">
        <div>
          <span className="text-muted-foreground">Volume:</span>{" "}
          <span className="font-mono">{mp.volume}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Size:</span>{" "}
          {mp.size || "--"}
        </div>
        <div className="flex gap-2">
          {mp.backup && <Badge variant="secondary" className="text-[10px]">backup</Badge>}
          {mp.ro && <Badge variant="secondary" className="text-[10px]">ro</Badge>}
          {mp.quota && <Badge variant="secondary" className="text-[10px]">quota</Badge>}
        </div>
      </div>
      <div className="flex items-end gap-2 pt-1">
        {mp.size && (
          <>
            <Input
              placeholder="e.g. 16G"
              value={resizeSize}
              onChange={(e) => {
                setResizeSize(e.target.value);
              }}
              className="h-7 max-w-[150px] text-xs"
            />
            <Button
              size="sm"
              variant="outline"
              className="h-7 gap-1 text-xs"
              disabled={!resizeSize.trim() || resizeMutation.isPending}
              onClick={() => {
                resizeMutation.mutate(
                  { clusterId, ctId, disk: deviceKey, size: resizeSize.trim() },
                  { onSuccess: () => { setResizeSize(""); } },
                );
              }}
            >
              {resizeMutation.isPending ? (
                <Loader2 className="h-3 w-3 animate-spin" />
              ) : (
                "Resize"
              )}
            </Button>
          </>
        )}
        <MoveVolumeDialog
          clusterId={clusterId}
          ctId={ctId}
          volumeKey={deviceKey}
          currentStorage={currentStorage}
          storageOptions={storageOptions}
        />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// NIC editor row
// ---------------------------------------------------------------------------

function NICRow({
  deviceKey,
  nic,
  onChange,
  bridges,
}: {
  deviceKey: string;
  nic: CTNetEdit;
  onChange: (updated: CTNetEdit) => void;
  bridges: string[];
}) {
  return (
    <div className="space-y-2 rounded border p-2">
      <div className="flex items-center gap-2">
        <Badge variant="outline" className="font-mono text-xs">
          <Network className="mr-1 h-3 w-3" />
          {deviceKey}
        </Badge>
        {nic.hwaddr && (
          <span className="font-mono text-xs text-muted-foreground">
            {nic.hwaddr}
          </span>
        )}
      </div>
      <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
        <div>
          <Label className="text-xs">Name</Label>
          <Input
            value={nic.name}
            onChange={(e) => {
              onChange({ ...nic, name: e.target.value });
            }}
            placeholder="eth0"
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">Bridge</Label>
          <select
            className={selectClass + " h-8 text-sm"}
            value={nic.bridge}
            onChange={(e) => {
              onChange({ ...nic, bridge: e.target.value });
            }}
          >
            <option value="">—</option>
            {bridges.map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
            {nic.bridge && !bridges.includes(nic.bridge) && (
              <option value={nic.bridge}>{nic.bridge}</option>
            )}
          </select>
        </div>
        <div>
          <Label className="text-xs">IPv4 (CIDR or DHCP)</Label>
          <Input
            value={nic.ip}
            onChange={(e) => {
              onChange({ ...nic, ip: e.target.value });
            }}
            placeholder="dhcp or 10.0.0.2/24"
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">Gateway</Label>
          <Input
            value={nic.gw}
            onChange={(e) => {
              onChange({ ...nic, gw: e.target.value });
            }}
            placeholder="10.0.0.1"
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">IPv6 (CIDR or DHCP)</Label>
          <Input
            value={nic.ip6}
            onChange={(e) => {
              onChange({ ...nic, ip6: e.target.value });
            }}
            placeholder="auto, dhcp, or fd00::2/64"
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">Gateway6</Label>
          <Input
            value={nic.gw6}
            onChange={(e) => {
              onChange({ ...nic, gw6: e.target.value });
            }}
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">VLAN Tag</Label>
          <Input
            type="number"
            min={1}
            max={4094}
            value={nic.tag}
            onChange={(e) => {
              onChange({ ...nic, tag: e.target.value });
            }}
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">Rate Limit (MB/s)</Label>
          <Input
            type="number"
            min={0}
            step={0.1}
            value={nic.rate}
            onChange={(e) => {
              onChange({ ...nic, rate: e.target.value });
            }}
            className="h-8 text-sm"
          />
        </div>
        <div>
          <Label className="text-xs">MTU</Label>
          <Input
            type="number"
            min={68}
            max={65535}
            value={nic.mtu}
            onChange={(e) => {
              onChange({ ...nic, mtu: e.target.value });
            }}
            className="h-8 text-sm"
          />
        </div>
        <div className="flex items-end pb-1">
          <div className="flex items-center gap-2">
            <Checkbox
              checked={nic.firewall}
              onCheckedChange={(v) => {
                onChange({ ...nic, firewall: v === true });
              }}
            />
            <Label className="text-xs">Firewall</Label>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main panel
// ---------------------------------------------------------------------------

const NET_KEY_RE = /^net(\d+)$/;
const MP_KEY_RE = /^mp(\d+)$/;

const consoleModes = [
  { value: "tty", label: "TTY" },
  { value: "console", label: "Console" },
  { value: "shell", label: "Shell" },
] as const;

export function ContainerResourcesPanel({
  clusterId,
  ctId,
  ctStatus,
  nodeName,
}: ContainerResourcesPanelProps) {
  const { data: config, isLoading, error } = useContainerConfig(clusterId, ctId);
  const setConfigMutation = useSetResourceConfig();
  const { data: bridges } = useNodeBridges(clusterId, nodeName);
  const { data: storageList } = useClusterStorage(clusterId);
  const storageOptions = useMemo(() => {
    if (!storageList) return [];
    const seen = new Set<string>();
    return storageList
      .filter((s) => {
        if (!s.enabled || !s.active) return false;
        if (!s.content.includes("rootdir") && !s.content.includes("images")) return false;
        if (seen.has(s.storage)) return false;
        seen.add(s.storage);
        return true;
      })
      .map((s) => s.storage)
      .sort();
  }, [storageList]);
  const bridgeList = useMemo(
    () => (bridges ?? []).map((b) => b.iface).sort(),
    [bridges],
  );

  // --- Local form state ---
  const [cores, setCores] = useState("1");
  const [cpulimit, setCpulimit] = useState("");
  const [cpuunits, setCpuunits] = useState("1024");
  const [memory, setMemory] = useState("512");
  const [swap, setSwap] = useState("512");
  const [hostname, setHostname] = useState("");
  const [nameserver, setNameserver] = useState("");
  const [searchdomain, setSearchdomain] = useState("");
  const [onboot, setOnboot] = useState(false);
  const [protection, setProtection] = useState(false);
  const [cmode, setCmode] = useState("tty");
  const [startup, setStartup] = useState<StartupOrder>({ order: "", up: "", down: "" });
  const [features, setFeatures] = useState<CTFeatures>({
    nesting: false, fuse: false, keyctl: false, mknod: false, mount: "",
  });
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState("");

  // Multi-NIC state
  const [nics, setNics] = useState<Map<string, CTNetEdit>>(new Map());

  // Pending new NICs to add
  const [pendingNics, setPendingNics] = useState<Map<string, CTNetEdit>>(new Map());

  // NICs to delete
  const [deleteNics, setDeleteNics] = useState<Set<string>>(new Set());

  // Track original values for change detection
  const [origFields, setOrigFields] = useState<Record<string, string>>({});

  // Populate form from fetched config
  useEffect(() => {
    if (!config) return;

    setCores(str(config["cores"]) || "1");
    setCpulimit(str(config["cpulimit"]));
    setCpuunits(str(config["cpuunits"]) || "1024");
    setMemory(str(config["memory"]) || "512");
    setSwap(str(config["swap"]) || "512");
    setHostname(str(config["hostname"]));
    setNameserver(str(config["nameserver"]));
    setSearchdomain(str(config["searchdomain"]));
    setOnboot(num(config["onboot"]) === 1);
    setProtection(num(config["protection"]) === 1);
    setCmode(str(config["cmode"]) || "tty");
    setStartup(parseStartupOrder(str(config["startup"])));
    setFeatures(parseFeatures(str(config["features"])));
    setDescription(str(config["description"]));
    setTags(str(config["tags"]));

    // Parse NICs
    const nicMap = new Map<string, CTNetEdit>();
    for (const key of Object.keys(config)) {
      if (NET_KEY_RE.test(key)) {
        nicMap.set(key, parseCTNet(str(config[key])));
      }
    }
    setNics(nicMap);
    setPendingNics(new Map());
    setDeleteNics(new Set());

    // Build original field snapshot
    const orig: Record<string, string> = {};
    orig["cores"] = str(config["cores"]) || "1";
    orig["cpulimit"] = str(config["cpulimit"]);
    orig["cpuunits"] = str(config["cpuunits"]) || "1024";
    orig["memory"] = str(config["memory"]) || "512";
    orig["swap"] = str(config["swap"]) || "512";
    orig["hostname"] = str(config["hostname"]);
    orig["nameserver"] = str(config["nameserver"]);
    orig["searchdomain"] = str(config["searchdomain"]);
    orig["onboot"] = num(config["onboot"]) === 1 ? "1" : "0";
    orig["protection"] = num(config["protection"]) === 1 ? "1" : "0";
    orig["cmode"] = str(config["cmode"]) || "tty";
    orig["startup"] = str(config["startup"]);
    orig["features"] = str(config["features"]);
    orig["description"] = str(config["description"]);
    orig["tags"] = str(config["tags"]);
    for (const key of Object.keys(config)) {
      if (NET_KEY_RE.test(key)) {
        orig[key] = str(config[key]);
      }
    }
    setOrigFields(orig);
  }, [config]);

  // Build current fields for diff
  const currentFields = useMemo(() => {
    const f: Record<string, string> = {};
    f["cores"] = cores;
    f["cpulimit"] = cpulimit;
    f["cpuunits"] = cpuunits;
    f["memory"] = memory;
    f["swap"] = swap;
    f["hostname"] = hostname;
    f["nameserver"] = nameserver;
    f["searchdomain"] = searchdomain;
    f["onboot"] = onboot ? "1" : "0";
    f["protection"] = protection ? "1" : "0";
    f["cmode"] = cmode;
    f["startup"] = buildStartupOrder(startup);
    f["features"] = buildFeatures(features);
    f["description"] = description;
    f["tags"] = tags;
    for (const [key, nic] of nics) {
      if (!deleteNics.has(key)) {
        f[key] = buildCTNet(nic);
      }
    }
    for (const [key, nic] of pendingNics) {
      f[key] = buildCTNet(nic);
    }
    return f;
  }, [
    cores, cpulimit, cpuunits, memory, swap, hostname, nameserver,
    searchdomain, onboot, protection, cmode, startup, features,
    description, tags, nics, pendingNics, deleteNics,
  ]);

  // Detect changes
  const changedFields = useMemo(() => {
    const diff: Record<string, string> = {};
    for (const [key, val] of Object.entries(currentFields)) {
      if (val !== (origFields[key] ?? "")) {
        diff[key] = val;
      }
    }
    // Handle deletions
    const deletes: string[] = [];
    for (const key of deleteNics) {
      deletes.push(key);
    }
    if (deletes.length > 0) {
      diff["delete"] = deletes.join(",");
    }
    return diff;
  }, [currentFields, origFields, deleteNics]);

  const hasChanges = Object.keys(changedFields).length > 0;
  const isRunning = ctStatus.toLowerCase() === "running";

  // Mount points (read-only display + resize)
  const mountPoints = useMemo(() => {
    if (!config) return [];
    const mps: { key: string; mp: CTMountPoint }[] = [];
    for (const key of Object.keys(config)) {
      if (MP_KEY_RE.test(key)) {
        mps.push({ key, mp: parseMountPoint(str(config[key])) });
      }
    }
    return mps.sort((a, b) => a.key.localeCompare(b.key));
  }, [config]);

  // Find next available NIC index
  function nextNicKey(): string {
    const existing = new Set<number>();
    for (const key of nics.keys()) {
      const m = NET_KEY_RE.exec(key);
      if (m?.[1]) existing.add(parseInt(m[1], 10));
    }
    for (const key of pendingNics.keys()) {
      const m = NET_KEY_RE.exec(key);
      if (m?.[1]) existing.add(parseInt(m[1], 10));
    }
    for (let i = 0; i < 32; i++) {
      if (!existing.has(i)) return `net${String(i)}`;
    }
    return `net${String(existing.size)}`;
  }

  function handleSave() {
    if (!hasChanges) return;
    setConfigMutation.mutate({
      clusterId,
      resourceId: ctId,
      kind: "ct",
      fields: changedFields,
    });
  }

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 p-6 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading configuration...
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-6 text-destructive">
        Failed to load container config: {error.message}
      </div>
    );
  }

  if (!config) return null;

  return (
    <div className="space-y-3">
      {/* Warning banner for running container */}
      {isRunning && (
        <div className="flex items-center gap-2 rounded-md border border-yellow-500/30 bg-yellow-500/10 p-2 text-sm text-yellow-700 dark:text-yellow-400">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          Some changes (CPU, memory) take effect immediately on a running
          container. Others may require a restart.
        </div>
      )}

      {/* Save bar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {hasChanges && (
            <Badge variant="secondary" className="text-xs">
              {Object.keys(changedFields).length} change
              {Object.keys(changedFields).length !== 1 ? "s" : ""}
            </Badge>
          )}
          {setConfigMutation.isSuccess && (
            <span className="text-xs text-green-600">Saved</span>
          )}
          {setConfigMutation.isError && (
            <span className="text-xs text-destructive">
              {setConfigMutation.error.message}
            </span>
          )}
        </div>
        <Button
          size="sm"
          className="gap-1.5"
          disabled={!hasChanges || setConfigMutation.isPending}
          onClick={handleSave}
        >
          {setConfigMutation.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Save className="h-3.5 w-3.5" />
          )}
          Save Changes
        </Button>
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        {/* CPU */}
        <Section title="CPU">
          <div className="grid grid-cols-3 gap-3">
            <div>
              <Label className="text-xs">Cores</Label>
              <Input
                type="number"
                min={1}
                max={512}
                value={cores}
                onChange={(e) => {
                  setCores(e.target.value);
                }}
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">CPU Limit</Label>
              <Input
                type="number"
                min={0}
                max={128}
                step={0.1}
                value={cpulimit}
                onChange={(e) => {
                  setCpulimit(e.target.value);
                }}
                placeholder="0 = unlimited"
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">CPU Units</Label>
              <Input
                type="number"
                min={2}
                max={500000}
                value={cpuunits}
                onChange={(e) => {
                  setCpuunits(e.target.value);
                }}
                className="h-8 text-sm"
              />
            </div>
          </div>
        </Section>

        {/* Memory */}
        <Section title="Memory">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="text-xs">Memory (MB)</Label>
              <Input
                type="number"
                min={16}
                step={16}
                value={memory}
                onChange={(e) => {
                  setMemory(e.target.value);
                }}
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">Swap (MB)</Label>
              <Input
                type="number"
                min={0}
                step={16}
                value={swap}
                onChange={(e) => {
                  setSwap(e.target.value);
                }}
                className="h-8 text-sm"
              />
            </div>
          </div>
        </Section>
      </div>

      {/* Root Filesystem */}
      <Section title="Root Filesystem">
        <RootFSRow config={config} clusterId={clusterId} ctId={ctId} storageOptions={storageOptions} />
      </Section>

      {/* Mount Points */}
      {mountPoints.length > 0 && (
        <Section title="Mount Points" defaultOpen={false}>
          <div className="space-y-2">
            {mountPoints.map(({ key, mp }) => (
              <MountPointRow
                key={key}
                deviceKey={key}
                mp={mp}
                clusterId={clusterId}
                ctId={ctId}
                storageOptions={storageOptions}
              />
            ))}
          </div>
        </Section>
      )}

      {/* Network */}
      <Section title="Network Interfaces">
        <div className="space-y-2">
          {Array.from(nics.entries())
            .filter(([key]) => !deleteNics.has(key))
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([key, nic]) => (
              <div key={key} className="relative">
                <NICRow
                  deviceKey={key}
                  nic={nic}
                  onChange={(updated) => {
                    setNics((prev) => {
                      const next = new Map(prev);
                      next.set(key, updated);
                      return next;
                    });
                  }}
                  bridges={bridgeList}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  className="absolute right-1 top-1 h-6 w-6 p-0 text-destructive"
                  onClick={() => {
                    setDeleteNics((prev) => new Set(prev).add(key));
                  }}
                  title="Remove NIC"
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </div>
            ))}

          {/* Pending new NICs */}
          {Array.from(pendingNics.entries())
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([key, nic]) => (
              <div key={key} className="relative">
                <div className="absolute -left-1 -top-1 z-10">
                  <Badge className="bg-green-600 text-[10px]">new</Badge>
                </div>
                <NICRow
                  deviceKey={key}
                  nic={nic}
                  onChange={(updated) => {
                    setPendingNics((prev) => {
                      const next = new Map(prev);
                      next.set(key, updated);
                      return next;
                    });
                  }}
                  bridges={bridgeList}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  className="absolute right-1 top-1 h-6 w-6 p-0 text-destructive"
                  onClick={() => {
                    setPendingNics((prev) => {
                      const next = new Map(prev);
                      next.delete(key);
                      return next;
                    });
                  }}
                  title="Remove"
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </div>
            ))}

          {/* Deleted NIC indicators */}
          {Array.from(deleteNics).map((key) => (
            <div
              key={`del-${key}`}
              className="flex items-center gap-2 rounded border border-destructive/30 bg-destructive/5 p-2 text-sm"
            >
              <Badge variant="destructive" className="text-[10px]">
                removing
              </Badge>
              <span className="font-mono">{key}</span>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => {
                  setDeleteNics((prev) => {
                    const next = new Set(prev);
                    next.delete(key);
                    return next;
                  });
                }}
              >
                Undo
              </Button>
            </div>
          ))}

          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={() => {
              const key = nextNicKey();
              setPendingNics((prev) => {
                const next = new Map(prev);
                next.set(key, {
                  name: `eth${key.replace("net", "")}`,
                  bridge: bridgeList[0] ?? "vmbr0",
                  hwaddr: "",
                  ip: "dhcp",
                  gw: "",
                  ip6: "",
                  gw6: "",
                  firewall: true,
                  rate: "",
                  mtu: "",
                  tag: "",
                });
                return next;
              });
            }}
          >
            <Plus className="h-3 w-3" /> Add Network Interface
          </Button>
        </div>
      </Section>

      <div className="grid gap-3 lg:grid-cols-2">
        {/* DNS */}
        <Section title="DNS">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="text-xs">Nameserver</Label>
              <Input
                value={nameserver}
                onChange={(e) => {
                  setNameserver(e.target.value);
                }}
                placeholder="Host default"
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">Search Domain</Label>
              <Input
                value={searchdomain}
                onChange={(e) => {
                  setSearchdomain(e.target.value);
                }}
                placeholder="Host default"
                className="h-8 text-sm"
              />
            </div>
          </div>
        </Section>

        {/* Features */}
        <Section title="Features">
          <div className="space-y-2">
            <div className="grid grid-cols-2 gap-3">
              <div className="flex items-center gap-2">
                <Checkbox
                  checked={features.nesting}
                  onCheckedChange={(v) => {
                    setFeatures((f) => ({ ...f, nesting: v === true }));
                  }}
                />
                <Label className="text-xs">Nesting</Label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  checked={features.fuse}
                  onCheckedChange={(v) => {
                    setFeatures((f) => ({ ...f, fuse: v === true }));
                  }}
                />
                <Label className="text-xs">FUSE</Label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  checked={features.keyctl}
                  onCheckedChange={(v) => {
                    setFeatures((f) => ({ ...f, keyctl: v === true }));
                  }}
                />
                <Label className="text-xs">Keyctl</Label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  checked={features.mknod}
                  onCheckedChange={(v) => {
                    setFeatures((f) => ({ ...f, mknod: v === true }));
                  }}
                />
                <Label className="text-xs">Mknod</Label>
              </div>
            </div>
            <div>
              <Label className="text-xs">Allowed Mount Types</Label>
              <Input
                value={features.mount}
                onChange={(e) => {
                  setFeatures((f) => ({ ...f, mount: e.target.value }));
                }}
                placeholder="e.g. cifs;nfs"
                className="h-8 text-sm"
              />
            </div>
          </div>
        </Section>
      </div>

      {/* Options */}
      <Section title="Options">
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <div>
              <Label className="text-xs">Hostname</Label>
              <Input
                value={hostname}
                onChange={(e) => {
                  setHostname(e.target.value);
                }}
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">Console Mode</Label>
              <select
                className={selectClass + " h-8 text-sm"}
                value={cmode}
                onChange={(e) => {
                  setCmode(e.target.value);
                }}
              >
                {consoleModes.map((m) => (
                  <option key={m.value} value={m.value}>
                    {m.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="flex flex-wrap gap-x-6 gap-y-2">
            <div className="flex items-center gap-2">
              <Checkbox
                checked={onboot}
                onCheckedChange={(v) => {
                  setOnboot(v === true);
                }}
              />
              <Label className="text-xs">Start at Boot</Label>
            </div>
            <div className="flex items-center gap-2">
              <Checkbox
                checked={protection}
                onCheckedChange={(v) => {
                  setProtection(v === true);
                }}
              />
              <Label className="text-xs">Protection</Label>
            </div>
          </div>

          {/* Startup Order */}
          <div className="grid grid-cols-3 gap-3">
            <div>
              <Label className="text-xs">Startup Order</Label>
              <Input
                type="number"
                min={0}
                value={startup.order}
                onChange={(e) => {
                  setStartup((s) => ({ ...s, order: e.target.value }));
                }}
                placeholder="—"
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">Up Delay (s)</Label>
              <Input
                type="number"
                min={0}
                value={startup.up}
                onChange={(e) => {
                  setStartup((s) => ({ ...s, up: e.target.value }));
                }}
                className="h-8 text-sm"
              />
            </div>
            <div>
              <Label className="text-xs">Down Delay (s)</Label>
              <Input
                type="number"
                min={0}
                value={startup.down}
                onChange={(e) => {
                  setStartup((s) => ({ ...s, down: e.target.value }));
                }}
                className="h-8 text-sm"
              />
            </div>
          </div>

          {/* Description & Tags */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="text-xs">Description</Label>
              <textarea
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                }}
                rows={3}
                className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>
            <div>
              <Label className="text-xs">Tags</Label>
              <Input
                value={tags}
                onChange={(e) => {
                  setTags(e.target.value);
                }}
                placeholder="tag1;tag2"
                className="h-8 text-sm"
              />
            </div>
          </div>
        </div>
      </Section>
    </div>
  );
}
