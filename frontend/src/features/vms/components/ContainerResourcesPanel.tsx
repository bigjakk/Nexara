import {
  use,
  useEffect,
  useLayoutEffect,
  useState,
  useMemo,
  useRef,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Loader2,
  Plus,
  Trash2,
  Network,
  HardDrive,
  FolderOpen,
} from "lucide-react";
import { formatBytes } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
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
import {
  parseKVString,
  pveBoolean,
  parseStartup,
  buildStartup,
  type ParsedStartup,
} from "../lib/vm-config-parsers";
import { parseCTNet, buildCTNet, emptyCTNet } from "../lib/ct-net";
import type { CTNetEdit } from "../lib/ct-net";
import { parseFeatures, buildFeatures } from "../lib/ct-features";
import type { CTFeatures } from "../lib/ct-features";
import type { VMConfig } from "../types/vm";
import {
  Check,
  Checks,
  DeviceList,
  DeviceRow,
  Field,
  FieldGrid,
  Group,
  GroupList,
  PendingBadge,
  SaveBar,
} from "./hardware/HardwareRows";
import { RowsReadOnlyContext } from "./hardware/read-only";
import { usePermissions } from "@/hooks/usePermissions";
import {
  clearSaveBarClass,
  fieldInputClass,
  fieldSelectClass,
} from "./hardware/field-classes";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useTranslation, Trans } from "react-i18next";
import i18n from "@/lib/i18n";

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
  // Booleans of pve-container's mount point format (src/PVE/LXC/Config.pm),
  // read as Proxmox reads them; backup and replicate default to on.
  const flag = (key: string, absent: boolean) => {
    const v = kv.get(key);
    return v === undefined ? absent : pveBoolean(v);
  };
  result.acl = flag("acl", false);
  result.backup = flag("backup", true);
  result.quota = flag("quota", false);
  result.ro = flag("ro", false);
  result.replicate = flag("replicate", true);
  return result;
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
  const { t } = useTranslation("vms");
  const [open, setOpen] = useState(false);
  const [targetStorage, setTargetStorage] = useState("");
  // Off by default, matching Proxmox: the source volume is kept as an unused
  // entry on the container so the move can be undone by hand.
  const [deleteOriginal, setDeleteOriginal] = useState(false);
  const [bwlimit, setBwlimit] = useState("");
  const moveMutation = useMoveContainerVolume();
  const settle = useSaveOutcome(open);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const filteredOptions = storageOptions.filter((s) => s !== currentStorage);
  const { value: bwlimitKib, invalid: bwlimitInvalid } = parseBwlimit(bwlimit);

  function handleMove() {
    if (!targetStorage || bwlimitInvalid) return;
    const focus = (data: { upid: string }) => {
      if (data.upid) {
        setFocusedTask({
          clusterId,
          upid: data.upid,
          description: t("containerResourcesPanel.moveVolumeKeyTargetStorage", {
            volumeKey,
            targetStorage,
          }),
        });
      }
    };
    const reset = () => {
      setTargetStorage("");
      setDeleteOriginal(false);
      setBwlimit("");
    };
    settle(
      moveMutation.mutateAsync({
        clusterId,
        ctId,
        volume: volumeKey,
        storage: targetStorage,
        deleteOriginal,
        bwlimitKib,
      }),
      {
        action: t("containerResourcesPanel.movingVolumeKeyToTargetStorage", {
          volumeKey,
          targetStorage,
        }),
        onSuccess: (data) => {
          focus(data);
          setOpen(false);
          reset();
        },
        // The dialog stays mounted while closed: follow the task and put the
        // form away as onSuccess would have.
        onLateSuccess: (data, now) => {
          focus(data);
          if (!now.open) reset();
        },
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button
        size="sm"
        variant="outline"
        aria-label={t("containerResourcesPanel.moveVolumeKeyToAnotherStorage", {
          volumeKey,
        })}
        onClick={() => {
          setOpen(true);
        }}
      >
        {t("containerResourcesPanel.move")}
      </Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("containerResourcesPanel.moveVolumeVolumeKey", { volumeKey })}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {currentStorage && (
            <p className="text-sm text-muted-foreground">
              <Trans
                t={t}
                i18nKey="containerResourcesPanel.currentStorageSpanCurrentStorageSpan"
                components={{
                  storage: (
                    <span className="font-mono font-medium">
                      {currentStorage}
                    </span>
                  ),
                }}
              />
            </p>
          )}
          <div className="space-y-2">
            <Label htmlFor="ct-target-storage">
              {t("containerResourcesPanel.targetStorage")}
            </Label>
            <select
              id="ct-target-storage"
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
              value={targetStorage}
              onChange={(e) => {
                setTargetStorage(e.target.value);
              }}
            >
              <option value="">
                {t("containerResourcesPanel.selectStorage")}
              </option>
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
            keptHint={t("containerResourcesPanel.theSourceVolumeIsKept")}
          />
          <Button
            onClick={handleMove}
            disabled={
              !targetStorage || bwlimitInvalid || moveMutation.isPending
            }
            className="w-full"
          >
            {moveMutation.isPending
              ? t("containerResourcesPanel.moving")
              : t("containerResourcesPanel.moveVolume")}
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
// Volume rows: the root disk and the mount points
// ---------------------------------------------------------------------------

/**
 * A volume's resize: the size typed and the request, held by the volume's row
 * so both outlive its fields being shut. Unlike the tab's other edits it is
 * not staged for Save: Resize sends it at once.
 */
function useVolumeResize(clusterId: string, ctId: string, disk: string) {
  const { t } = useTranslation("vms");
  const [resizeSize, setResizeSize] = useState("");
  const resizeMutation = useResizeContainerDisk();
  const settle = useSaveOutcome();
  return {
    size: resizeSize,
    setSize: setResizeSize,
    isPending: resizeMutation.isPending,
    isSuccess: resizeMutation.isSuccess,
    error: resizeMutation.isError ? resizeMutation.error.message : null,
    send: () => {
      settle(
        resizeMutation.mutateAsync({
          clusterId,
          ctId,
          disk,
          size: resizeSize.trim(),
        }),
        {
          action: t("containerResourcesPanel.resizingDisk", { disk }),
          onSuccess: () => {
            setResizeSize("");
          },
          onError: () => undefined, // shown below from the mutation's state
        },
      );
    },
  };
}

type VolumeResize = ReturnType<typeof useVolumeResize>;

/**
 * The volume move takes execute:container where the tab's edits take manage,
 * so it is offered on a read-only tab too.
 */
function useCanMoveVolume(): boolean {
  return usePermissions().hasPermission("execute", "container");
}

/** How the last resize went, on the row itself: it shows with the fields shut. */
function ResizeResult({ resize }: { resize: VolumeResize }) {
  const { t } = useTranslation("vms");
  if (resize.error !== null) {
    return (
      <span className="text-[12.5px] text-destructive">{resize.error}</span>
    );
  }
  if (resize.isSuccess) {
    return (
      <span className="text-[12.5px] text-muted-foreground">
        {t("containerResourcesPanel.resized")}
      </span>
    );
  }
  return null;
}

/** The resize fields, under the volume's row. */
function ResizeEditor({
  resize,
  placeholder,
}: {
  resize: VolumeResize;
  placeholder: string;
}) {
  const { t } = useTranslation("vms");
  return (
    <div className="flex flex-wrap items-end gap-2">
      <Field label={t("containerResourcesPanel.resizeTo")} className="w-36">
        <Input
          placeholder={placeholder}
          value={resize.size}
          onChange={(e) => {
            resize.setSize(e.target.value);
          }}
          className={fieldInputClass}
        />
      </Field>
      <Button
        size="sm"
        variant="outline"
        className="gap-1.5"
        disabled={!resize.size.trim() || resize.isPending}
        onClick={resize.send}
      >
        {resize.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
        {t("containerResourcesPanel.resize")}
      </Button>
      <p className="basis-full text-[12.5px] text-muted-foreground">
        {t("containerResourcesPanel.appliesAtOnceNotOn")}
      </p>
    </div>
  );
}

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
  const { t } = useTranslation("vms");
  const raw = str(config["rootfs"]);
  const kv = parseKVString(raw);
  const volume = raw.split(",")[0] ?? "";
  const currentStorage = volume.split(":")[0] ?? "";
  const currentSize = kv.get("size") ?? "";
  const [open, setOpen] = useState(false);
  const resize = useVolumeResize(clusterId, ctId, "rootfs");
  const canMove = useCanMoveVolume();

  return (
    <DeviceRow
      name="rootfs"
      icon={HardDrive}
      title={t("containerResourcesPanel.rootDisk")}
      marker={<ResizeResult resize={resize} />}
      summary={
        <>
          <span>{currentSize || "--"}</span>
          {currentStorage && (
            <span>
              {t("containerResourcesPanel.onCurrentStorage", {
                currentStorage,
              })}
            </span>
          )}
          <span className="break-all font-mono">{volume || "--"}</span>
        </>
      }
      open={open}
      onToggle={() => {
        setOpen((was) => !was);
      }}
      editLabel={t("containerResourcesPanel.resizeRootfs")}
      ownActions={
        canMove ? (
          <MoveVolumeDialog
            clusterId={clusterId}
            ctId={ctId}
            volumeKey="rootfs"
            currentStorage={currentStorage}
            storageOptions={storageOptions}
          />
        ) : undefined
      }
      editor={<ResizeEditor resize={resize} placeholder="e.g. 32G" />}
    />
  );
}

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
  const { t } = useTranslation("vms");
  const currentStorage = mp.volume.split(":")[0] ?? "";
  const [open, setOpen] = useState(false);
  // Only a volume with a size can be resized: a bind or device mount has none.
  const resizable = mp.size !== "";
  const resize = useVolumeResize(clusterId, ctId, deviceKey);
  const canMove = useCanMoveVolume();

  return (
    <DeviceRow
      name={deviceKey}
      icon={FolderOpen}
      title={t("containerResourcesPanel.mountPoint")}
      marker={<ResizeResult resize={resize} />}
      summary={
        <>
          <span className="break-all font-mono">{mp.path}</span>
          <span>{mp.size || "--"}</span>
          <span className="break-all font-mono">{mp.volume}</span>
          {mp.backup && <span>{t("containerResourcesPanel.inBackups")}</span>}
          {mp.ro && <span>{t("containerResourcesPanel.readOnly")}</span>}
          {mp.quota && <span>{t("containerResourcesPanel.quota")}</span>}
        </>
      }
      open={open}
      onToggle={
        resizable
          ? () => {
              setOpen((was) => !was);
            }
          : undefined
      }
      editLabel={t("containerResourcesPanel.resizeDeviceKey", { deviceKey })}
      ownActions={
        canMove ? (
          <MoveVolumeDialog
            clusterId={clusterId}
            ctId={ctId}
            volumeKey={deviceKey}
            currentStorage={currentStorage}
            storageOptions={storageOptions}
          />
        ) : undefined
      }
      editor={
        resizable ? (
          <ResizeEditor resize={resize} placeholder="e.g. 16G" />
        ) : undefined
      }
    />
  );
}

// ---------------------------------------------------------------------------
// NIC rows
// ---------------------------------------------------------------------------

/** A NIC in a few words, as its row reads while the fields are closed. */
function NICSummary({ nic }: { nic: CTNetEdit }) {
  const { t } = useTranslation("vms");
  return (
    <>
      <span>{nic.name || t("containerResourcesPanel.noName")}</span>
      <span>
        {nic.bridge
          ? t("containerResourcesPanel.onBridge", { bridge: nic.bridge })
          : t("containerResourcesPanel.noBridge")}
      </span>
      {nic.ip && (
        <span>
          {nic.ip === "dhcp" ? t("containerResourcesPanel.dhcp") : nic.ip}
        </span>
      )}
      {nic.ip6 && (
        <span>{t("containerResourcesPanel.iPv6Ip6", { ip6: nic.ip6 })}</span>
      )}
      {nic.tag && (
        <span>{t("containerResourcesPanel.vlanTag", { tag: nic.tag })}</span>
      )}
      {nic.hwaddr && <span className="font-mono">{nic.hwaddr}</span>}
      {nic.firewall && <span>{t("containerResourcesPanel.firewallOn")}</span>}
    </>
  );
}

function NICFields({
  nic,
  onChange,
  bridges,
  macPending = false,
}: {
  nic: CTNetEdit;
  onChange: (updated: CTNetEdit) => void;
  bridges: string[];
  /**
   * A NIC that a Save just created, or rewrote, without a MAC, shown from
   * what that Save sent until Proxmox's own copy arrives with the MAC it gave
   * it. It is read only until then: pve-container's parse_lxc_network
   * (src/PVE/LXC/Config.pm) gives a value with no hwaddr a new random one,
   * so sent again without it the NIC would change its MAC.
   */
  macPending?: boolean;
}) {
  const { t } = useTranslation("vms");
  return (
    <fieldset disabled={macPending} className="min-w-0 space-y-3">
      <FieldGrid>
        <Field label={t("containerResourcesPanel.name")}>
          <Input
            value={nic.name}
            onChange={(e) => {
              onChange({ ...nic, name: e.target.value });
            }}
            placeholder="eth0"
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.bridge")}>
          <select
            className={fieldSelectClass}
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
        </Field>
        <Field label={t("containerResourcesPanel.iPv4CIDROrDHCP")}>
          <Input
            value={nic.ip}
            onChange={(e) => {
              onChange({ ...nic, ip: e.target.value });
            }}
            placeholder={t("containerResourcesPanel.dhcpOr19202")}
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.iPv4Gateway")}>
          <Input
            value={nic.gw}
            onChange={(e) => {
              onChange({ ...nic, gw: e.target.value });
            }}
            placeholder="192.0.2.1"
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.iPv6CIDROrDHCP")}>
          <Input
            value={nic.ip6}
            onChange={(e) => {
              onChange({ ...nic, ip6: e.target.value });
            }}
            placeholder={t("containerResourcesPanel.autoDhcpOr2001Db8")}
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.iPv6Gateway")}>
          <Input
            value={nic.gw6}
            onChange={(e) => {
              onChange({ ...nic, gw6: e.target.value });
            }}
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.vlanTag2")}>
          <Input
            type="number"
            min={1}
            max={4094}
            value={nic.tag}
            onChange={(e) => {
              onChange({ ...nic, tag: e.target.value });
            }}
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.rateLimitMBS")}>
          <Input
            type="number"
            min={0}
            step={0.1}
            value={nic.rate}
            onChange={(e) => {
              onChange({ ...nic, rate: e.target.value });
            }}
            className={fieldInputClass}
          />
        </Field>
        <Field label={t("containerResourcesPanel.mtu")}>
          <Input
            type="number"
            min={68}
            max={65535}
            value={nic.mtu}
            onChange={(e) => {
              onChange({ ...nic, mtu: e.target.value });
            }}
            className={fieldInputClass}
          />
        </Field>
      </FieldGrid>
      <Checks>
        <Check
          label={t("containerResourcesPanel.firewall")}
          checked={nic.firewall}
          onCheckedChange={(v) => {
            onChange({ ...nic, firewall: v === true });
          }}
        />
      </Checks>
    </fieldset>
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

// The fields a Save deletes when they are cleared. pve-container refuses an
// empty value for most (a type or format check: integer, number, dns-name,
// pve-startup-order), and the whole write with it; features, nameserver,
// searchdomain and tags it takes empty (verify_nameserver_list,
// verify_searchdomain_list and get_unique_tags join an empty list to ""),
// but stores as sent and reads back as " ". Absent, each reads as unset
// (src/PVE/LXC.pm, src/PVE/LXC/Config.pm): all of the host's cores, no CPU
// limit, the cgroup's default CPU weight, no swap (`$conf->{swap} // 0`;
// deleting it hotplugs 0), the hostname CT<vmid>, no startup order, no
// features, the host's DNS settings, no tags. Memory is not one:
// update_pct_config refuses to delete it, so a Save waits until it has a value.
const DELETED_WHEN_CLEARED = new Set([
  "cores",
  "cpulimit",
  "cpuunits",
  "swap",
  "hostname",
  "nameserver",
  "searchdomain",
  "startup",
  "features",
  "tags",
]);

const consoleModes = [
  {
    value: "tty",
    get label() {
      return i18n.t("vms:containerResourcesPanel.tty");
    },
  },
  {
    value: "console",
    get label() {
      return i18n.t("vms:containerResourcesPanel.console");
    },
  },
  {
    value: "shell",
    get label() {
      return i18n.t("vms:containerResourcesPanel.shell");
    },
  },
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

/**
 * A role without manage:container, which the config route takes, sees the tab
 * read-only.
 */
export function ContainerResourcesPanel(props: ContainerResourcesPanelProps) {
  const { hasPermission } = usePermissions();
  return (
    <RowsReadOnlyContext value={!hasPermission("manage", "container")}>
      <ContainerResourcesPanelBody {...props} />
    </RowsReadOnlyContext>
  );
}

function ContainerResourcesPanelBody({
  clusterId,
  ctId,
  ctStatus,
  nodeName,
}: ContainerResourcesPanelProps) {
  const { t } = useTranslation("vms");
  const readOnly = use(RowsReadOnlyContext);
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
  const setConfigMutation = useSetResourceConfig(true);
  const settle = useSaveOutcome();
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
  const [startup, setStartup] = useState<ParsedStartup>({
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

  // Which NIC rows have their fields open. Display only: closing a row keeps
  // what was typed.
  const [openRows, setOpenRows] = useState<Set<string>>(new Set());

  // Where focus goes when that confirmation closes and Save, which opened it,
  // cannot take it back: after a successful Save it is disabled, having
  // nothing left to send. The bar says "Saved", or why not.
  const saveBarRef = useRef<HTMLDivElement>(null);

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

    // Absent, each reads as Proxmox runs it (DELETED_WHEN_CLEARED): no number
    // for cores or cpuunits, 512 MiB of memory (`|| 512`) and no swap.
    setCores(str(config["cores"]));
    setCpulimit(str(config["cpulimit"]));
    setCpuunits(str(config["cpuunits"]));
    setMemory(str(config["memory"]) || "512");
    setSwap(str(config["swap"]) || "0");
    setHostname(str(config["hostname"]));
    setNameserver(str(config["nameserver"]));
    setSearchdomain(str(config["searchdomain"]));
    setOnboot(num(config["onboot"]) === 1);
    setProtection(num(config["protection"]) === 1);
    setCmode(str(config["cmode"]) || "tty");
    setStartup(parseStartup(str(config["startup"])));
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
    orig["cores"] = str(config["cores"]);
    orig["cpulimit"] = str(config["cpulimit"]);
    orig["cpuunits"] = str(config["cpuunits"]);
    orig["memory"] = str(config["memory"]) || "512";
    orig["swap"] = str(config["swap"]) || "0";
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
    // Rebuilt on the value it was read from, as features is: a bare order
    // stays, and an untouched startup is the stored string.
    f["startup"] = buildStartup(startup, origFields["startup"] ?? "");
    // Rebuilt on the value it was read from, so an untouched one is the
    // stored string, in its order, keys this form has no field for included.
    f["features"] = buildFeatures(features, origFields["features"] ?? "");
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
    origFields,
    description,
    tags,
    nics,
    pendingNics,
    deleteNics,
  ]);

  // Detect changes
  const changedFields = useMemo(() => {
    const diff: Record<string, string> = {};
    const deletes: string[] = [];
    for (const [key, val] of Object.entries(currentFields)) {
      if (val === (origFields[key] ?? "")) continue;
      if (val === "" && DELETED_WHEN_CLEARED.has(key)) deletes.push(key);
      else diff[key] = val;
    }
    // Handle deletions
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
  // Proxmox can neither take an empty memory nor delete it.
  const memoryMissing = memory === "";
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
    if (
      savesSent.current !== savesSeenSettled.current ||
      !hasChanges ||
      memoryMissing
    ) {
      return;
    }
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
        setSaveNotice(t("containerResourcesPanel.theVolumesMarkedForDeletion"));
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
    const save = setConfigMutation.mutateAsync({
      clusterId,
      resourceId: ctId,
      kind: "ct",
      fields,
    });
    const saved = () => {
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
    };
    // Hidden under <Activity> counts as gone to useSaveOutcome, so a success
    // then is applied all the same.
    settle(save, {
      action: t("containerResourcesPanel.savingTheContainerSResources"),
      onSuccess: saved,
      onLateSuccess: saved,
      onError: () => undefined, // the dialog and the Save bar read the mutation's error
    });
    void save
      .catch(() => undefined)
      .finally(() => {
        setSavesSettled((settled) => settled + 1);
      });
  }

  if (isLoading) {
    return <Skeleton className="h-96 w-full" />;
  }

  if (error) {
    return (
      <Panel>
        <p className="text-sm text-destructive">
          {t("containerResourcesPanel.failedToLoadContainerConfig", {
            message: error.message,
          })}
        </p>
      </Panel>
    );
  }

  if (!config) return null;

  // check_ct_modify_config_perm (src/PVE/LXC.pm) reads this to decide who may
  // change a container's features; see the Features section.
  const unprivileged = num(config["unprivileged"]) === 1;

  function toggleRow(key: string) {
    setOpenRows((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  // For the tab's opening sentence and the panels' asides. Display only.
  const changeCount = Object.keys(changedFields).length;
  const memoryBytes = num(memory) * 1024 * 1024;
  const nicCount = nics.size + pendingNics.size;
  /** "1 core", "2 volumes": a count with its noun, from containerResourcesPanel.count. */
  const plural = (n: number, noun: string) =>
    t(`containerResourcesPanel.count.${noun}`, { count: n });
  const coresText =
    cores === ""
      ? t("containerResourcesPanel.allOfTheHostsCores")
      : plural(num(cores), "core");

  return (
    <div className={`space-y-4 ${clearSaveBarClass}`}>
      <p className="text-sm text-muted-foreground">
        {t(
          isRunning
            ? "containerResourcesPanel.summaryRunning"
            : "containerResourcesPanel.summary",
          {
            cores: coresText,
            memory: formatBytes(memoryBytes),
            volumes: plural(mountPoints.length + 1, "volume"),
            nics: plural(nicCount, "networkInterface"),
          },
        )}
      </p>

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
        <AlertDialogContent fallbackFocus={() => saveBarRef.current}>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("containerResourcesPanel.deleteUnusedVolumes", {
                count: confirmation.volumes.length,
              })}
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
                      ? t(
                          "containerResourcesPanel.nothingWasSavedThisContainer",
                        )
                      : t(
                          "containerResourcesPanel.theseVolumesChangedAfterThe",
                        )}
                  </p>
                )}
                {configChangedUnder && !confirmation.blocked && (
                  <p
                    role="status"
                    className="text-amber-700 dark:text-amber-400"
                  >
                    {t(
                      "containerResourcesPanel.thisContainerSConfigurationChanged",
                    )}
                  </p>
                )}
                <p>
                  {t("containerResourcesPanel.savingWillPermanentlyDeleteEach")}
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
              {t("containerResourcesPanel.cancel")}
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
              {setConfigMutation.isPending
                ? t("containerResourcesPanel.saving")
                : t("containerResourcesPanel.deleteAndSave")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Two columns that fold into one: what the container is made of on
          the left, how it starts and what it may do on the right. */}
      <div className="flex flex-wrap items-start gap-4">
        <div className="flex min-w-0 flex-[3_1_520px] flex-col gap-4">
          <Panel
            flush
            title={t("containerResourcesPanel.processorAndMemory")}
            aside={t("containerResourcesPanel.coresAndMemory", {
              cores:
                cores === ""
                  ? t("containerResourcesPanel.allCores")
                  : plural(num(cores), "core"),
              memory: formatBytes(memoryBytes),
            })}
          >
            <GroupList>
              <Group
                title={t("containerResourcesPanel.processor")}
                summary={
                  cores === ""
                    ? t("containerResourcesPanel.everyCoreOfTheHost")
                    : undefined
                }
              >
                <FieldGrid>
                  <Field label={t("containerResourcesPanel.cores")}>
                    <Input
                      type="number"
                      min={1}
                      max={512}
                      value={cores}
                      onChange={(e) => {
                        setCores(e.target.value);
                      }}
                      placeholder="all"
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.cpuLimit")}>
                    <Input
                      type="number"
                      min={0}
                      max={128}
                      step={0.1}
                      value={cpulimit}
                      onChange={(e) => {
                        setCpulimit(e.target.value);
                      }}
                      placeholder={t("containerResourcesPanel.n0Unlimited")}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.cpuUnits")}>
                    <Input
                      type="number"
                      min={2}
                      max={500000}
                      value={cpuunits}
                      onChange={(e) => {
                        setCpuunits(e.target.value);
                      }}
                      placeholder={t("containerResourcesPanel.default")}
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
              </Group>

              <Group
                title={t("containerResourcesPanel.memory")}
                summary={formatBytes(memoryBytes)}
              >
                <FieldGrid>
                  <Field
                    label={t("containerResourcesPanel.memoryMiB")}
                    note={
                      memoryMissing && (
                        <p className="mt-1 text-[12.5px] text-destructive">
                          {t(
                            "containerResourcesPanel.proxmoxCannotUnsetAContainer",
                          )}
                        </p>
                      )
                    }
                  >
                    <Input
                      type="number"
                      min={16}
                      step={16}
                      value={memory}
                      onChange={(e) => {
                        setMemory(e.target.value);
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.swapMiB")}>
                    <Input
                      type="number"
                      min={0}
                      step={16}
                      value={swap}
                      onChange={(e) => {
                        setSwap(e.target.value);
                      }}
                      placeholder="0"
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
              </Group>
            </GroupList>
          </Panel>

          <Panel
            flush
            title={t("containerResourcesPanel.volumes")}
            aside={
              unusedVolumes.length > 0
                ? t("containerResourcesPanel.volumesMountedUnused", {
                    volumes: plural(mountPoints.length + 1, "volume"),
                    unused: String(unusedVolumes.length),
                  })
                : t("containerResourcesPanel.volumesMounted", {
                    volumes: plural(mountPoints.length + 1, "volume"),
                  })
            }
          >
            <DeviceList>
              <RootFSRow
                config={config}
                clusterId={clusterId}
                ctId={ctId}
                storageOptions={storageOptions}
              />
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
              {unusedVolumes.map(({ key, volume }) =>
                deleteVolumes.has(key) ? (
                  <DeviceRow
                    key={key}
                    name={key}
                    icon={HardDrive}
                    title={t("containerResourcesPanel.detachedVolume")}
                    struck
                    marker={
                      <PendingBadge>
                        {t("containerResourcesPanel.markedForRemoval")}
                      </PendingBadge>
                    }
                    summary={
                      <span className="break-all font-mono">{volume}</span>
                    }
                    actions={
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 px-2"
                        onClick={() => {
                          setDeleteVolumes((prev) => {
                            const next = new Set(prev);
                            next.delete(key);
                            return next;
                          });
                        }}
                      >
                        {t("containerResourcesPanel.undo")}
                      </Button>
                    }
                  />
                ) : (
                  <DeviceRow
                    key={key}
                    name={key}
                    icon={HardDrive}
                    title={t("containerResourcesPanel.detachedVolume")}
                    marker={
                      <Badge variant="warning">
                        {t("containerResourcesPanel.unused")}
                      </Badge>
                    }
                    summary={
                      <span className="break-all font-mono">{volume}</span>
                    }
                    actions={
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                        onClick={() => {
                          setDeleteVolumes((prev) => new Set(prev).add(key));
                        }}
                        title={t("containerResourcesPanel.removeVolume")}
                      >
                        <Trash2 className="h-4 w-4" />
                        <span className="sr-only">
                          {t("containerResourcesPanel.remove")}
                        </span>
                      </Button>
                    }
                  />
                ),
              )}
            </DeviceList>
            {unusedVolumes.length > 0 && (
              <p className="border-t px-[18px] pt-2.5 text-[12.5px] text-muted-foreground">
                {t("containerResourcesPanel.anUnusedVolumeStillTakes")}
              </p>
            )}
          </Panel>

          <Panel
            flush
            title={t("containerResourcesPanel.network")}
            aside={plural(nicCount, "interface")}
            actions={
              readOnly ? undefined : (
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
                  <Plus className="h-3.5 w-3.5" />{" "}
                  {t("containerResourcesPanel.addNetworkInterface")}
                </Button>
              )
            }
          >
            {nicCount === 0 && (
              <p className="border-t px-[18px] py-3 text-sm text-muted-foreground">
                {t("containerResourcesPanel.noNetworkInterfacesConfigured")}
              </p>
            )}
            <DeviceList>
              {Array.from(nics.entries())
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([key, nic]) => {
                  if (deleteNics.has(key)) {
                    return (
                      <DeviceRow
                        key={key}
                        name={key}
                        icon={Network}
                        title={t("containerResourcesPanel.networkInterface")}
                        struck
                        marker={
                          <PendingBadge>
                            {t("containerResourcesPanel.markedForRemoval")}
                          </PendingBadge>
                        }
                        actions={
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 px-2"
                            onClick={() => {
                              setDeleteNics((prev) => {
                                const next = new Set(prev);
                                next.delete(key);
                                return next;
                              });
                            }}
                          >
                            {t("containerResourcesPanel.undo")}
                          </Button>
                        }
                      />
                    );
                  }
                  // Shown from what the last Save sent, which created or
                  // rewrote it with no MAC, and not yet from Proxmox's copy,
                  // which names the one it gave: config is that Save's
                  // result until the copy lands.
                  const macPending =
                    nic.hwaddr === "" &&
                    str(config[key]) !== str(fetchedConfig?.[key]);
                  const edited =
                    origFields[key] !== undefined &&
                    buildCTNet(nic) !== origFields[key];
                  return (
                    <DeviceRow
                      key={key}
                      name={key}
                      icon={Network}
                      title={t("containerResourcesPanel.networkInterface")}
                      marker={
                        edited ? (
                          <PendingBadge>
                            {t("containerResourcesPanel.edited")}
                          </PendingBadge>
                        ) : null
                      }
                      summary={
                        <>
                          <NICSummary nic={nic} />
                          {macPending && (
                            <span>
                              {t(
                                "containerResourcesPanel.waitingForTheMACAddress",
                              )}
                            </span>
                          )}
                        </>
                      }
                      open={openRows.has(key)}
                      onToggle={() => {
                        toggleRow(key);
                      }}
                      actions={
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                          onClick={() => {
                            setDeleteNics((prev) => new Set(prev).add(key));
                          }}
                          title={t("containerResourcesPanel.removeNIC")}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      }
                      editor={
                        <NICFields
                          nic={nic}
                          onChange={(updated) => {
                            setNics((prev) => {
                              const next = new Map(prev);
                              next.set(key, updated);
                              return next;
                            });
                          }}
                          bridges={bridgeList}
                          macPending={macPending}
                        />
                      }
                    />
                  );
                })}

              {/* Pending new NICs: open, as each still has to be filled in. */}
              {Array.from(pendingNics.entries())
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([key, nic]) => (
                  <DeviceRow
                    key={key}
                    name={key}
                    icon={Network}
                    title={t("containerResourcesPanel.networkInterface")}
                    marker={
                      <PendingBadge>
                        {t("containerResourcesPanel.addedOnSave")}
                      </PendingBadge>
                    }
                    summary={<NICSummary nic={nic} />}
                    actions={
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 px-2"
                        onClick={() => {
                          setPendingNics((prev) => {
                            const next = new Map(prev);
                            next.delete(key);
                            return next;
                          });
                        }}
                      >
                        {t("containerResourcesPanel.cancel")}
                      </Button>
                    }
                    editor={
                      <NICFields
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
                    }
                  />
                ))}
            </DeviceList>
          </Panel>
        </div>

        <div className="flex min-w-0 flex-[2_1_340px] flex-col gap-4">
          <Panel flush title={t("containerResourcesPanel.options")}>
            <GroupList>
              <Group title={t("containerResourcesPanel.identity")}>
                <FieldGrid>
                  <Field
                    label={t("containerResourcesPanel.hostname")}
                    className="col-span-2"
                  >
                    <Input
                      value={hostname}
                      onChange={(e) => {
                        setHostname(e.target.value);
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.consoleMode")}>
                    <select
                      className={fieldSelectClass}
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
                  </Field>
                </FieldGrid>
              </Group>

              <Group
                title={t("containerResourcesPanel.startup")}
                summary={
                  onboot
                    ? t("containerResourcesPanel.startsWithTheNode")
                    : t("containerResourcesPanel.startedByHand")
                }
              >
                <Checks>
                  <Check
                    label={t("containerResourcesPanel.startAtBoot")}
                    checked={onboot}
                    onCheckedChange={(v) => {
                      setOnboot(v === true);
                    }}
                  />
                  <Check
                    label={t("containerResourcesPanel.protection")}
                    checked={protection}
                    onCheckedChange={(v) => {
                      setProtection(v === true);
                    }}
                  />
                </Checks>
                <FieldGrid>
                  <Field label={t("containerResourcesPanel.startupOrder")}>
                    <Input
                      type="number"
                      min={0}
                      value={startup.order}
                      onChange={(e) => {
                        setStartup((s) => ({ ...s, order: e.target.value }));
                      }}
                      placeholder="—"
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.upDelayS")}>
                    <Input
                      type="number"
                      min={0}
                      value={startup.up}
                      onChange={(e) => {
                        setStartup((s) => ({ ...s, up: e.target.value }));
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.downDelayS")}>
                    <Input
                      type="number"
                      min={0}
                      value={startup.down}
                      onChange={(e) => {
                        setStartup((s) => ({ ...s, down: e.target.value }));
                      }}
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
              </Group>

              <Group title={t("containerResourcesPanel.dns")}>
                <FieldGrid>
                  <Field label={t("containerResourcesPanel.nameserver")}>
                    <Input
                      value={nameserver}
                      onChange={(e) => {
                        setNameserver(e.target.value);
                      }}
                      placeholder={t("containerResourcesPanel.hostDefault")}
                      className={fieldInputClass}
                    />
                  </Field>
                  <Field label={t("containerResourcesPanel.searchDomain")}>
                    <Input
                      value={searchdomain}
                      onChange={(e) => {
                        setSearchdomain(e.target.value);
                      }}
                      placeholder={t("containerResourcesPanel.hostDefault")}
                      className={fieldInputClass}
                    />
                  </Field>
                </FieldGrid>
              </Group>
            </GroupList>
          </Panel>

          {/* Features. check_ct_modify_config_perm (pve-container
              src/PVE/LXC.pm) lets nobody but root@pam change a privileged
              container's features, or any but nesting on an unprivileged one,
              and Nexara connects with an API token, which never is root@pam. So
              those show what is set and say why they are locked: a change to
              one would fail the whole Save. */}
          <Panel
            title={t("containerResourcesPanel.features")}
            aside={
              unprivileged
                ? t("containerResourcesPanel.unprivileged")
                : t("containerResourcesPanel.privileged")
            }
          >
            <div className="space-y-3">
              <Checks>
                <Check
                  id="ct-feature-nesting"
                  label={t("containerResourcesPanel.nesting")}
                  checked={features.nesting}
                  disabled={!unprivileged}
                  onCheckedChange={(v) => {
                    setFeatures((f) => ({ ...f, nesting: v === true }));
                  }}
                />
                <Check
                  id="ct-feature-fuse"
                  label={t("containerResourcesPanel.fuse")}
                  checked={features.fuse}
                  disabled
                />
                <Check
                  id="ct-feature-keyctl"
                  label={t("containerResourcesPanel.keyctl")}
                  checked={features.keyctl}
                  disabled
                />
                <Check
                  id="ct-feature-mknod"
                  label={t("containerResourcesPanel.mknod")}
                  checked={features.mknod}
                  disabled
                />
              </Checks>
              <div className="space-y-1">
                <Label
                  htmlFor="ct-feature-mount"
                  className="block text-[12.5px] font-normal text-muted-foreground"
                >
                  {t("containerResourcesPanel.allowedMountTypes")}
                </Label>
                <Input
                  id="ct-feature-mount"
                  value={features.mount}
                  disabled
                  className={fieldInputClass}
                />
              </div>
              <p className="text-[12.5px] text-muted-foreground">
                {unprivileged
                  ? t("containerResourcesPanel.proxmoxLetsOnlyRootPam")
                  : t("containerResourcesPanel.onAPrivilegedContainerProxmox")}
              </p>
            </div>
          </Panel>

          <Panel title={t("containerResourcesPanel.descriptionAndTags")}>
            <div className="space-y-3">
              <Field label={t("containerResourcesPanel.description")}>
                <textarea
                  value={description}
                  onChange={(e) => {
                    setDescription(e.target.value);
                  }}
                  rows={3}
                  className="flex w-full rounded-md border border-input bg-transparent px-2.5 py-1.5 text-sm placeholder:text-muted-foreground focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                />
              </Field>
              <Field label={t("containerResourcesPanel.tags")}>
                <Input
                  value={tags}
                  onChange={(e) => {
                    setTags(e.target.value);
                  }}
                  placeholder="tag1;tag2"
                  className={fieldInputClass}
                />
              </Field>
            </div>
          </Panel>
        </div>
      </div>

      <SaveBar
        ref={saveBarRef}
        dirty={hasChanges}
        dirtyLabel={t("containerResourcesPanel.changesToSave", {
          changes: plural(changeCount, "change"),
        })}
        saving={setConfigMutation.isPending}
        disabled={!hasChanges || memoryMissing || setConfigMutation.isPending}
        onSave={() => {
          saveChanges({ confirmed: null });
        }}
        hideIdle={
          setConfigMutation.isSuccess ||
          setConfigMutation.isError ||
          saveNotice !== null
        }
      >
        {setConfigMutation.isSuccess && (
          <span className="text-muted-foreground">
            {t("containerResourcesPanel.saved")}
          </span>
        )}
        {setConfigMutation.isError && (
          <span className="text-destructive">
            {setConfigMutation.error.message}
          </span>
        )}
        {saveNotice !== null && (
          <span role="status" className="text-amber-700 dark:text-amber-400">
            {saveNotice}
          </span>
        )}
        {/* Said here too: the Memory field may be a screen away from Save. */}
        {memoryMissing && (
          <span className="text-destructive">
            {t("containerResourcesPanel.memoryIsEmptySoSave")}
          </span>
        )}
      </SaveBar>
    </div>
  );
}
