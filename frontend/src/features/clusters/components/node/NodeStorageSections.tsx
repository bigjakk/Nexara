import { useState, type ReactNode } from "react";
import { Plus, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes } from "@/lib/format";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import {
  useNodeZFSPools,
  useNodeLVM,
  useNodeLVMThin,
  type ZFSPoolResponse,
  type LVMVolumeGroupResponse,
  type LVMThinPoolResponse,
  type DirectoryEntryResponse,
  type LiveDiskResponse,
  useLiveDisks,
  useCreateZFSPool,
  useDeleteZFSPool,
  useCreateLVM,
  useDeleteLVM,
  useCreateLVMThin,
  useDeleteLVMThin,
  useNodeDirectories,
  useCreateDirectory,
} from "../../api/cluster-queries";
import { NoValue, StateText } from "./StateText";

const SELECT_CLASS =
  "h-8 w-full rounded-md border border-input bg-background px-2 text-sm";

/**
 * What the four create forms show where their disk picker would be. An empty
 * picker is the same picture whether the node has no spare disks or Nexara
 * could not ask it, so the distinction has to come from the query.
 */
function UnusedDisksNotice({
  query,
  className,
}: {
  query: Parameters<typeof QueryStateNotice>[0]["query"];
  className?: string;
}) {
  return (
    <QueryStateNotice
      query={query}
      subject="this node's disks"
      empty="No unused disks available."
      skeletonClassName={className ?? "h-8 w-full"}
    />
  );
}

function diskLabel(d: LiveDiskResponse): string {
  const size = formatBytes(d.size);
  const model = d.model ? ` - ${d.model}` : "";
  const used = d.used ? ` [${d.used}]` : " [unused]";
  return `${d.dev_path} (${size}${model})${used}`;
}

/** "3 pools", or nothing to say when there are none (or none read yet). */
function countOf(
  items: readonly unknown[] | undefined,
  one: string,
  many: string,
): string | undefined {
  const n = items?.length ?? 0;
  return n === 0 ? undefined : `${String(n)} ${n === 1 ? one : many}`;
}

/**
 * One kind of local storage as a panel: its Create button in the title row,
 * the create form under it while open, then the table or what stands in for
 * one.
 */
function StoragePanel({
  title,
  count,
  createLabel,
  showCreate,
  onToggleCreate,
  form,
  children,
}: {
  title: string;
  /** How many there are, in words, once known and above zero. */
  count: string | undefined;
  createLabel: string;
  showCreate: boolean;
  onToggleCreate: () => void;
  form: ReactNode;
  children: ReactNode;
}) {
  return (
    <Panel
      flush
      title={title}
      aside={count}
      actions={
        <Button
          variant="outline"
          size="sm"
          className="gap-1.5"
          aria-expanded={showCreate}
          onClick={onToggleCreate}
        >
          <Plus className="h-4 w-4" />
          {createLabel}
        </Button>
      }
    >
      {showCreate && (
        <div className="mb-3 space-y-3 border-y bg-foreground/[0.025] px-[18px] py-4">
          {form}
        </div>
      )}
      {children}
    </Panel>
  );
}

/** A storage panel's body when it has no table to show. */
function Gutter({ children }: { children: ReactNode }) {
  return <div className="px-[18px] pb-1">{children}</div>;
}

function FormActions({
  onCancel,
  onCreate,
  disabled,
}: {
  onCancel: () => void;
  onCreate: () => void;
  disabled: boolean;
}) {
  return (
    <div className="flex justify-end gap-2">
      <Button variant="outline" size="sm" onClick={onCancel}>
        Cancel
      </Button>
      <Button size="sm" onClick={onCreate} disabled={disabled}>
        Create
      </Button>
    </div>
  );
}

/** The two things a destroy can also clean up, as its dialog offers them. */
function CleanupOptions({
  disks,
  onDisksChange,
  config,
  onConfigChange,
}: {
  disks: boolean;
  onDisksChange: (checked: boolean) => void;
  config: boolean;
  onConfigChange: (checked: boolean) => void;
}) {
  return (
    <div className="space-y-2 py-2">
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={disks}
          onChange={(e) => {
            onDisksChange(e.target.checked);
          }}
          className="rounded border"
        />
        Clean up disks
      </label>
      <p className="ml-6 text-xs text-muted-foreground">
        Wipe partition tables of member disks
      </p>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={config}
          onChange={(e) => {
            onConfigChange(e.target.checked);
          }}
          className="rounded border"
        />
        Clean up storage configuration
      </label>
      <p className="ml-6 text-xs text-muted-foreground">
        Remove associated storage from Proxmox configuration
      </p>
    </div>
  );
}

