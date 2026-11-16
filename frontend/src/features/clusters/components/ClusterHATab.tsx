import { useState, useMemo } from "react";
import { Badge } from "@/components/ui/badge";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { Segmented } from "@/components/ui/segmented";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import { AlertTriangle, Pencil, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  useHAResources,
  useHAGroups,
  useHAStatus,
  useHARules,
  useHAManagerStatus,
  useUpdateHAResource,
  useDeleteHAResource,
  useCreateHAGroup,
  useUpdateHAGroup,
  useDeleteHAGroup,
  useUpdateHARule,
  useDeleteHARule,
  HA_RESOURCE_DEFAULTS,
  haResourceHasFailback,
  type HAStatusEntry,
  type HAResource,
  type HAGroup,
  type HARuleEntry,
} from "@/features/ha/api/ha-queries";
import { HAResourceForm } from "@/features/ha/components/HAResourceForm";
import { HARuleForm } from "@/features/ha/components/HARuleForm";
import { HAMaintenanceCard } from "@/features/ha/components/HAMaintenanceCard";
import { useClusterVMs, useClusterNodes } from "../api/cluster-queries";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import type { VMResponse } from "@/types/api";
import {
  isPVEAtLeast,
  isPVEVersionKnown,
  PVE_FEATURES,
} from "@/lib/pve-version";
import { DateTime } from "@/components/DateTime";

interface ClusterHATabProps {
  clusterId: string;
  pveVersion: string;
}

function ErrorBanner({ error }: { error: Error }) {
  const message = error.message || "Failed to load data";
  const isForbidden =
    message.includes("403") ||
    message.toLowerCase().includes("permission") ||
    message.toLowerCase().includes("forbidden");
  return (
    <p className="flex items-start gap-2 text-sm text-amber-700 dark:text-amber-400">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <span>
        {isForbidden
          ? "Permission denied. You may need to log out and back in for new RBAC permissions to take effect."
          : message}
      </span>
    </p>
  );
}

function statusBadge(status: string) {
  switch (status) {
    case "active":
    case "started":
    case "online":
      return <Badge variant="healthy">{status}</Badge>;
    case "stopped":
    case "disabled":
    case "offline":
      return <Badge variant="secondary">{status}</Badge>;
    case "error":
    case "fence":
    case "recovery":
      return <Badge variant="destructive">{status}</Badge>;
    case "ignored":
      return <Badge variant="outline">{status}</Badge>;
    default:
      return <Badge variant="secondary">{status}</Badge>;
  }
}

/** Categorize HA status entries by their type field.
 *  Proxmox returns: master, quorum, lrm (per-node), service */
function categorizeStatus(entries: HAStatusEntry[]) {
  let manager: HAStatusEntry | undefined;
  let quorum: HAStatusEntry | undefined;
  const nodes: HAStatusEntry[] = [];
  const services: HAStatusEntry[] = [];

  for (const e of entries) {
    switch (e.type) {
      case "master":
        manager = e;
        break;
      case "quorum":
        quorum = e;
        break;
      case "lrm":
        nodes.push(e);
        break;
      case "service":
        services.push(e);
        break;
      default:
        if (e.sid) services.push(e);
        break;
    }
  }

  return { manager, quorum, nodes, services };
}

const HA_STATES = ["started", "stopped", "disabled", "ignored"] as const;

/** The views of the tab, in the order the switch shows them. */
const HA_VIEWS = [
  { value: "status", label: "Status" },
  { value: "resources", label: "Resources" },
  { value: "groups", label: "Groups / Rules" },
] as const;

/** A row's icon buttons: quiet until pointed at, the delete red only then. */
const ROW_ACTION = "text-muted-foreground hover:text-foreground";
const ROW_DELETE = "text-muted-foreground hover:text-destructive";

function vmToSID(vm: VMResponse): string {
  return `${vm.type === "lxc" ? "ct" : "vm"}:${String(vm.vmid)}`;
}

