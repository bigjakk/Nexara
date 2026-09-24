import { useEffect, useLayoutEffect, useState, useMemo, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
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
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  containerConfigKey,
  useContainerConfig,
  useSetResourceConfig,
  useResizeContainerDisk,
  useMoveContainerVolume,
} from "../api/vm-queries";
import { useNodeBridges } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import { DiskMoveOptions } from "@/features/storage/components/DiskMoveOptions";
import { parseBwlimit } from "@/features/storage/lib/disk-move";
import { useTaskLogStore } from "@/stores/task-log-store";
import { parseKVString, buildKVString } from "../lib/vm-config-parsers";
import { parseCTNet, buildCTNet, emptyCTNet } from "../lib/ct-net";
import type { CTNetEdit } from "../lib/ct-net";
import type { VMConfig } from "../types/vm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

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

// The config once a write of `fields` has succeeded, close enough to stand in
// until a fetch completed after the write brings Proxmox's own copy, which is
// then shown whatever it holds: each key in the delete list gone, every other
// field as sent. (That copy can differ from this one: Proxmox normalises some
// values, even back to what it had, and gives a new NIC its MAC.)
function applyConfigWrite(
  config: VMConfig,
  fields: Record<string, string>,
): VMConfig {
  const deleted = new Set((fields["delete"] ?? "").split(","));
  const next: VMConfig = Object.fromEntries(
    Object.entries(config).filter(([key]) => !deleted.has(key)),
  );
  for (const [key, value] of Object.entries(fields)) {
    if (key !== "delete") next[key] = value;
  }
  return next;
}

// An unused volume a Save would destroy: its config key and the volume that
// key holds.
interface VolumeToDelete {
  key: string;
  volume: string;
}