/** The trash button that opens a row's destroy dialog. */
function DestroyTrigger({ label }: { label: string }) {
  return (
    <AlertDialogTrigger asChild>
      <Button
        aria-label={label}
        variant="ghost"
        size="icon"
        className="h-7 w-7 text-muted-foreground hover:text-destructive"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </AlertDialogTrigger>
  );
}

export function ZFSPoolsSection({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const { t } = useTranslation("clusters");
  const poolsQuery = useNodeZFSPools(clusterId, nodeName);
  const pools = poolsQuery.data;
  const liveDisksQuery = useLiveDisks(clusterId, nodeName);
  const liveDisks = liveDisksQuery.data;
  const [showCreate, setShowCreate] = useState(false);
  const [zfsName, setZfsName] = useState("");
  const [zfsRaid, setZfsRaid] = useState("single");
  const [zfsSelectedDisks, setZfsSelectedDisks] = useState<string[]>([]);
  const [zfsCompression, setZfsCompression] = useState("on");
  const createZFS = useCreateZFSPool(clusterId, nodeName);
  const deleteZFS = useDeleteZFSPool(clusterId, nodeName);
  // Which row's Destroy dialog is open: each row has its own, over one
  // mutation, so a failure that lands after it closed is toasted instead.
  const [destroying, setDestroying] = useState<string | null>(null);
  const settle = useSaveOutcome(destroying);
  const deleteError =
    deleteZFS.error instanceof Error ? deleteZFS.error.message : "";
  const [cleanupDisks, setCleanupDisks] = useState(true);
  const [cleanupConfig, setCleanupConfig] = useState(true);

  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const toggleDisk = (devPath: string) => {
    setZfsSelectedDisks((prev) =>
      prev.includes(devPath)
        ? prev.filter((p) => p !== devPath)
        : [...prev, devPath],
    );
  };

  const handleCreate = () => {
    createZFS.mutate(
      {
        name: zfsName,
        raidlevel: zfsRaid,
        devices: zfsSelectedDisks.join(","),
        compression: zfsCompression,
      },
      {
        onSuccess: () => {
          setShowCreate(false);
          setZfsName("");
          setZfsSelectedDisks([]);
        },
      },
    );
  };

  return (
    <StoragePanel
      title="ZFS pools"
      count={countOf(pools, "pool", "pools")}
      createLabel="Create ZFS"
      showCreate={showCreate}
      onToggleCreate={() => {
        setShowCreate(!showCreate);
      }}
      form={
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs">Pool name</Label>
              <Input
                className="h-8 text-sm"
                value={zfsName}
                onChange={(e) => {
                  setZfsName(e.target.value);
                }}
                placeholder="e.g. mypool"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">RAID level</Label>
              <select
                className={SELECT_CLASS}
                value={zfsRaid}
                onChange={(e) => {
                  setZfsRaid(e.target.value);
                }}
              >
                <option value="single">Single</option>
                <option value="mirror">Mirror</option>
                <option value="raidz">RAIDZ</option>
                <option value="raidz2">RAIDZ2</option>
                <option value="raidz3">RAIDZ3</option>
              </select>
            </div>
            <div className="space-y-1 sm:col-span-2">
              <Label className="text-xs">
                Select disks ({zfsSelectedDisks.length} selected)
              </Label>
              {unusedDisks.length === 0 ? (
                <UnusedDisksNotice query={liveDisksQuery} />
              ) : (
                <div className="max-h-40 space-y-1 overflow-auto rounded-md border border-input p-2">
                  {unusedDisks.map((d) => (
                    <label
                      key={d.dev_path}
                      className="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 text-xs hover:bg-foreground/[0.05]"
                    >
                      <input
                        type="checkbox"
                        checked={zfsSelectedDisks.includes(d.dev_path)}
                        onChange={() => {
                          toggleDisk(d.dev_path);
                        }}
                        className="rounded border"
                      />
                      <span className="font-mono">{d.dev_path}</span>
                      <span className="text-muted-foreground">
                        {formatBytes(d.size)}
                      </span>
                      <span className="text-muted-foreground">
                        {d.model || ""}
                      </span>
                      <span className="text-muted-foreground">
                        {d.disk_type.toUpperCase()}
                      </span>
                    </label>
                  ))}
                </div>
              )}
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Compression</Label>
              <select
                className={SELECT_CLASS}
                value={zfsCompression}
                onChange={(e) => {
                  setZfsCompression(e.target.value);
                }}
              >
                <option value="on">On (LZ4)</option>
                <option value="off">Off</option>
                <option value="lz4">LZ4</option>
                <option value="zstd">ZSTD</option>
                <option value="gzip">GZIP</option>
              </select>
            </div>
          </div>
          <FormActions
            onCancel={() => {
              setShowCreate(false);
            }}
            onCreate={handleCreate}
            disabled={
              !zfsName || zfsSelectedDisks.length === 0 || createZFS.isPending
            }
          />
        </>
      }
    >
      {!pools || pools.length === 0 ? (
        <Gutter>
          <QueryStateNotice
            query={poolsQuery}
            subject="this node's ZFS pools"
            empty="No ZFS pools found."
          />
        </Gutter>
      ) : (
        <Table className="whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Allocated</TableHead>
              <TableHead>Free</TableHead>
              <TableHead>Fragmentation</TableHead>
              <TableHead>Health</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {pools.map((p: ZFSPoolResponse) => (
              <TableRow key={p.name}>
                <TableCell className="font-mono text-[13px]">
                  {p.name}
                </TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(p.size)}
                </TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(p.alloc)}
                </TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(p.free)}
                </TableCell>
                <TableCell className="tabular-nums">{p.frag}%</TableCell>
                <TableCell>
                  <StateText tone={p.health === "ONLINE" ? "ok" : "bad"}>
                    {p.health === "ONLINE" ? "Online" : p.health}
                  </StateText>
                </TableCell>
                <TableCell className="text-right">
                  <AlertDialog
                    onOpenChange={(open) => {
                      setDestroying(open ? p.name : null);
                    }}
                  >
                    <DestroyTrigger label={`Destroy pool ${p.name}`} />
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>Destroy ZFS pool</AlertDialogTitle>
                        <AlertDialogDescription>
                          Are you sure you want to destroy{" "}
                          <span className="font-mono font-semibold">
                            {p.name}
                          </span>
                          ? This will permanently delete the pool and all data
                          on it. This action cannot be undone.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <CleanupOptions
                        disks={cleanupDisks}
                        onDisksChange={setCleanupDisks}
                        config={cleanupConfig}
                        onConfigChange={setCleanupConfig}
                      />
                      {deleteError && (
                        <p className="text-sm text-destructive">
                          {deleteError}
                        </p>
                      )}
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction
                          className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                          disabled={deleteZFS.isPending}
                          onClick={(e) => {
                            e.preventDefault();
                            settle(
                              deleteZFS.mutateAsync({
                                poolName: p.name,
                                cleanupDisks,
                                cleanupConfig,
                              }),
                              {
                                action: t(
                                  "nodeStorageSectionsActions.destroyingZFSPoolName",
                                  { name: p.name },
                                ),
                                onError: () => undefined, // shown above from the mutation's state
                              },
                            );
                          }}
                        >
                          {deleteZFS.isPending ? "Destroying…" : "Destroy"}
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </StoragePanel>
  );
}

/** The name, disk and "add as storage" fields the three single-disk forms share. */
function SingleDiskFields({
  nameLabel,
  namePlaceholder,
  name,
  onNameChange,
  device,
  onDeviceChange,
  unusedDisks,
  liveDisksQuery,
  children,
}: {
  nameLabel: string;
  namePlaceholder: string;
  name: string;
  onNameChange: (value: string) => void;
  device: string;
  onDeviceChange: (value: string) => void;
  unusedDisks: LiveDiskResponse[];
  liveDisksQuery: Parameters<typeof QueryStateNotice>[0]["query"];
  /** Further fields of the same grid. */
  children?: ReactNode;
}) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <div className="space-y-1">
        <Label className="text-xs">{nameLabel}</Label>
        <Input
          className="h-8 text-sm"
          value={name}
          onChange={(e) => {
            onNameChange(e.target.value);
          }}
          placeholder={namePlaceholder}
        />
      </div>
      <div className="space-y-1">
        <Label className="text-xs">Disk</Label>
        <select
          className={SELECT_CLASS}
          value={device}
          onChange={(e) => {
            onDeviceChange(e.target.value);
          }}
        >
          <option value="">Select a disk...</option>
          {unusedDisks.map((d) => (
            <option key={d.dev_path} value={d.dev_path}>
              {diskLabel(d)}
            </option>
          ))}
        </select>
        {unusedDisks.length === 0 && (
          <UnusedDisksNotice query={liveDisksQuery} className="h-4 w-40" />
        )}
      </div>
      {children}
    </div>
  );
}

function AddStorageCheckbox({
  id,
  checked,
  onChange,
}: {
  id: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <input
        type="checkbox"
        id={id}
        checked={checked}
        onChange={(e) => {
          onChange(e.target.checked);
        }}
        className="rounded border"
      />
      <Label htmlFor={id} className="text-xs">
        Add as Proxmox storage
      </Label>
    </div>
  );
}

export function LVMSection({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const { t } = useTranslation("clusters");
  const vgsQuery = useNodeLVM(clusterId, nodeName);
  const vgs = vgsQuery.data;
  const liveDisksQuery = useLiveDisks(clusterId, nodeName);
  const liveDisks = liveDisksQuery.data;
  const [showCreate, setShowCreate] = useState(false);
  const [lvmName, setLvmName] = useState("");
  const [lvmDevice, setLvmDevice] = useState("");
  const [lvmAddStorage, setLvmAddStorage] = useState(true);
  const createLVM = useCreateLVM(clusterId, nodeName);
  const deleteLVM = useDeleteLVM(clusterId, nodeName);
  // Which row's Destroy dialog is open: each row has its own, over one
  // mutation, so a failure that lands after it closed is toasted instead.
  const [destroying, setDestroying] = useState<string | null>(null);
  const settle = useSaveOutcome(destroying);
  const deleteError =
    deleteLVM.error instanceof Error ? deleteLVM.error.message : "";
  const [lvmCleanupDisks, setLvmCleanupDisks] = useState(true);
  const [lvmCleanupConfig, setLvmCleanupConfig] = useState(true);
  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const handleCreate = () => {
    createLVM.mutate(
      { name: lvmName, device: lvmDevice, add_storage: lvmAddStorage },
      {
        onSuccess: () => {
          setShowCreate(false);
          setLvmName("");
          setLvmDevice("");
        },
      },
    );
  };

  return (
    <StoragePanel
      title="LVM volume groups"
      count={countOf(vgs, "volume group", "volume groups")}
      createLabel="Create LVM"
      showCreate={showCreate}
      onToggleCreate={() => {
        setShowCreate(!showCreate);
      }}
      form={
        <>
          <SingleDiskFields
            nameLabel="Volume group name"
            namePlaceholder="e.g. myvg"
            name={lvmName}
            onNameChange={setLvmName}
            device={lvmDevice}
            onDeviceChange={setLvmDevice}
            unusedDisks={unusedDisks}
            liveDisksQuery={liveDisksQuery}
          />
          <AddStorageCheckbox
            id="lvm-add-storage"
            checked={lvmAddStorage}
            onChange={setLvmAddStorage}
          />
          <FormActions
            onCancel={() => {
              setShowCreate(false);
            }}
            onCreate={handleCreate}
            disabled={!lvmName || !lvmDevice || createLVM.isPending}
          />
        </>
      }
    >
      {!vgs || vgs.length === 0 ? (
        <Gutter>
          <QueryStateNotice
            query={vgsQuery}
            subject="this node's LVM volume groups"
            empty="No LVM volume groups found."
          />
        </Gutter>
      ) : (
        <Table className="whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Free</TableHead>
              <TableHead>Physical volumes</TableHead>
              <TableHead>Logical volumes</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {vgs.map((vg: LVMVolumeGroupResponse) => (
              <TableRow key={vg.name}>
                <TableCell className="font-mono text-[13px]">
                  {vg.name}
                </TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(vg.size)}
                </TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(vg.free)}
                </TableCell>
                <TableCell className="tabular-nums">{vg.pv_count}</TableCell>
                <TableCell className="tabular-nums">{vg.lv_count}</TableCell>
                <TableCell className="text-right">
                  <AlertDialog
                    onOpenChange={(open) => {
                      setDestroying(open ? vg.name : null);
                    }}
                  >
                    <DestroyTrigger label={`Destroy volume group ${vg.name}`} />
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          Destroy LVM volume group
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          Are you sure you want to destroy{" "}
                          <span className="font-mono font-semibold">
                            {vg.name}
                          </span>
                          ? This will permanently delete the volume group and
                          all data on it. This action cannot be undone.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <CleanupOptions
                        disks={lvmCleanupDisks}
                        onDisksChange={setLvmCleanupDisks}
                        config={lvmCleanupConfig}
                        onConfigChange={setLvmCleanupConfig}
                      />
                      {deleteError && (
                        <p className="text-sm text-destructive">
                          {deleteError}
                        </p>
                      )}
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction
                          className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                          disabled={deleteLVM.isPending}
                          onClick={(e) => {
                            e.preventDefault();
                            settle(
                              deleteLVM.mutateAsync({
                                name: vg.name,
                                cleanupDisks: lvmCleanupDisks,
                                cleanupConfig: lvmCleanupConfig,
                              }),
                              {
                                action: t(
                                  "nodeStorageSectionsActions.destroyingVolumeGroupName",
                                  { name: vg.name },
                                ),
                                onError: () => undefined, // shown above from the mutation's state
                              },
                            );
                          }}
                        >
                          {deleteLVM.isPending ? "Destroying…" : "Destroy"}
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </StoragePanel>
  );
}

export function LVMThinSection({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const { t } = useTranslation("clusters");
  const poolsQuery = useNodeLVMThin(clusterId, nodeName);
  const pools = poolsQuery.data;
  const liveDisksQuery = useLiveDisks(clusterId, nodeName);
  const liveDisks = liveDisksQuery.data;
  const [showCreate, setShowCreate] = useState(false);
  const [thinName, setThinName] = useState("");
  const [thinDevice, setThinDevice] = useState("");
  const [thinAddStorage, setThinAddStorage] = useState(true);
  const createThin = useCreateLVMThin(clusterId, nodeName);
  const deleteThin = useDeleteLVMThin(clusterId, nodeName);
  // Which row's Destroy dialog is open: each row has its own, over one
  // mutation, so a failure that lands after it closed is toasted instead.
  const [destroying, setDestroying] = useState<string | null>(null);
  const settle = useSaveOutcome(destroying);
  const thinDeleteError =
    deleteThin.error instanceof Error ? deleteThin.error.message : "";
  const [thinCleanupDisks, setThinCleanupDisks] = useState(true);
  const [thinCleanupConfig, setThinCleanupConfig] = useState(true);
  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const handleCreate = () => {
    createThin.mutate(
      { name: thinName, device: thinDevice, add_storage: thinAddStorage },
      {
        onSuccess: () => {
          setShowCreate(false);
          setThinName("");
          setThinDevice("");
        },
      },
    );
  };

  return (
    <StoragePanel
      title="LVM-thin pools"
      count={countOf(pools, "pool", "pools")}
      createLabel="Create LVM-thin"
      showCreate={showCreate}
      onToggleCreate={() => {
        setShowCreate(!showCreate);
      }}
      form={
        <>
          <SingleDiskFields
            nameLabel="Thin pool name"
            namePlaceholder="e.g. mythinpool"
            name={thinName}
            onNameChange={setThinName}
            device={thinDevice}
            onDeviceChange={setThinDevice}
            unusedDisks={unusedDisks}
            liveDisksQuery={liveDisksQuery}
          />
          <AddStorageCheckbox
            id="thin-add-storage"
            checked={thinAddStorage}
            onChange={setThinAddStorage}
          />
          <FormActions
            onCancel={() => {
              setShowCreate(false);
            }}
            onCreate={handleCreate}
            disabled={!thinName || !thinDevice || createThin.isPending}
          />
        </>
      }
    >
      {!pools || pools.length === 0 ? (
        <Gutter>
          <QueryStateNotice
            query={poolsQuery}
            subject="this node's LVM thin pools"
            empty="No LVM thin pools found."
          />
        </Gutter>
      ) : (
        <Table className="whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>Logical volume</TableHead>
              <TableHead>Volume group</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Used</TableHead>
              <TableHead>Data</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {pools.map((p: LVMThinPoolResponse) => (
              <TableRow key={`${p.vg}-${p.lv}`}>
                <TableCell className="font-mono text-[13px]">{p.lv}</TableCell>
                <TableCell className="font-mono text-[13px]">{p.vg}</TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(p.lv_size)}
                </TableCell>
                <TableCell className="tabular-nums">
                  {formatBytes(p.used)}
                </TableCell>
                <TableCell className="tabular-nums">
                  {p.data_percent.toFixed(1)}%
                </TableCell>
                <TableCell className="text-right">
                  <AlertDialog
                    onOpenChange={(open) => {
                      setDestroying(open ? `${p.vg}/${p.lv}` : null);
                    }}
                  >
                    <DestroyTrigger
                      label={`Destroy thin pool ${p.vg}/${p.lv}`}
                    />
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          Destroy LVM-thin pool
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          Are you sure you want to destroy{" "}
                          <span className="font-mono font-semibold">
                            {p.vg}/{p.lv}
                          </span>
                          ? This will permanently delete the thin pool and all
                          data on it. This action cannot be undone.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <CleanupOptions
                        disks={thinCleanupDisks}
                        onDisksChange={setThinCleanupDisks}
                        config={thinCleanupConfig}
                        onConfigChange={setThinCleanupConfig}
                      />
                      {thinDeleteError && (
                        <p className="text-sm text-destructive">
                          {thinDeleteError}
                        </p>
                      )}
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction
                          className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                          disabled={deleteThin.isPending}
                          onClick={(e) => {
                            e.preventDefault();
                            settle(
                              deleteThin.mutateAsync({
                                lv: p.lv,
                                vg: p.vg,
                                cleanupDisks: thinCleanupDisks,
                                cleanupConfig: thinCleanupConfig,
                              }),
                              {
                                action: t(
                                  "nodeStorageSectionsActions.destroyingThinPoolVgLv",
                                  { vg: p.vg, lv: p.lv },
                                ),
                                onError: () => undefined, // shown above from the mutation's state
                              },
                            );
                          }}
                        >
                          {deleteThin.isPending ? "Destroying…" : "Destroy"}
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </StoragePanel>
  );
}

export function DirectorySection({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const dirsQuery = useNodeDirectories(clusterId, nodeName);
  const dirs = dirsQuery.data;
  const liveDisksQuery = useLiveDisks(clusterId, nodeName);
  const liveDisks = liveDisksQuery.data;
  const [showCreate, setShowCreate] = useState(false);
  const [dirName, setDirName] = useState("");
  const [dirDevice, setDirDevice] = useState("");
  const [dirFs, setDirFs] = useState("ext4");
  const [dirAddStorage, setDirAddStorage] = useState(true);
  const createDir = useCreateDirectory(clusterId, nodeName);
  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const handleCreate = () => {
    createDir.mutate(
      {
        name: dirName,
        device: dirDevice,
        filesystem: dirFs,
        add_storage: dirAddStorage,
      },
      {
        onSuccess: () => {
          setShowCreate(false);
          setDirName("");
          setDirDevice("");
        },
      },
    );
  };

  return (
    <StoragePanel
      title="Directory storage"
      count={countOf(dirs, "entry", "entries")}
      createLabel="Create directory"
      showCreate={showCreate}
      onToggleCreate={() => {
        setShowCreate(!showCreate);
      }}
      form={
        <>
          <SingleDiskFields
            nameLabel="Name"
            namePlaceholder="e.g. mydir"
            name={dirName}
            onNameChange={setDirName}
            device={dirDevice}
            onDeviceChange={setDirDevice}
            unusedDisks={unusedDisks}
            liveDisksQuery={liveDisksQuery}
          >
            <div className="space-y-1">
              <Label className="text-xs">Filesystem</Label>
              <select
                className={SELECT_CLASS}
                value={dirFs}
                onChange={(e) => {
                  setDirFs(e.target.value);
                }}
              >
                <option value="ext4">ext4</option>
                <option value="xfs">XFS</option>
              </select>
            </div>
          </SingleDiskFields>
          <AddStorageCheckbox
            id="dir-add-storage"
            checked={dirAddStorage}
            onChange={setDirAddStorage}
          />
          <FormActions
            onCancel={() => {
              setShowCreate(false);
            }}
            onCreate={handleCreate}
            disabled={!dirName || !dirDevice || createDir.isPending}
          />
        </>
      }
    >
      {!dirs || dirs.length === 0 ? (
        <Gutter>
          <QueryStateNotice
            query={dirsQuery}
            subject="this node's directory storage entries"
            empty="No directory storage entries found."
          />
        </Gutter>
      ) : (
        <Table className="whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>Path</TableHead>
              <TableHead>Device</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Options</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {dirs.map((d: DirectoryEntryResponse) => (
              <TableRow key={d.path}>
                <TableCell className="font-mono text-[13px]">
                  {d.path}
                </TableCell>
                <TableCell className="font-mono text-[12.5px]">
                  {d.device}
                </TableCell>
                <TableCell>{d.type}</TableCell>
                <TableCell className="text-muted-foreground">
                  {d.options || <NoValue />}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </StoragePanel>
  );
}