export function ClusterHATab({ clusterId, pveVersion }: ClusterHATabProps) {
  const { canManage } = useAuth();
  // PVE 9 deprecated HA groups in favor of node-affinity rules and soft-disables
  // the groups write API once migrated. Gate group creation/editing accordingly.
  const groupsDeprecated = isPVEAtLeast(pveVersion, PVE_FEATURES.HA_RULES);
  // The version is "" until the cluster query resolves, and on a cluster that
  // never synced. That is its own state, not PVE 8: read as PVE 8 it offered
  // group writes a migrated PVE 9 cluster refuses, and told a PVE 9 cluster it
  // was too old for rules. Group writes and that message wait for a version.
  const versionKnown = isPVEVersionKnown(pveVersion);
  // Group create and edit exist only before PVE 9 migrated groups to rules.
  const groupWritesAllowed = versionKnown && !groupsDeprecated;
  // The same boundary from the other side: a resource has a failback of its
  // own only from PVE 9, where it took over from the group's nofailback.
  const resourceFailback = haResourceHasFailback(pveVersion);
  const resourcesQuery = useHAResources(clusterId);
  const groupsQuery = useHAGroups(clusterId);
  const rulesQuery = useHARules(clusterId);
  const statusQuery = useHAStatus(clusterId);
  const managerStatusQuery = useHAManagerStatus(clusterId);
  const vmsQuery = useClusterVMs(clusterId);
  const nodesQuery = useClusterNodes(clusterId);

  const updateResource = useUpdateHAResource(clusterId);
  const deleteResource = useDeleteHAResource(clusterId);
  const createGroup = useCreateHAGroup(clusterId);
  const updateGroup = useUpdateHAGroup(clusterId);
  const deleteGroup = useDeleteHAGroup(clusterId);
  const updateRule = useUpdateHARule(clusterId);
  const deleteRule = useDeleteHARule(clusterId);

  const [mutationError, setMutationError] = useState<string | null>(null);

  // The panels are Radix tab panels, so only the view on show is mounted;
  // the switch above them is what picks it.
  const [view, setView] = useState("status");
  const history = clusterHistories.ha(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  const shown = view === "history" && !canHistory ? "status" : view;

  // --- Resource dialog state ---
  const [resCreateOpen, setResCreateOpen] = useState(false);
  const [resEditing, setResEditing] = useState<HAResource | null>(null);

  // --- Group dialog state ---
  const [grpCreateOpen, setGrpCreateOpen] = useState(false);
  const [grpName, setGrpName] = useState("");
  const [grpNodes, setGrpNodes] = useState("");
  const [grpComment, setGrpComment] = useState("");
  const [grpEditing, setGrpEditing] = useState<HAGroup | null>(null);
  // The tab shows a failure in its banner (the hooks opt out of the app's
  // toast), and one that lands after it was left is toasted instead. A group
  // save is told apart by its dialog, as its success closes and clears it.
  const settle = useSaveOutcome();
  const settleGroupCreate = useSaveOutcome(grpCreateOpen);
  const settleGroupEdit = useSaveOutcome(grpEditing?.group ?? null);
  // A group save's failure, shown in its dialog: the tab's banner is behind
  // the modal, which stays open when the save fails.
  const [grpError, setGrpError] = useState<string | null>(null);
  const showGroupError = (err: unknown) => {
    setGrpError(describeError(err) || "Operation failed");
  };

  // --- Rule dialog state ---
  const [ruleCreateOpen, setRuleCreateOpen] = useState(false);
  const [ruleEditing, setRuleEditing] = useState<HARuleEntry | null>(null);

  // --- Delete confirmations: a row's delete button only sets these ---
  const [pendingResource, setPendingResource] = useState<HAResource | null>(
    null,
  );
  const [pendingRule, setPendingRule] = useState<HARuleEntry | null>(null);
  const [pendingGroup, setPendingGroup] = useState<HAGroup | null>(null);
  // A State pick that stops the guest only sets this; see handleQuickStateChange.
  const [pendingState, setPendingState] = useState<{
    sid: string;
    state: "stopped" | "disabled";
  } | null>(null);

  const existingHASIDs = useMemo(() => {
    const sids = new Set<string>();
    for (const r of resourcesQuery.data ?? []) sids.add(r.sid);
    return sids;
  }, [resourcesQuery.data]);

  const availableVMs = useMemo(() => {
    return (vmsQuery.data ?? []).filter(
      (vm) => !vm.template && !existingHASIDs.has(vmToSID(vm)),
    );
  }, [vmsQuery.data, existingHASIDs]);

  const allVMs = useMemo(() => {
    return (vmsQuery.data ?? []).filter((vm) => !vm.template);
  }, [vmsQuery.data]);
  const allNodes = nodesQuery.data ?? [];

  const vmNameBySID = useMemo(() => {
    const map = new Map<string, string>();
    for (const vm of vmsQuery.data ?? []) map.set(vmToSID(vm), vm.name);
    return map;
  }, [vmsQuery.data]);

  const handleMutationError = (err: unknown) => {
    const msg = err instanceof Error ? err.message : "Operation failed";
    setMutationError(msg);
    setTimeout(() => {
      setMutationError(null);
    }, 5000);
  };

  const clearGroupForm = () => {
    setGrpName("");
    setGrpNodes("");
    setGrpComment("");
  };

  const handleCreateGroup = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setGrpError(null);
    settleGroupCreate(
      createGroup.mutateAsync({
        group: grpName,
        nodes: grpNodes,
        ...(grpComment ? { comment: grpComment } : {}),
      }),
      {
        action: `Creating HA group ${grpName}`,
        onSuccess: () => {
          setGrpCreateOpen(false);
          clearGroupForm();
        },
        onError: showGroupError,
      },
    );
  };

  const handleUpdateGroup = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (!grpEditing) return;
    setGrpError(null);
    settleGroupEdit(
      updateGroup.mutateAsync({
        group: grpEditing.group,
        nodes: grpNodes,
        comment: grpComment,
      }),
      {
        action: `Saving HA group ${grpEditing.group}`,
        onSuccess: () => {
          setGrpEditing(null);
        },
        onError: showGroupError,
      },
    );
  };

  const startEditGroup = (g: HAGroup) => {
    setGrpError(null);
    setGrpEditing(g);
    setGrpNodes(g.nodes);
    setGrpComment(g.comment ?? "");
  };

  // stopped and disabled make HA shut the guest down, so they confirm first;
  // started and ignored send at once.
  const handleQuickStateChange = (sid: string, state: string) => {
    if (state === "stopped" || state === "disabled") {
      setPendingState({ sid, state });
      return;
    }
    settle(updateResource.mutateAsync({ sid, state }), {
      action: `Setting ${sid} to ${state}`,
      onError: handleMutationError,
    });
  };

  const handleQuickRuleDisable = (rule: HARuleEntry, disabled: boolean) => {
    settle(
      updateRule.mutateAsync({
        rule: rule.rule,
        type: rule.type,
        disable: disabled ? 1 : 0,
      }),
      {
        action: `${disabled ? "Disabling" : "Enabling"} HA rule ${rule.rule}`,
        onError: handleMutationError,
      },
    );
  };

  const handleDeleteResource = (sid: string) => {
    settle(deleteResource.mutateAsync(sid), {
      action: `Removing ${sid} from HA`,
      onError: handleMutationError,
    });
  };

  const handleDeleteGroup = (group: string) => {
    settle(deleteGroup.mutateAsync(group), {
      action: `Deleting HA group ${group}`,
      onError: handleMutationError,
    });
  };

  const handleDeleteRule = (rule: string) => {
    settle(deleteRule.mutateAsync(rule), {
      action: `Deleting HA rule ${rule}`,
      onError: handleMutationError,
    });
  };

  const {
    manager,
    quorum,
    nodes: nodeEntries,
    services: serviceEntries,
  } = categorizeStatus(statusQuery.data ?? []);
  const statusEntries = statusQuery.data ?? [];

  const hasRules = (rulesQuery.data ?? []).length > 0;
  // HA rules arrived with PVE 9.0 (pve-ha-manager 5.0.2; 4.0.x has no
  // API2/HA/Rules.pm). Not the rules query's success: PVE 8 answers the rules
  // path with 501, which the backend's ListRules turns into an empty 200, so
  // success alone offered "Add Rule" on a cluster where every save fails.
  const rulesSupported = isPVEAtLeast(pveVersion, PVE_FEATURES.HA_RULES);
  const hasGroups = (groupsQuery.data ?? []).length > 0;

  return (
    <Tabs value={shown} onValueChange={setView}>
      {mutationError && (
        <div className="mb-4 flex items-center gap-2 rounded-[10px] bg-destructive/10 px-[18px] py-3 text-sm text-destructive">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          <span>{mutationError}</span>
        </div>
      )}
      <Segmented
        aria-label="HA view"
        size="md"
        value={shown}
        onValueChange={setView}
        options={[
          ...HA_VIEWS,
          ...(canHistory ? [{ value: "history", label: "History" }] : []),
        ]}
      />

      {/* Each panel is named by its view: a dialog whose opener is gone
          when it closes (a deleted row) sends focus to the panel. */}
      <TabsContent
        value="status"
        aria-label="Status"
        aria-labelledby={undefined}
        className="mt-4 space-y-4"
      >
        {statusQuery.isLoading && (
          <div className="space-y-4">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-32 w-full" />
          </div>
        )}
        {statusQuery.isError && (
          <Panel>
            <ErrorBanner error={statusQuery.error} />
          </Panel>
        )}
        {!statusQuery.isLoading && !statusQuery.isError && (
          <>
            <Panel
              flush
              title="Manager and quorum"
              aside={
                statusEntries.length === 0
                  ? "No HA status available. HA may not be configured for this cluster."
                  : undefined
              }
            >
              <HAStatusStrip
                manager={manager}
                quorum={quorum}
                nodes={nodeEntries}
                services={serviceEntries}
              />
            </Panel>

            <HAMaintenanceCard clusterId={clusterId} pveVersion={pveVersion} />

            {nodeEntries.length > 0 && (
              <Panel
                flush
                title="Node status"
                aside={String(nodeEntries.length)}
              >
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Node</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>CRM state</TableHead>
                      <TableHead>Timestamp</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {nodeEntries.map((n) => (
                      <TableRow key={n.id}>
                        <TableCell className="font-medium">
                          {n.node ?? n.id.replace("lrm:", "")}
                        </TableCell>
                        <TableCell>
                          {n.status.includes("maintenance") ? (
                            <Badge variant="warning">Maintenance</Badge>
                          ) : (
                            statusBadge(
                              n.status.includes("active") ? "active" : n.status,
                            )
                          )}
                        </TableCell>
                        <TableCell>
                          {n.crm_state ??
                            (n.status.includes("active")
                              ? "active"
                              : (n.state ?? "—"))}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">
                          <DateTime
                            value={n.timestamp ? n.timestamp * 1000 : null}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Panel>
            )}

            {serviceEntries.length > 0 && (
              <Panel
                flush
                title="Active services"
                aside={String(serviceEntries.length)}
              >
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Service</TableHead>
                      <TableHead>Node</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead>Request state</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {serviceEntries.map((s) => (
                      <TableRow key={s.sid ?? s.id}>
                        <TableCell>
                          <span className="font-mono text-sm">
                            {s.sid ?? s.id}
                          </span>
                          {s.sid && vmNameBySID.get(s.sid) && (
                            <span className="ml-2 text-muted-foreground">
                              {vmNameBySID.get(s.sid)}
                            </span>
                          )}
                        </TableCell>
                        <TableCell>{s.node ?? "—"}</TableCell>
                        <TableCell>
                          {statusBadge(s.state ?? s.status)}
                        </TableCell>
                        <TableCell>{s.request_state ?? "—"}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Panel>
            )}

            {managerStatusQuery.data && (
              <Panel title="Manager details">
                {/* Raw, for diagnosis: folded away until it is wanted. */}
                <details className="text-sm">
                  <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
                    The manager&apos;s status as Proxmox reports it
                  </summary>
                  <pre className="mt-3 max-h-80 overflow-auto rounded-md bg-foreground/[0.05] p-3 font-mono text-xs">
                    {JSON.stringify(managerStatusQuery.data, null, 2)}
                  </pre>
                </details>
              </Panel>
            )}
          </>
        )}
      </TabsContent>

      <TabsContent
        value="resources"
        aria-label="Resources"
        aria-labelledby={undefined}
        className="mt-4"
      >
        <Panel
          flush
          title="HA resources"
          aside={
            resourcesQuery.data && resourcesQuery.data.length > 0
              ? String(resourcesQuery.data.length)
              : undefined
          }
          actions={
            canManage("ha") ? (
              <Dialog open={resCreateOpen} onOpenChange={setResCreateOpen}>
                <DialogTrigger asChild>
                  <Button size="sm">
                    <Plus className="mr-2 h-4 w-4" />
                    Add Resource
                  </Button>
                </DialogTrigger>
                <DialogContent className="max-w-md">
                  <DialogHeader>
                    <DialogTitle>Add HA Resource</DialogTitle>
                  </DialogHeader>
                  <HAResourceForm
                    mode="create"
                    clusterId={clusterId}
                    pveVersion={pveVersion}
                    availableVMs={availableVMs}
                    onSuccess={() => {
                      setResCreateOpen(false);
                    }}
                  />
                </DialogContent>
              </Dialog>
            ) : undefined
          }
        >
          {resourcesQuery.isLoading && (
            <div className="px-[18px]">
              <Skeleton className="h-20 w-full" />
            </div>
          )}
          {resourcesQuery.isError && (
            <div className="px-[18px]">
              <ErrorBanner error={resourcesQuery.error} />
            </div>
          )}
          {!resourcesQuery.isLoading &&
            !resourcesQuery.isError &&
            (resourcesQuery.data && resourcesQuery.data.length > 0 ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Resource</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Group</TableHead>
                    <TableHead>Restart / Relocate</TableHead>
                    {resourceFailback && <TableHead>Failback</TableHead>}
                    <TableHead>Comment</TableHead>
                    {canManage("ha") && (
                      <TableHead className="w-24">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    )}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {resourcesQuery.data.map((res) => (
                    <TableRow key={res.sid}>
                      <TableCell>
                        <span className="font-mono text-sm">{res.sid}</span>
                        {vmNameBySID.get(res.sid) && (
                          <span className="ml-2 text-muted-foreground">
                            {vmNameBySID.get(res.sid)}
                          </span>
                        )}
                        <ClusterHistoryButton
                          history={clusterHistories.haResource(
                            clusterId,
                            res.sid,
                          )}
                          title={res.sid}
                        />
                      </TableCell>
                      <TableCell>
                        {canManage("ha") ? (
                          <Select
                            value={res.state || HA_RESOURCE_DEFAULTS.state}
                            onValueChange={(v) => {
                              handleQuickStateChange(res.sid, v);
                            }}
                          >
                            <SelectTrigger className="h-7 w-28">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {HA_STATES.map((s) => (
                                <SelectItem key={s} value={s}>
                                  {s}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        ) : (
                          statusBadge(res.state || HA_RESOURCE_DEFAULTS.state)
                        )}
                      </TableCell>
                      <TableCell>
                        {res.status ? statusBadge(res.status) : "—"}
                      </TableCell>
                      <TableCell>{res.group || "—"}</TableCell>
                      <TableCell className="text-xs">
                        <ResourceSetting
                          value={res.max_restart}
                          fallback={HA_RESOURCE_DEFAULTS.max_restart}
                        />
                        {" / "}
                        <ResourceSetting
                          value={res.max_relocate}
                          fallback={HA_RESOURCE_DEFAULTS.max_relocate}
                        />
                      </TableCell>
                      {resourceFailback && (
                        <TableCell>
                          {(res.failback ?? HA_RESOURCE_DEFAULTS.failback) ===
                          0 ? (
                            <span className="text-muted-foreground">Off</span>
                          ) : (
                            "On"
                          )}
                          {res.failback === undefined && <DefaultMark />}
                        </TableCell>
                      )}
                      <TableCell
                        className="max-w-[12rem] truncate text-xs text-muted-foreground"
                        title={res.comment ?? ""}
                      >
                        {res.comment || "—"}
                      </TableCell>
                      {canManage("ha") && (
                        <TableCell className="whitespace-nowrap text-right">
                          <Button
                            aria-label={`Edit resource ${res.sid}`}
                            variant="ghost"
                            size="sm"
                            className={ROW_ACTION}
                            onClick={() => {
                              setResEditing(res);
                            }}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            aria-label={`Delete resource ${res.sid}`}
                            variant="ghost"
                            size="sm"
                            className={ROW_DELETE}
                            disabled={deleteResource.isPending}
                            onClick={() => {
                              setPendingResource(res);
                            }}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <p className="px-[18px] text-sm text-muted-foreground">
                No HA resources configured.
              </p>
            ))}
        </Panel>

        {/* Edit-resource dialog */}
        <Dialog
          open={resEditing != null}
          onOpenChange={(open) => {
            if (!open) setResEditing(null);
          }}
        >
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle>Edit HA Resource</DialogTitle>
            </DialogHeader>
            {resEditing && (
              <HAResourceForm
                mode="edit"
                clusterId={clusterId}
                pveVersion={pveVersion}
                resource={resEditing}
                onSuccess={() => {
                  setResEditing(null);
                }}
              />
            )}
          </DialogContent>
        </Dialog>
      </TabsContent>

      <TabsContent
        value="groups"
        aria-label="Groups / Rules"
        aria-labelledby={undefined}
        className="mt-4 space-y-4"
      >
        {/* HA Rules section (PVE 9.0+) */}
        <Panel
          flush
          title="HA rules"
          aside={hasRules ? String((rulesQuery.data ?? []).length) : undefined}
          actions={
            canManage("ha") && rulesSupported ? (
              <Dialog open={ruleCreateOpen} onOpenChange={setRuleCreateOpen}>
                <DialogTrigger asChild>
                  <Button size="sm">
                    <Plus className="mr-2 h-4 w-4" />
                    Add Rule
                  </Button>
                </DialogTrigger>
                <DialogContent className="max-w-lg">
                  <DialogHeader>
                    <DialogTitle>Create HA Rule</DialogTitle>
                  </DialogHeader>
                  <HARuleForm
                    mode="create"
                    clusterId={clusterId}
                    pveVersion={pveVersion}
                    allVMs={allVMs}
                    allNodes={allNodes}
                    onSuccess={() => {
                      setRuleCreateOpen(false);
                    }}
                  />
                </DialogContent>
              </Dialog>
            ) : undefined
          }
        >
          {rulesQuery.isLoading && (
            <div className="px-[18px]">
              <Skeleton className="h-20 w-full" />
            </div>
          )}
          {rulesQuery.isError && (
            <div className="px-[18px]">
              <ErrorBanner error={rulesQuery.error} />
            </div>
          )}
          {!rulesQuery.isLoading &&
            !rulesQuery.isError &&
            (hasRules ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Rule</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Resources</TableHead>
                    <TableHead>Nodes / Affinity</TableHead>
                    <TableHead>Strict</TableHead>
                    <TableHead>Comment</TableHead>
                    <TableHead>Enabled</TableHead>
                    {canManage("ha") && (
                      <TableHead className="w-24">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    )}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(rulesQuery.data ?? []).map((r) => (
                    <TableRow key={r.rule}>
                      <TableCell className="font-medium">
                        {r.rule}
                        <ClusterHistoryButton
                          history={clusterHistories.haRule(clusterId, r.rule)}
                          title={r.rule}
                        />
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">{r.type}</Badge>
                      </TableCell>
                      <TableCell className="text-xs font-mono">
                        {r.resources}
                      </TableCell>
                      <TableCell className="text-xs font-mono">
                        {r.type === "resource-affinity"
                          ? (r.affinity ?? "—")
                          : (r.nodes ?? "—")}
                      </TableCell>
                      <TableCell>
                        {r.type === "node-affinity" ? (
                          r.strict ? (
                            "Yes"
                          ) : (
                            <span className="text-muted-foreground">No</span>
                          )
                        ) : (
                          "—"
                        )}
                      </TableCell>
                      <TableCell
                        className="max-w-[10rem] truncate text-xs text-muted-foreground"
                        title={r.comment ?? ""}
                      >
                        {r.comment || "—"}
                      </TableCell>
                      <TableCell>
                        {canManage("ha") ? (
                          <Switch
                            aria-label={`Rule ${r.rule} enabled`}
                            checked={r.disable !== 1}
                            onCheckedChange={(checked) => {
                              handleQuickRuleDisable(r, !checked);
                            }}
                          />
                        ) : r.disable === 1 ? (
                          <Badge variant="secondary">Disabled</Badge>
                        ) : (
                          <Badge variant="healthy">Enabled</Badge>
                        )}
                      </TableCell>
                      {canManage("ha") && (
                        <TableCell className="whitespace-nowrap text-right">
                          <Button
                            aria-label={`Edit rule ${r.rule}`}
                            variant="ghost"
                            size="sm"
                            className={ROW_ACTION}
                            onClick={() => {
                              setRuleEditing(r);
                            }}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            aria-label={`Delete rule ${r.rule}`}
                            variant="ghost"
                            size="sm"
                            className={ROW_DELETE}
                            disabled={deleteRule.isPending}
                            onClick={() => {
                              setPendingRule(r);
                            }}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <p className="px-[18px] text-sm text-muted-foreground">
                {rulesSupported
                  ? "No HA rules configured."
                  : versionKnown
                    ? "HA rules require Proxmox VE 9.0 or newer."
                    : "Checking the cluster's Proxmox VE version…"}
              </p>
            ))}
        </Panel>

        {/* Edit-rule dialog */}
        <Dialog
          open={ruleEditing != null}
          onOpenChange={(open) => {
            if (!open) setRuleEditing(null);
          }}
        >
          <DialogContent className="max-w-lg">
            <DialogHeader>
              <DialogTitle>Edit HA Rule</DialogTitle>
            </DialogHeader>
            {ruleEditing && (
              <HARuleForm
                mode="edit"
                clusterId={clusterId}
                pveVersion={pveVersion}
                rule={ruleEditing}
                allVMs={allVMs}
                allNodes={allNodes}
                onSuccess={() => {
                  setRuleEditing(null);
                }}
              />
            )}
          </DialogContent>
        </Dialog>

        {/* HA Groups section. On PVE 9+ Proxmox auto-migrates groups to rules
            so this list is normally empty. We still surface it for PVE 8 and
            for any cluster where groups still exist — but not, with nothing
            to list, while the version is still unknown. */}
        {(hasGroups || (versionKnown && !rulesSupported)) && (
          <Panel
            flush
            title="HA groups"
            aside={
              rulesSupported
                ? "Legacy, superseded by rules in PVE 9"
                : undefined
            }
            actions={
              canManage("ha") && groupWritesAllowed ? (
                <Dialog
                  open={grpCreateOpen}
                  onOpenChange={(open) => {
                    // A new group starts empty: the nodes and comment are the
                    // edit dialog's too, and a closed create keeps what was typed.
                    if (open) {
                      setGrpError(null);
                      clearGroupForm();
                    }
                    setGrpCreateOpen(open);
                  }}
                >
                  <DialogTrigger asChild>
                    <Button size="sm">
                      <Plus className="mr-2 h-4 w-4" />
                      Add Group
                    </Button>
                  </DialogTrigger>
                  <DialogContent className="max-w-sm">
                    <DialogHeader>
                      <DialogTitle>Create HA Group</DialogTitle>
                    </DialogHeader>
                    <form onSubmit={handleCreateGroup} className="space-y-4">
                      <div className="space-y-2">
                        <Label>Group Name</Label>
                        <Input
                          value={grpName}
                          onChange={(e) => {
                            setGrpName(e.target.value);
                          }}
                          required
                          placeholder="mygroup"
                        />
                      </div>
                      <div className="space-y-2">
                        <Label>Nodes</Label>
                        <Input
                          value={grpNodes}
                          onChange={(e) => {
                            setGrpNodes(e.target.value);
                          }}
                          required
                          placeholder="node1:100,node2:50"
                        />
                        <p className="text-xs text-muted-foreground">
                          Comma-separated, optional <code>:priority</code> per
                          node.
                        </p>
                      </div>
                      <div className="space-y-2">
                        <Label>Comment</Label>
                        <Textarea
                          value={grpComment}
                          rows={2}
                          onChange={(e) => {
                            setGrpComment(e.target.value);
                          }}
                        />
                      </div>
                      {grpError && (
                        <p role="alert" className="text-sm text-destructive">
                          {grpError}
                        </p>
                      )}
                      <Button type="submit" disabled={createGroup.isPending}>
                        {createGroup.isPending ? "Creating..." : "Create"}
                      </Button>
                    </form>
                  </DialogContent>
                </Dialog>
              ) : undefined
            }
          >
            {groupsDeprecated && (
              <p className="flex items-start gap-2 px-[18px] pb-3 text-[12.5px] text-amber-700 dark:text-amber-400">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>
                  Proxmox VE 9 migrated HA Groups to <strong>HA Rules</strong>{" "}
                  (node affinity). Creating or editing groups is disabled; use
                  the <strong>Rules</strong> section above. Any existing groups
                  are shown read-only.
                </span>
              </p>
            )}
            {groupsQuery.isLoading && (
              <div className="px-[18px]">
                <Skeleton className="h-20 w-full" />
              </div>
            )}
            {groupsQuery.isError && (
              <div className="px-[18px]">
                <ErrorBanner error={groupsQuery.error} />
              </div>
            )}
            {!groupsQuery.isLoading &&
              !groupsQuery.isError &&
              (hasGroups ? (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Group</TableHead>
                      <TableHead>Nodes</TableHead>
                      <TableHead>Restricted</TableHead>
                      <TableHead>No failback</TableHead>
                      <TableHead>Comment</TableHead>
                      {canManage("ha") && (
                        <TableHead className="w-24">
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      )}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(groupsQuery.data ?? []).map((g) => (
                      <TableRow key={g.group}>
                        <TableCell className="font-medium">
                          {g.group}
                          <ClusterHistoryButton
                            history={clusterHistories.haGroup(
                              clusterId,
                              g.group,
                            )}
                            title={g.group}
                          />
                        </TableCell>
                        <TableCell className="text-xs font-mono">
                          {g.nodes}
                        </TableCell>
                        <TableCell>
                          <YesNo value={Boolean(g.restricted)} />
                        </TableCell>
                        <TableCell>
                          <YesNo value={Boolean(g.nofailback)} />
                        </TableCell>
                        <TableCell
                          className="max-w-[10rem] truncate text-xs text-muted-foreground"
                          title={g.comment ?? ""}
                        >
                          {g.comment ?? "—"}
                        </TableCell>
                        {canManage("ha") && (
                          <TableCell className="whitespace-nowrap text-right">
                            {groupWritesAllowed && (
                              <Button
                                aria-label={`Edit group ${g.group}`}
                                variant="ghost"
                                size="sm"
                                className={ROW_ACTION}
                                onClick={() => {
                                  startEditGroup(g);
                                }}
                              >
                                <Pencil className="h-4 w-4" />
                              </Button>
                            )}
                            <Button
                              aria-label={`Delete group ${g.group}`}
                              variant="ghost"
                              size="sm"
                              className={ROW_DELETE}
                              disabled={deleteGroup.isPending}
                              onClick={() => {
                                setPendingGroup(g);
                              }}
                            >
                              <Trash2 className="h-4 w-4" />
                            </Button>
                          </TableCell>
                        )}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              ) : (
                <p className="px-[18px] text-sm text-muted-foreground">
                  No HA groups configured.
                </p>
              ))}
          </Panel>
        )}

        {/* Edit-group dialog */}
        <Dialog
          open={grpEditing != null}
          onOpenChange={(open) => {
            if (!open) setGrpEditing(null);
          }}
        >
          <DialogContent className="max-w-sm">
            <DialogHeader>
              <DialogTitle>Edit HA Group</DialogTitle>
            </DialogHeader>
            {grpEditing && (
              <form onSubmit={handleUpdateGroup} className="space-y-4">
                <div className="space-y-2">
                  <Label>Group</Label>
                  <Input value={grpEditing.group} disabled />
                </div>
                <div className="space-y-2">
                  <Label>Nodes</Label>
                  <Input
                    value={grpNodes}
                    onChange={(e) => {
                      setGrpNodes(e.target.value);
                    }}
                    placeholder="node1:100,node2:50"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Comment</Label>
                  <Textarea
                    value={grpComment}
                    rows={2}
                    onChange={(e) => {
                      setGrpComment(e.target.value);
                    }}
                  />
                </div>
                {grpError && (
                  <p role="alert" className="text-sm text-destructive">
                    {grpError}
                  </p>
                )}
                <Button type="submit" disabled={updateGroup.isPending}>
                  {updateGroup.isPending ? "Saving..." : "Save"}
                </Button>
              </form>
            )}
          </DialogContent>
        </Dialog>
      </TabsContent>

      <HistoryTabContent history={history} label="History" />

      {/*
        Resource delete (pve-ha-manager API2/HA/Resources.pm "delete", then
        HA/Config.pm delete_service_from_config) only drops the sid from
        resources.cfg. The CRM then forgets the service ("removing stale
        service", HA/Manager.pm) and the LRM acts only on services the CRM
        tracks (HA/LRM.pm manage_resources), so nothing stops or moves the
        guest. Nexara sends no `purge`; where the API has it (added
        2025-11, commit c952c63 "api: add purge parameter for resource
        deletion") it defaults to 1 and also takes the sid out of every rule,
        deleting a rule left empty. Older releases leave the rules alone.
      */}
      <ConfirmDeleteDialog
        target={pendingResource}
        onClose={() => {
          setPendingResource(null);
        }}
        onConfirm={(res) => {
          handleDeleteResource(res.sid);
        }}
        title={(res) => {
          const name = vmNameBySID.get(res.sid);
          return `Delete HA resource ${res.sid}${name ? ` (${name})` : ""}?`;
        }}
        description={(res) =>
          `Proxmox stops managing ${res.sid} with HA as soon as you confirm: it is no longer restarted after a failure or recovered to another node when its node fails. The guest itself is left as it is — if it is running, it keeps running where it is. Recent Proxmox VE 9 releases also take it out of every HA rule that lists it and delete a rule it was the only resource of. To undo, add it to HA again.`
        }
      />
      {/*
        Rule delete (API2/HA/Rules.pm "delete_rule") only drops the rule
        from rules.cfg; the resources it names stay in resources.cfg.
      */}
      <ConfirmDeleteDialog
        target={pendingRule}
        onClose={() => {
          setPendingRule(null);
        }}
        onConfirm={(r) => {
          handleDeleteRule(r.rule);
        }}
        title={(r) => `Delete HA rule ${r.rule}?`}
        description={(r) =>
          r.disable === 1
            ? `Proxmox deletes this ${r.type} rule as soon as you confirm. It is disabled, so HA is not applying it to ${r.resources} now. Those resources stay HA-managed. To undo, create the rule again.`
            : `Proxmox deletes this ${r.type} rule as soon as you confirm, and HA stops applying it to ${r.resources}. Those resources stay HA-managed. To undo, create the rule again.`
        }
      />
      {/*
        Group delete (API2/HA/Groups.pm "delete") dies with "ha group is used
        by service" while any resource names the group, and with "ha groups
        have been migrated to rules" once PVE 9 migrated them.
      */}
      <ConfirmDeleteDialog
        target={pendingGroup}
        onClose={() => {
          setPendingGroup(null);
        }}
        onConfirm={(g) => {
          handleDeleteGroup(g.group);
        }}
        title={(g) => `Delete HA group ${g.group}?`}
        description={() =>
          "Proxmox deletes the group as soon as you confirm. It refuses while any HA resource is still assigned to the group, and on a cluster whose HA groups were migrated to rules. To undo, create the group again."
        }
      />
      {/*
        The LRM stops the guest with a shutdown (pve-ha-manager
        Resources/PVEVM.pm and PVECT.pm "shutdown"; only a VM's is forced once
        it times out, so the dialog promises no forced stop). The CRM still
        relocates a stopped resource on a node failure and not a disabled one
        (ha-manager(1), the resource "state" option).
      */}
      <ConfirmDeleteDialog
        target={pendingState}
        onClose={() => {
          setPendingState(null);
        }}
        onConfirm={(p) => {
          settle(updateResource.mutateAsync({ sid: p.sid, state: p.state }), {
            action: `Setting ${p.sid} to ${p.state}`,
            onError: handleMutationError,
          });
        }}
        title={(p) => {
          const name = vmNameBySID.get(p.sid);
          return `Set ${p.sid}${name ? ` (${name})` : ""} to ${p.state}?`;
        }}
        description={(p) =>
          `HA shuts ${p.sid} down shortly after you confirm, if it is running, and keeps it stopped until it is started again. ${
            p.state === "stopped"
              ? "If its node fails, HA still moves it to another node, stopped."
              : "Unlike stopped, it is not moved to another node if its node fails."
          }`
        }
        confirmLabel={(p) => `Set to ${p.state}`}
      />
    </Tabs>
  );
}

// --- Resource settings ---

/**
 * Marks a value as Proxmox's default rather than one the resource sets, so a
 * resource on the defaults reads differently from one pinned to the same
 * value.
 */
function DefaultMark() {
  return (
    <span
      className="text-muted-foreground"
      title="Not set on this resource, so Proxmox's default applies"
    >
      {" (default)"}
    </span>
  );
}

/**
 * A count an HA resource may leave unset (see HA_RESOURCE_DEFAULTS). Proxmox
 * returns the raw config, so an unset one arrives absent, not as 0: shown as
 * Proxmox's default, marked as a default, while an explicit value — 0 included
 * — shows as itself.
 */
function ResourceSetting({
  value,
  fallback,
}: {
  value: number | undefined;
  fallback: number;
}) {
  if (value !== undefined) return <>{value}</>;
  return (
    <>
      {fallback}
      <DefaultMark />
    </>
  );
}

// --- Status strip ---

/** A yes or no in a table: said in words, the no kept quiet. */
function YesNo({ value }: { value: boolean }) {
  return value ? <>Yes</> : <span className="text-muted-foreground">No</span>;
}

/**
 * The HA stack at a glance: who the manager is, whether the cluster has
 * quorum, and how many nodes and services HA is tracking.
 */
function HAStatusStrip({
  manager,
  quorum,
  nodes,
  services,
}: {
  manager: HAStatusEntry | undefined;
  quorum: HAStatusEntry | undefined;
  nodes: HAStatusEntry[];
  services: HAStatusEntry[];
}) {
  const managerActive =
    manager != null &&
    (manager.status.includes("active") || manager.state === "active");
  const hasQuorum =
    quorum != null && (quorum.quorum === 1 || quorum.status === "OK");
  const nodesActive = nodes.filter((n) => n.status.includes("active")).length;
  const servicesActive = services.filter(
    (s) => s.state === "started" || s.status === "active",
  ).length;
  return (
    <StatStrip>
      <Stat
        label="HA manager"
        value={
          manager == null
            ? "Not available"
            : managerActive
              ? "Active"
              : "Not active"
        }
        tone={manager == null ? "quiet" : managerActive ? "default" : "bad"}
        sub={
          manager == null
            ? undefined
            : managerActive
              ? manager.node
                ? `Master node: ${manager.node}`
                : undefined
              : manager.status
        }
      />
      <Stat
        label="Quorum"
        value={quorum == null ? "Unknown" : hasQuorum ? "OK" : "No quorum"}
        tone={quorum == null ? "quiet" : hasQuorum ? "default" : "bad"}
        sub={quorum == null ? "No quorum data" : undefined}
      />
      <Stat
        label="Nodes"
        value={String(nodes.length)}
        tone={nodes.length === 0 ? "quiet" : "default"}
        sub={nodes.length > 0 ? `${String(nodesActive)} active` : undefined}
      />
      <Stat
        label="HA services"
        value={String(services.length)}
        tone={services.length === 0 ? "quiet" : "default"}
        sub={
          services.length > 0 ? `${String(servicesActive)} active` : undefined
        }
      />
    </StatStrip>
  );
}