// Whether two lists name the same volumes under the same keys. Both come
// sorted by key, so equal lists match entry by entry.
function sameVolumes(
  a: readonly VolumeToDelete[],
  b: readonly VolumeToDelete[],
): boolean {
  return (
    a.length === b.length &&
    a.every((entry, i) => {
      const other = b[i];
      return (
        other !== undefined &&
        entry.key === other.key &&
        entry.volume === other.volume
      );
    })
  );
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
      {open && <CardContent className="px-3 pb-3 pt-0">{children}</CardContent>}
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
  // Off by default, matching Proxmox: the source volume is kept as an unused
  // entry on the container so the move can be undone by hand.
  const [deleteOriginal, setDeleteOriginal] = useState(false);
  const [bwlimit, setBwlimit] = useState("");
  const moveMutation = useMoveContainerVolume();
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const filteredOptions = storageOptions.filter((s) => s !== currentStorage);
  const { value: bwlimitKib, invalid: bwlimitInvalid } = parseBwlimit(bwlimit);

  function handleMove() {
    if (!targetStorage || bwlimitInvalid) return;
    moveMutation.mutate(
      {
        clusterId,
        ctId,
        volume: volumeKey,
        storage: targetStorage,
        deleteOriginal,
        bwlimitKib,
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
          setDeleteOriginal(false);
          setBwlimit("");
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
              Current storage:{" "}
              <span className="font-mono font-medium">{currentStorage}</span>
            </p>
          )}
          <div className="space-y-2">
            <Label htmlFor="ct-target-storage">Target Storage</Label>
            <select
              id="ct-target-storage"
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
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
          {/* Containers have no image-format choice, so the format field is
              hidden; everything else matches the VM move dialog. */}
          <DiskMoveOptions
            idPrefix="ct-move"
            hideFormat
            bwlimit={bwlimit}
            onBwlimitChange={setBwlimit}
            deleteSource={deleteOriginal}
            onDeleteSourceChange={setDeleteOriginal}
            keptHint="The source volume is kept as an unused volume on this container. Remove it later to reclaim the space."
          />
          <Button
            onClick={handleMove}
            disabled={
              !targetStorage || bwlimitInvalid || moveMutation.isPending
            }
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
          <span className="text-xs text-emerald-600">Resized</span>
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
          <span className="text-muted-foreground">Size:</span> {mp.size || "--"}
        </div>
        <div className="flex gap-2">
          {mp.backup && (
            <Badge variant="secondary" className="text-[10px]">
              backup
            </Badge>
          )}
          {mp.ro && (
            <Badge variant="secondary" className="text-[10px]">
              ro
            </Badge>
          )}
          {mp.quota && (
            <Badge variant="secondary" className="text-[10px]">
              quota
            </Badge>
          )}
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
// Volumes still owned by the container but not mounted anywhere — what a
// move-volume without "delete source" leaves behind.
const UNUSED_KEY_RE = /^unused(\d+)$/;

const consoleModes = [
  { value: "tty", label: "TTY" },
  { value: "console", label: "Console" },
  { value: "shell", label: "Shell" },
] as const;

// A successful Save's result, laid over the fetched copy it was built on; the
// component says when it stands.
interface AfterSave {
  over: VMConfig;
  config: VMConfig;
  at: number;
}

// The config the panel stands on: an afterSave while it still stands, else
// the fetched copy, which was fetched at `updatedAt`.
function currentConfig(
  fetched: VMConfig | undefined,
  updatedAt: number,
  afterSave: AfterSave | null,
): VMConfig | undefined {
  return afterSave !== null &&
    afterSave.over === fetched &&
    updatedAt <= afterSave.at
    ? afterSave.config
    : fetched;
}

// What a config write of `fields` would destroy: the unusedN keys in its
// delete list, each with the volume it holds in `config`, sorted by key as the
// Unused Volumes section is. Read from the request itself, not from
// deleteVolumes or unusedVolumes, so it cannot differ from what is sent.
function volumesIn(
  fields: Record<string, string>,
  config: VMConfig | undefined,
): VolumeToDelete[] {
  return (fields["delete"] ?? "")
    .split(",")
    .filter((key) => UNUSED_KEY_RE.test(key))
    .sort((a, b) => a.localeCompare(b))
    .map((key) => ({ key, volume: str(config?.[key]) }));
}

// Every other key a config write of `fields` sets or deletes: all but the
// unusedN deletes, which volumesIn has.
function keysBesidesVolumes(fields: Record<string, string>): string[] {
  return [
    ...Object.keys(fields).filter((key) => key !== "delete"),
    ...(fields["delete"] ?? "")
      .split(",")
      .filter((key) => key !== "" && !UNUSED_KEY_RE.test(key)),
  ];
}

export function ContainerResourcesPanel({
  clusterId,
  ctId,
  ctStatus,
  nodeName,
}: ContainerResourcesPanelProps) {
  const {
    data: fetchedConfig,
    dataUpdatedAt,
    isLoading,
    error,
  } = useContainerConfig(clusterId, ctId);

  // After a successful Save: the config as it now stands, laid over the
  // fetched copy that Save was built on until a fetch completes after the
  // write. Without it, until the refetch lands everything sent stays staged
  // behind an enabled Save, and a deleted volume is still listed, and Save
  // would offer to delete it again, when its key may by then name another
  // volume (pve-guest-common's add_unused_volume reuses the lowest free
  // unusedN). Nothing is written to the query cache, so a fetch that lands
  // first is never overwritten.
  //
  // Either of two things drops it. A fetch bringing other data is a new
  // object, so `over` no longer matches. A fetch bringing the same data keeps
  // the object (TanStack's structural sharing), as it does when Proxmox
  // normalises a sent value back to what it stored (pve-guest-common's
  // get_unique_tags sorts and lowercases tags), so its dataUpdatedAt, later
  // than `at`, is the only sign it landed. A fetch that completed before
  // `at`, while the Save was out, may predate the write and leaves it in
  // place. One completing in the same millisecond as `at` counts as before
  // it: holding the overlay too long only shows the values as sent, where
  // dropping it too soon could list a deleted volume again.
  const [afterSave, setAfterSave] = useState<AfterSave | null>(null);
  const config = currentConfig(fetchedConfig, dataUpdatedAt, afterSave);
  const setConfigMutation = useSetResourceConfig();
  const queryClient = useQueryClient();
  // useContainerConfig's key, so saveChanges can read the newest copy at the
  // moment it checks.
  const configKey = containerConfigKey(clusterId, ctId);
  const { data: bridges } = useNodeBridges(clusterId, nodeName);
  const { data: storageList } = useClusterStorage(clusterId);
  const storageOptions = useMemo(() => {
    if (!storageList) return [];
    const seen = new Set<string>();
    return storageList
      .filter((s) => {
        if (!s.enabled || !s.active) return false;
        if (!s.content.includes("rootdir") && !s.content.includes("images"))
          return false;
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
  const [startup, setStartup] = useState<StartupOrder>({
    order: "",
    up: "",
    down: "",
  });
  const [features, setFeatures] = useState<CTFeatures>({
    nesting: false,
    fuse: false,
    keyctl: false,
    mknod: false,
    mount: "",
  });
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState("");

  // Multi-NIC state
  const [nics, setNics] = useState<Map<string, CTNetEdit>>(new Map());

  // Pending new NICs to add
  const [pendingNics, setPendingNics] = useState<Map<string, CTNetEdit>>(
    new Map(),
  );

  // NICs to delete
  const [deleteNics, setDeleteNics] = useState<Set<string>>(new Set());

  // Unused volumes to delete
  const [deleteVolumes, setDeleteVolumes] = useState<Set<string>>(new Set());

  // A Save that would delete them, waiting on its confirmation: whether that
  // is open, the volumes it lists, whether it is asking again because they
  // changed, and whether it refused to save because the configuration
  // changed under it. The list is a snapshot of what the Save would destroy,
  // taken when it opened, and it is what Delete and Save confirms. It is
  // kept while the dialog animates closed.
  const [confirmation, setConfirmation] = useState<{
    open: boolean;
    volumes: readonly VolumeToDelete[];
    changed: boolean;
    blocked: boolean;
  }>({ open: false, volumes: [], changed: false, blocked: false });

  // Said beside Save when a Save sent nothing and there was no dialog left
  // to say it in.
  const [saveNotice, setSaveNotice] = useState<string | null>(null);

  // Where focus returns when that confirmation closes. It has no Trigger for
  // Radix to return focus to, so without this it would land on the page.
  const saveButtonRef = useRef<HTMLButtonElement>(null);

  // The notice a Delete and Save that sent nothing leaves in the dialog, and
  // the number of such stops. Each stop moves focus to the notice, not to a
  // button, so a held Enter neither confirms a list that has only just
  // appeared nor cancels, which reloads the panel. It goes by the count, not
  // by whether the notice is shown: a second stop finds it shown already,
  // with focus perhaps back on Delete and Save. The count is also its key,
  // so each stop mounts it afresh and a screen reader announces it again,
  // even in the same words.
  const noticeRef = useRef<HTMLParagraphElement>(null);
  const noticeShown = confirmation.changed || confirmation.blocked;
  const [stops, setStops] = useState(0);
  useLayoutEffect(() => {
    if (stops > 0) noticeRef.current?.focus();
  }, [stops]);

  // Saves sent, and saves a render has seen settle. While they differ a Save
  // is in flight and saveChanges sends nothing. isPending cannot stand in for
  // this: a render sees it only after TanStack's notification tick, so a
  // second call before then would still read it false and send again. Nor
  // can a Save's own settling end it: the render TanStack then gives
  // re-enables Save and Delete and Save while the afterSave it set waits for
  // the next render, so the staging and edits just sent are still there to
  // send again. So settling (on the mutation's own promise, see saveChanges)
  // bumps savesSettled, and the effect copies it once a render has it. React
  // also runs the effect when a hidden panel (<Activity>) shows again, which
  // copies only what has settled, so a Save still out stays in flight.
  const savesSent = useRef(0);
  const savesSeenSettled = useRef(0);
  const [savesSettled, setSavesSettled] = useState(0);
  useEffect(() => {
    savesSeenSettled.current = savesSettled;
  }, [savesSettled]);

  // Track original values for change detection
  const [origFields, setOrigFields] = useState<Record<string, string>>({});

  // The config the fields and the staging were last populated from, and the
  // one this render's fields were: the populate effect below moves the ref
  // after the render that brings a new config, so for that render the two
  // differ.
  const populatedFrom = useRef<VMConfig | undefined>(undefined);
  const populatedAtRender = populatedFrom.current;
  // A config that arrived while the confirmation is open, waiting on it.
  // Cancel then reloads the panel, so the dialog says so.
  const configChangedUnder = confirmation.open && config !== populatedAtRender;

  // Populate form from fetched config. One that arrives while the
  // confirmation is open waits until it closes, so the staging it confirms
  // stays put. Everything the Save sends is still built on the config it was
  // populated from, though, so saveChanges checks each key the request
  // writes or deletes against the newest config before it sends.
  useEffect(() => {
    if (!config || confirmation.open || config === populatedFrom.current) {
      return;
    }
    populatedFrom.current = config;

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
    setDeleteVolumes(new Set());

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
  }, [config, confirmation.open]);

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
    cores,
    cpulimit,
    cpuunits,
    memory,
    swap,
    hostname,
    nameserver,
    searchdomain,
    onboot,
    protection,
    cmode,
    startup,
    features,
    description,
    tags,
    nics,
    pendingNics,
    deleteNics,
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
    for (const key of deleteVolumes) {
      deletes.push(key);
    }
    if (deletes.length > 0) {
      diff["delete"] = deletes.join(",");
    }
    return diff;
  }, [currentFields, origFields, deleteNics, deleteVolumes]);

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

  // Unused volumes: still allocated and billed to the container, but not
  // mounted. Moving a volume without "delete source" produces one, so they
  // need a way out of the UI or the space is never reclaimed.
  const unusedVolumes = useMemo(() => {
    if (!config) return [];
    const vols: { key: string; volume: string }[] = [];
    for (const key of Object.keys(config)) {
      if (UNUSED_KEY_RE.test(key)) {
        vols.push({ key, volume: str(config[key]) });
      }
    }
    return vols.sort((a, b) => a.key.localeCompare(b.key));
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

  // Deleting an unusedN key destroys its volume. In pve-container,
  // update_pct_config (src/PVE/LXC/Config.pm) queues the delete. Then
  // vmconfig_apply_pending, or vmconfig_hotplug_pending on a running
  // container, passes the volume to PVE::LXC::delete_mountpoint_volume
  // (src/PVE/LXC.pm) unless is_volume_in_use finds a mount point, a snapshot
  // or a pending change still using it, and with_checked_volid skips that
  // call, with a warning, when the volume's storage no longer exists.
  // delete_mountpoint_volume frees a storage volume (not a bind or device
  // mount) with PVE::Storage::vdisk_free when this container owns it, and
  // only warns when another guest does.
  //
  // So this, the one place a Save is sent from, sends only what still stands
  // on the config it was built from. An unusedN delete goes only when its
  // caller passes the list the operator confirmed and that list still names
  // exactly what the request would destroy: the same keys, holding the same
  // volumes now. Otherwise it opens the confirmation on what it would destroy
  // now, or asks again on the new list, in place so the rest of the Save
  // stays staged. Every other key the request writes or deletes must still
  // hold what the fields were built from; if one has changed elsewhere,
  // nothing is sent and the dialog says so. A NIC removal frees no volume (on
  // an SDN vnet whose zone has IPAM and DHCP it releases the IP reserved for
  // its MAC, pve-network's del_ips_from_mac), so it is not asked about.
  function saveChanges({
    confirmed,
  }: {
    confirmed: readonly VolumeToDelete[] | null;
  }) {
    if (savesSent.current !== savesSeenSettled.current || !hasChanges) return;
    // This render's fields are built on a config already replaced, whose
    // repopulating effect has not run yet. (While the confirmation is open
    // that effect waits on purpose, and the checks below cover it.)
    if (!confirmation.open && config !== populatedAtRender) return;
    // Each setter below is called only when it changes something: even a
    // same-value set can render this component, and a render taking in
    // isPending before TanStack's own tick would change what a second click
    // meets.
    if (saveNotice !== null) setSaveNotice(null);
    // The newest config the query has. That can be a tick ahead of this
    // render, as TanStack tells React on a timer, and a click landing in that
    // tick must not pass what has just changed.
    const newest = queryClient.getQueryState<VMConfig>(configKey);
    const newestConfig = currentConfig(
      newest?.data,
      newest?.dataUpdatedAt ?? 0,
      afterSave,
    );
    // What the request would destroy now: its unusedN keys with what they
    // hold.
    const live = volumesIn(changedFields, newestConfig);

    // A staged key that is gone was removed elsewhere, and its delete is not
    // sent: pve-guest-common's add_unused_volume refills the lowest free
    // unusedN, so by the time it arrived the key could hold another volume.
    // It comes off the staging, and nothing is sent this time. With none left
    // to ask about, the dialog closes, and the populate effect then reloads
    // the panel from the newer config, discarding the rest of this Save too,
    // so the notice beside Save says that as well.
    const gone = new Set(
      live
        .filter(({ key }) => newestConfig?.[key] === undefined)
        .map(({ key }) => key),
    );
    if (gone.size > 0) {
      setConfigMutation.reset();
      setDeleteVolumes(
        (staged) => new Set([...staged].filter((key) => !gone.has(key))),
      );
      const rest = live.filter(({ key }) => !gone.has(key));
      if (rest.length > 0) {
        setConfirmation({
          open: true,
          volumes: rest,
          changed: true,
          blocked: false,
        });
        setStops((count) => count + 1);
      } else {
        setConfirmation((current) => ({ ...current, open: false }));
        setSaveNotice(
          "The volumes marked for deletion are no longer on this container, so nothing was saved, and the panel reloaded, discarding any other unsaved changes.",
        );
      }
      return;
    }

    // Every other key the request writes or deletes must still hold what the
    // fields were built from. One changed elsewhere since would be overwritten,
    // or a NIC re-created under a staged key removed. Nothing is sent; with
    // the dialog open it says so, and Cancel reloads the panel. (Closed, the
    // newer config is a tick away from reloading the panel itself.)
    const moved = keysBesidesVolumes(changedFields).some(
      (key) => str(populatedAtRender?.[key]) !== str(newestConfig?.[key]),
    );
    if (moved) {
      if (confirmed !== null) {
        setConfigMutation.reset();
        setConfirmation((current) => ({ ...current, blocked: true }));
        setStops((count) => count + 1);
      }
      return;
    }

    const asks =
      confirmed === null ? live.length > 0 : !sameVolumes(confirmed, live);
    if (asks) {
      // A failure left over from an earlier Save belongs to that one, not to
      // the confirmation about to open.
      setConfigMutation.reset();
      setConfirmation({
        open: live.length > 0,
        volumes: live,
        changed: confirmed !== null,
        blocked: false,
      });
      if (confirmed !== null) setStops((count) => count + 1);
      return;
    }
    // Sending: "nothing was deleted" must not outlive the attempt it was
    // about, least of all into a failure, as Proxmox frees an unusedN volume
    // before it applies the rest of the write.
    if (confirmation.changed) {
      setConfirmation((current) => ({ ...current, changed: false }));
    }
    const fields = changedFields;
    // What this Save was built from (an earlier Save's afterSave, while the
    // fetch after that one is still out), and the fetched copy under it. The
    // next afterSave is built on the first, so it keeps that Save's changes.
    const shown = config;
    const fetched = fetchedConfig;
    savesSent.current += 1;
    // The mutation's own promise, not mutate()'s per-call callbacks: TanStack
    // runs those only while this panel's observer has listeners, and a parent
    // that keeps the panel alive while hiding it (<Activity>) takes them
    // away, which would leave the Save forever unsettled and Save dead.
    void setConfigMutation
      .mutateAsync({ clusterId, resourceId: ctId, kind: "ct", fields })
      .then(
        () => {
          // Closed first, as the populate effect waits while it is open.
          // That then re-reads every field from the config as it now stands
          // and clears all that was staged, so nothing sent is sent or
          // offered again.
          setConfirmation((current) => ({ ...current, open: false }));
          if (shown !== undefined && fetched !== undefined) {
            setAfterSave({
              over: fetched,
              config: applyConfigWrite(shown, fields),
              at: Date.now(),
            });
          }
        },
        // Already shown: the dialog and the Save bar read the mutation's
        // error, and the app's mutation cache toasts it.
        () => undefined,
      )
      .finally(() => {
        setSavesSettled((settled) => settled + 1);
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
        <div className="flex items-center gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-2 text-sm text-amber-700 dark:text-amber-400">
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
            <span className="text-xs text-emerald-600">Saved</span>
          )}
          {setConfigMutation.isError && (
            <span className="text-xs text-destructive">
              {setConfigMutation.error.message}
            </span>
          )}
          {saveNotice !== null && (
            <span
              role="status"
              className="text-xs text-amber-700 dark:text-amber-400"
            >
              {saveNotice}
            </span>
          )}
        </div>
        <Button
          ref={saveButtonRef}
          size="sm"
          className="gap-1.5"
          disabled={!hasChanges || setConfigMutation.isPending}
          onClick={() => {
            saveChanges({ confirmed: null });
          }}
        >
          {setConfigMutation.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Save className="h-3.5 w-3.5" />
          )}
          Save Changes
        </Button>
      </div>

      {/* Held open while the Save is in flight: Cancel cannot un-send it, and
          closing would hide whether it worked. Its buttons are disabled
          meanwhile, so one confirmation sends one request; a closing dialog
          stays clickable for as long as its exit animation runs. Success
          closes it, and a failure stays on screen to retry or cancel, with
          the volumes still staged. A refetch while it is open, such as the
          vm_state_change and inventory_change events trigger for every
          container in the cluster, changes neither: the populate effect
          waits, and the list shown is the snapshot the operator confirms,
          which saveChanges checks against the volumes the keys hold now. */}
      <AlertDialog
        open={confirmation.open}
        onOpenChange={(open) => {
          if (!open && !setConfigMutation.isPending) {
            setConfirmation((current) => ({ ...current, open: false }));
          }
        }}
      >
        <AlertDialogContent
          onCloseAutoFocus={(event) => {
            // Back to Save, which opened it. After a successful Save that
            // button is disabled, having nothing left to send, and a disabled
            // button cannot take focus.
            event.preventDefault();
            saveButtonRef.current?.focus();
          }}
        >
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete {confirmation.volumes.length} unused volume
              {confirmation.volumes.length !== 1 ? "s" : ""}?
            </AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2">
                {noticeShown && (
                  <p
                    key={stops}
                    ref={noticeRef}
                    tabIndex={-1}
                    role="alert"
                    className="rounded-sm font-medium text-destructive focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
                  >
                    {confirmation.blocked
                      ? "Nothing was saved: this container's configuration changed while this was open, including what this Save changes. Cancel reloads it; then make your changes again."
                      : "These volumes changed after the list was shown, so nothing was deleted. Check the list again."}
                  </p>
                )}
                {configChangedUnder && !confirmation.blocked && (
                  <p
                    role="status"
                    className="text-amber-700 dark:text-amber-400"
                  >
                    This container&apos;s configuration changed while this was
                    open. Cancel reloads it, discarding the changes you have not
                    saved.
                  </p>
                )}
                <p>
                  Saving will permanently delete each volume below from storage,
                  along with all data on it. This action cannot be undone.
                </p>
                <ul className="space-y-1 rounded-md border border-destructive/30 bg-destructive/5 p-2">
                  {confirmation.volumes.map(({ key, volume }) => (
                    <li key={key}>
                      <span className="font-mono text-xs">{key}</span>{" "}
                      <span className="break-all font-mono font-semibold">
                        {volume}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          {setConfigMutation.isError && (
            <p className="text-sm text-destructive">
              {setConfigMutation.error.message}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={setConfigMutation.isPending}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={setConfigMutation.isPending || confirmation.blocked}
              onClick={(event) => {
                // Stays open until the outcome is known.
                event.preventDefault();
                // A double-click is one decision. Its second click would
                // otherwise confirm the list that the first asked again on,
                // before anyone could read it.
                if (event.detail > 1) return;
                saveChanges({ confirmed: confirmation.volumes });
              }}
            >
              {setConfigMutation.isPending ? "Saving..." : "Delete and Save"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

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
        <RootFSRow
          config={config}
          clusterId={clusterId}
          ctId={ctId}
          storageOptions={storageOptions}
        />
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

      {/* Unused Volumes */}
      {unusedVolumes.length > 0 && (
        <Section title="Unused Volumes">
          <div className="space-y-2">
            {unusedVolumes.map(({ key, volume }) =>
              deleteVolumes.has(key) ? (
                <div
                  key={key}
                  className="flex items-center gap-2 rounded border border-destructive/30 bg-destructive/5 px-2 py-1 text-sm"
                >
                  <Badge variant="destructive" className="text-[10px]">
                    removing
                  </Badge>
                  <span className="font-mono text-xs">{key}</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="ml-auto h-6 px-2 text-xs"
                    onClick={() => {
                      setDeleteVolumes((prev) => {
                        const next = new Set(prev);
                        next.delete(key);
                        return next;
                      });
                    }}
                  >
                    Undo
                  </Button>
                </div>
              ) : (
                <div
                  key={key}
                  className="flex items-center gap-2 rounded border border-amber-300 bg-amber-50 px-2 py-1 dark:border-amber-800 dark:bg-amber-950"
                >
                  <span className="font-mono text-xs font-medium text-amber-700 dark:text-amber-400">
                    {key}
                  </span>
                  <span className="truncate text-[10px] text-amber-600 dark:text-amber-400">
                    {volume}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="ml-auto h-6 gap-1 px-2 text-[10px] text-destructive hover:text-destructive"
                    onClick={() => {
                      setDeleteVolumes((prev) => new Set(prev).add(key));
                    }}
                    title="Remove volume"
                  >
                    <Trash2 className="h-3 w-3" /> Remove
                  </Button>
                </div>
              ),
            )}
            <p className="text-[10px] text-muted-foreground">
              Removing a volume deletes its data. Changes apply on Save.
            </p>
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
                  <Badge className="bg-emerald-600 text-[10px]">new</Badge>
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
                next.set(
                  key,
                  emptyCTNet({
                    name: `eth${key.replace("net", "")}`,
                    bridge: bridgeList[0] ?? "vmbr0",
                    ip: "dhcp",
                    firewall: true,
                  }),
                );
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
                className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs placeholder:text-muted-foreground focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
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
