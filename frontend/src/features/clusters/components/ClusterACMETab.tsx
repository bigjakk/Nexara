import { useRef, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
  DialogDescription,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Plus, RefreshCw, Trash2, ShieldCheck, ShieldOff } from "lucide-react";
import { ApiClientError } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import { useAuth } from "@/hooks/useAuth";
import { Textarea } from "@/components/ui/textarea";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useACMEAccounts,
  useACMEPlugins,
  useACMEChallengeSchema,
  useCreateACMEPlugin,
  useDeleteACMEPlugin,
  useNodeCertificates,
  useOrderNodeCertificate,
  useRenewNodeCertificate,
  useNodeACMEConfig,
  useSetNodeACMEConfig,
} from "@/features/acme/api/acme-queries";
import type {
  ACMEChallengeSchema,
  NodeACMEConfig,
} from "@/features/acme/api/acme-queries";
import { useClusterNodes } from "../api/cluster-queries";

interface ClusterACMETabProps {
  clusterId: string;
}

export function ClusterACMETab({ clusterId }: ClusterACMETabProps) {
  return (
    <Tabs defaultValue="accounts">
      <TabsList>
        <TabsTrigger value="accounts">Accounts</TabsTrigger>
        <TabsTrigger value="plugins">Plugins</TabsTrigger>
        <TabsTrigger value="certificates">Node Certificates</TabsTrigger>
      </TabsList>
      <TabsContent value="accounts" className="mt-4">
        <AccountsTab clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="plugins" className="mt-4">
        <PluginsTab clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="certificates" className="mt-4">
        <CertificatesTab clusterId={clusterId} />
      </TabsContent>
    </Tabs>
  );
}

// --- Accounts Tab ---

function AccountsTab({ clusterId }: { clusterId: string }) {
  const accountsQuery = useACMEAccounts(clusterId);

  return (
    <Card>
      <CardHeader>
        <CardTitle>ACME Accounts</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="mb-4 text-sm text-muted-foreground">
          ACME accounts must be created directly in the Proxmox web UI
          (Datacenter &gt; ACME). Proxmox restricts account registration to
          interactive root@pam sessions.
        </p>
        {accountsQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : !accountsQuery.data || accountsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No ACME accounts configured.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Directory</TableHead>
                <TableHead>Contact</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {accountsQuery.data.map((acc) => {
                const name = acc.name ?? "default";
                return (
                  <TableRow key={name}>
                    <TableCell className="font-medium">{name}</TableCell>
                    <TableCell className="text-xs max-w-[250px] truncate">
                      {acc.directory ?? "—"}
                    </TableCell>
                    <TableCell className="text-xs">
                      {typeof acc.account === "object" && acc.account !== null
                        ? (() => {
                            const c = (acc.account as Record<string, unknown>)[
                              "contact"
                            ];
                            return typeof c === "string"
                              ? c
                              : Array.isArray(c)
                                ? c.join(", ")
                                : "—";
                          })()
                        : "—"}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

// --- Plugins Tab ---

function PluginsTab({ clusterId }: { clusterId: string }) {
  const { canManage } = useAuth();
  const pluginsQuery = useACMEPlugins(clusterId);
  const deletePlugin = useDeleteACMEPlugin(clusterId);
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>ACME Plugins</CardTitle>
        {canManage("certificate") && (
          <Dialog open={createOpen} onOpenChange={setCreateOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="mr-1 h-4 w-4" />
                Add Plugin
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Add ACME Plugin</DialogTitle>
              </DialogHeader>
              <CreatePluginForm
                clusterId={clusterId}
                onSuccess={() => {
                  setCreateOpen(false);
                }}
              />
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>
      <CardContent>
        {pluginsQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : !pluginsQuery.data || pluginsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No ACME plugins configured.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Plugin</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>API</TableHead>
                <TableHead>Data</TableHead>
                {canManage("certificate") && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {pluginsQuery.data.map((p) => (
                <TableRow key={p.plugin}>
                  <TableCell className="font-medium">{p.plugin}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{p.type}</Badge>
                  </TableCell>
                  <TableCell className="text-xs">{p.api ?? "—"}</TableCell>
                  <TableCell
                    className="text-xs max-w-[200px] truncate"
                    title={p.data ?? ""}
                  >
                    {p.data ?? "—"}
                  </TableCell>
                  {canManage("certificate") && (
                    <TableCell className="text-right">
                      <Button
                        aria-label={`Delete plugin ${p.plugin}`}
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setDeleteConfirm(p.plugin);
                        }}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        <Dialog
          open={deleteConfirm !== null}
          onOpenChange={(v) => {
            if (!v) setDeleteConfirm(null);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete Plugin</DialogTitle>
              <DialogDescription>
                Are you sure you want to delete plugin &quot;{deleteConfirm}
                &quot;?
              </DialogDescription>
            </DialogHeader>
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                onClick={() => {
                  setDeleteConfirm(null);
                }}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  if (deleteConfirm) {
                    deletePlugin.mutate(deleteConfirm, {
                      onSuccess: () => {
                        setDeleteConfirm(null);
                      },
                    });
                  }
                }}
                disabled={deletePlugin.isPending}
              >
                Delete
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}

function getSchemaDataFields(
  schema: ACMEChallengeSchema,
): Array<{ key: string; description: string }> {
  const fields: Array<{ key: string; description: string }> = [];
  const schemaFields = schema.schema?.fields;
  if (!schemaFields) return fields;
  for (const [key, val] of Object.entries(schemaFields)) {
    fields.push({
      key,
      description: val.description ?? key,
    });
  }
  return fields;
}

function CreatePluginForm({
  clusterId,
  onSuccess,
}: {
  clusterId: string;
  onSuccess: () => void;
}) {
  const createPlugin = useCreateACMEPlugin(clusterId);
  const schemaQuery = useACMEChallengeSchema(clusterId);

  const [id, setId] = useState("");
  const [type, setType] = useState("standalone");
  const [api, setApi] = useState("");
  const [dataFields, setDataFields] = useState<Record<string, string>>({});
  const [validationDelay, setValidationDelay] = useState("30");

  const dnsSchemas = (schemaQuery.data ?? [])
    .filter((s) => s.type === "dns")
    .sort((a, b) => a.name.localeCompare(b.name));
  const selectedSchema = dnsSchemas.find((s) => s.id === api);
  const fields = selectedSchema ? getSchemaDataFields(selectedSchema) : [];

  const handleApiChange = (newApi: string) => {
    setApi(newApi);
    setDataFields({});
  };

  const handleFieldChange = (key: string, value: string) => {
    setDataFields((prev) => ({ ...prev, [key]: value }));
  };

  const buildDataString = (): string => {
    return Object.entries(dataFields)
      .filter(([, v]) => v.length > 0)
      .map(([k, v]) => `${k}=${v}`)
      .join("\n");
  };

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (!id || !type) return;
    const dataStr = buildDataString();
    const delay = parseInt(validationDelay, 10);
    createPlugin.mutate(
      {
        id,
        type,
        ...(api ? { api } : {}),
        ...(dataStr ? { data: dataStr } : {}),
        ...(type === "dns" && !isNaN(delay) && delay !== 30
          ? { "validation-delay": delay }
          : {}),
      },
      { onSuccess },
    );
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="space-y-4 max-h-[calc(85vh-6rem)] overflow-y-auto pr-1"
    >
      <div className="space-y-2">
        <Label>Plugin ID</Label>
        <Input
          value={id}
          onChange={(e) => {
            setId(e.target.value);
          }}
          placeholder="myplugin"
          required
        />
      </div>
      <div className="space-y-2">
        <Label>Validation Type</Label>
        <Select
          value={type}
          onValueChange={(v) => {
            setType(v);
            setApi("");
            setDataFields({});
          }}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="standalone">Standalone (HTTP)</SelectItem>
            <SelectItem value="dns">DNS</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {type === "dns" && (
        <>
          <div className="space-y-2">
            <Label>DNS API Plugin</Label>
            {schemaQuery.isLoading ? (
              <Skeleton className="h-9 w-full" />
            ) : (
              <Select value={api} onValueChange={handleApiChange}>
                <SelectTrigger>
                  <SelectValue placeholder="Select DNS provider..." />
                </SelectTrigger>
                <SelectContent className="max-h-[300px]">
                  {dnsSchemas.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {s.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {api && fields.length > 0 && (
            <div className="space-y-3 rounded-md border p-3">
              <p className="text-sm font-medium">
                {selectedSchema?.schema?.name ?? selectedSchema?.name ?? api}
              </p>
              {selectedSchema?.schema?.description && (
                <p className="text-xs text-muted-foreground">
                  {selectedSchema.schema.description}
                </p>
              )}
              {fields.map((f) => (
                <div key={f.key} className="space-y-1">
                  <Label className="text-xs">{f.key}</Label>
                  <Input
                    value={dataFields[f.key] ?? ""}
                    onChange={(e) => {
                      handleFieldChange(f.key, e.target.value);
                    }}
                    placeholder={f.description}
                  />
                </div>
              ))}
            </div>
          )}

          {api && fields.length === 0 && !schemaQuery.isLoading && (
            <div className="space-y-2">
              <Label>API Credentials (KEY=VALUE, one per line)</Label>
              <Textarea
                value={Object.entries(dataFields)
                  .map(([k, v]) => `${k}=${v}`)
                  .join("\n")}
                onChange={(e) => {
                  const parsed: Record<string, string> = {};
                  for (const line of e.target.value.split("\n")) {
                    const idx = line.indexOf("=");
                    if (idx > 0) {
                      parsed[line.substring(0, idx)] = line.substring(idx + 1);
                    }
                  }
                  setDataFields(parsed);
                }}
                placeholder={"KEY=value\nKEY2=value2"}
                rows={4}
              />
            </div>
          )}

          <div className="space-y-2">
            <Label>Validation Delay (seconds)</Label>
            <Input
              type="number"
              min={0}
              max={172800}
              value={validationDelay}
              onChange={(e) => {
                setValidationDelay(e.target.value);
              }}
            />
            <p className="text-xs text-muted-foreground">
              Time to wait for DNS propagation before validation (default 30)
            </p>
          </div>
        </>
      )}

      {createPlugin.isError && (
        <p className="text-sm text-destructive">{createPlugin.error.message}</p>
      )}
      {createPlugin.isSuccess && (
        <p className="text-sm text-emerald-600">Plugin created successfully.</p>
      )}

      <Button
        type="submit"
        disabled={!id || (type === "dns" && !api) || createPlugin.isPending}
      >
        {createPlugin.isPending ? "Creating..." : "Create"}
      </Button>
    </form>
  );
}

// --- Certificates Tab ---

function parseDomainEntry(val: string): {
  domain: string;
  plugin: string;
  alias: string;
} {
  const parts: Record<string, string> = {};
  for (const seg of val.split(",")) {
    const idx = seg.indexOf("=");
    if (idx > 0) {
      parts[seg.substring(0, idx)] = seg.substring(idx + 1);
    } else if (!parts["domain"]) {
      parts["domain"] = seg;
    }
  }
  return {
    domain: parts["domain"] ?? "",
    plugin: parts["plugin"] ?? "",
    alias: parts["alias"] ?? "",
  };
}

function buildDomainEntry(
  domain: string,
  plugin: string,
  alias: string,
): string {
  let entry = `domain=${domain}`;
  if (plugin) entry += `,plugin=${plugin}`;
  if (alias) entry += `,alias=${alias}`;
  return entry;
}

function CertificatesTab({ clusterId }: { clusterId: string }) {
  const { canManage } = useAuth();
  const nodesQuery = useClusterNodes(clusterId);
  const [selectedNode, setSelectedNode] = useState("");
  const orderCert = useOrderNodeCertificate(clusterId);
  const renewCert = useRenewNodeCertificate(clusterId);
  const { setFocusedTask, setPanelOpen } = useTaskLogStore();

  const firstNode = nodesQuery.data?.[0]?.name ?? "";
  const certNode = selectedNode || firstNode;
  const certsQuery = useNodeCertificates(clusterId, certNode);
  const acmeConfigQuery = useNodeACMEConfig(clusterId, certNode);
  const setAcmeConfig = useSetNodeACMEConfig(clusterId);
  const pluginsQuery = useACMEPlugins(clusterId);
  const accountsQuery = useACMEAccounts(clusterId);

  const [domainDialogOpen, setDomainDialogOpen] = useState(false);
  const [editDomain, setEditDomain] = useState("");
  const [editPlugin, setEditPlugin] = useState("");
  const [editAlias, setEditAlias] = useState("");
  // null while adding: an add has no slot until saveDomain picks one.
  const [editIndex, setEditIndex] = useState<number | null>(null);
  // The digest an edit saves with, pinned when its dialog opened. Undefined
  // for an add, which reads the live config at save time instead.
  const [editDigest, setEditDigest] = useState<string | undefined>(undefined);
  const [domainError, setDomainError] = useState("");

  const acmeConfig = acmeConfigQuery.data;
  const domainKeys = [
    "acmedomain0",
    "acmedomain1",
    "acmedomain2",
    "acmedomain3",
    "acmedomain4",
    "acmedomain5",
  ] as const;
  const configuredDomains = domainKeys
    .map((k, i) => ({ index: i, key: k, value: acmeConfig?.[k] ?? "" }))
    .filter((d) => d.value.length > 0);

  const hasDomains = configuredDomains.length > 0;
  const addingDomain = editIndex === null;

  // Bumped on every open, so an async callback can tell that a DIFFERENT
  // dialog has opened since it started. (Not that its own is still open — a
  // close without a reopen leaves this unchanged, which is harmless: both
  // openers set the pin themselves, so nothing stale can survive into one.)
  const domainDialogGen = useRef(0);

  const openDomainDialog = () => {
    domainDialogGen.current += 1;
    setDomainError("");
    setAcmeConfig.reset();
    setDomainDialogOpen(true);
  };

  // Every close goes through here. Radix calls onOpenChange only for closes it
  // initiates — Escape, the overlay, the X — so a Cancel button that sets the
  // open prop directly would skip the clear and leave a dialog-scoped message
  // stranded on the card behind it.
  const closeDomainDialog = () => {
    setDomainDialogOpen(false);
    setDomainError("");
  };

  const openAddDomain = () => {
    setEditDomain("");
    setEditPlugin("");
    setEditAlias("");
    setEditIndex(null);
    setEditDigest(undefined);
    // Refetch, because an add is based on nothing the operator can already see
    // being out of date: saveDomain picks the slot from whatever this returns,
    // so slot and digest still come from one read. Queries here hold for five
    // minutes and do not refetch on window focus, so a tab left open all
    // afternoon is the normal case rather than the unlucky one.
    void acmeConfigQuery.refetch();
    openDomainDialog();
  };

  const openEditDomain = (index: number, value: string) => {
    const parsed = parseDomainEntry(value);
    setEditDomain(parsed.domain);
    setEditPlugin(parsed.plugin);
    setEditAlias(parsed.alias);
    setEditIndex(index);
    // Pin the digest to the same read these fields came from, and do not
    // refetch. Reading it live at save time instead would let ANY refetch
    // landing while this dialog is open swap the digest under values that
    // never move with it — and plenty do: a WebSocket reconnect invalidates
    // every active query (stores/websocket-store.ts), ordering or renewing a
    // certificate invalidates this cluster, and refetchOnReconnect is on. The
    // save would then carry a digest matching a version of this slot the
    // operator never saw, and the compare-and-swap would wave through exactly
    // the overwrite it exists to catch. Pinned, a slot that moved since this
    // read answers 409, which is the whole point.
    setEditDigest(acmeConfig?.digest);
    openDomainDialog();
  };

  const saveDomain = () => {
    if (!editDomain) return;
    // The rule both branches serve: the digest must come from the same read as
    // whatever the save is based on. An edit is based on the values the dialog
    // was opened with, so it keeps that read's digest and its slot. An add is
    // based on nothing but the operator's typing, so it takes the newest read
    // — and therefore has to pick its slot HERE rather than at open time,
    // because the refetch can reveal a slot someone else filled in between. A
    // save aimed at the slot number chosen back then would carry a digest that
    // now matches and land on top of their domain.
    const index = editIndex ?? domainKeys.findIndex((k) => !acmeConfig?.[k]);
    if (index < 0) {
      setDomainError(
        "All six ACME domain slots are in use. Remove one in Proxmox before adding another.",
      );
      return;
    }
    const key = domainKeys[index];
    if (!key) return;
    setDomainError("");
    const config: NodeACMEConfig = {
      [key]: buildDomainEntry(editDomain, editPlugin, editAlias),
    };
    // Deliberately no `acme` key. It carries the ACME account, this dialog
    // cannot change it, and sending the cached copy back wrote whatever the
    // last read happened to see over whatever Proxmox actually had — on every
    // domain save. PVE's set_options only assigns the keys present in the
    // request, so omitting it leaves the account untouched.

    // An add takes the live digest, read in the same breath as the slot above
    // so the two cannot disagree; an edit takes the one pinned to the values
    // it is showing.
    const digest = addingDomain ? acmeConfig?.digest : editDigest;
    if (digest) {
      // Turns the write into a compare-and-swap. The digest covers the WHOLE
      // node config, not just the ACME keys, so editing the node's Notes in
      // the Proxmox UI conflicts with a pending save here too — which is why
      // the 409 copy talks about the node's configuration rather than ACME.
      config.digest = digest;
    }
    // No else. An add cannot get here without a config read, so the only way
    // to have no digest is a PVE that answered without one. That is an
    // unconditional write, as every save was before this — the check degrades
    // rather than locking the operator out of their own node.
    const gen = domainDialogGen.current;
    setAcmeConfig.mutate(
      { node: certNode, config },
      {
        onSuccess: closeDomainDialog,
        onError: (err) => {
          // Refetch so a deliberate retry carries the current digest, and
          // re-pin it: a conflict this dialog was shown is the one thing that
          // moves its pinned digest, or an edit could never be retried at all.
          // Still not an automatic retry — when the conflict is another
          // operator writing this same slot, retrying on their behalf performs
          // exactly the overwrite that was just prevented.
          if (!(err instanceof ApiClientError) || err.status !== 409) return;
          void acmeConfigQuery.refetch().then((res) => {
            // isSuccess, not res.data: a failed refetch RETAINS the last
            // successful data, so testing the data alone is a guard that can
            // never fail — it would move the pin onto a digest belonging to a
            // change the operator has not seen, which is the overwrite the pin
            // exists to prevent, reintroduced through the failure path.
            if (!res.isSuccess || !res.data.digest) return;
            // And only for the dialog that hit the conflict. Cancelling and
            // opening another row while this refetch is in flight would
            // otherwise land this digest on a pin that was just set to match
            // different values.
            if (domainDialogGen.current !== gen) return;
            setEditDigest(res.data.digest);
          });
        },
      },
    );
  };

  // Rendered in the dialog while it is open and in the card once it closes —
  // the card sits behind the open dialog rather than being unmounted by it, so
  // an ungated second copy shows the same message twice.
  //
  // The fallback is load-bearing, not politeness. describeError returns "" for
  // a TypeError, which is how a dropped connection rejects fetch, and the hook
  // has opted out of the global toast — so without a floor here that failure
  // renders as nothing at all, on both surfaces.
  const domainSaveError =
    domainError ||
    (setAcmeConfig.isError
      ? describeError(setAcmeConfig.error) ||
        "The save request failed — check your connection and try again."
      : "");

  // The server's 409 copy says to reload and try again, but the dialog's own
  // fields do not reload — only the table behind it does. Pressing Save again
  // therefore overwrites rather than retries, and the operator should be told
  // that rather than left to discover it.
  const domainSaveConflicted =
    setAcmeConfig.error instanceof ApiClientError &&
    setAcmeConfig.error.status === 409;

  // Only an add waits on the live config: it takes both its free slot and its
  // digest from it, and unread, the slot scan answers 0 and the write goes out
  // with no digest — a blind overwrite of acmedomain0, in precisely the state
  // where the compare-and-swap is most needed. isFetching holds it shut while
  // a refetch is in flight too, so a retry cannot re-send the digest it just
  // lost on.
  //
  // An edit carries its own pinned slot, values and digest, so it needs none
  // of that. Note this asks whether the data is THERE, not whether the query
  // is `isSuccess`: a background refetch that fails flips the status to error
  // while keeping the data, so gating on status would shut Save on an edit
  // that has everything it needs, until some later refetch happened to
  // succeed.
  const domainSaveBlocked =
    addingDomain && (!acmeConfig || acmeConfigQuery.isFetching);

  const formatDate = (ts?: number) => {
    if (!ts) return "—";
    return new Date(ts * 1000).toLocaleDateString();
  };

  const isExpiringSoon = (ts?: number) => {
    if (!ts) return false;
    const daysLeft = (ts * 1000 - Date.now()) / (1000 * 60 * 60 * 24);
    return daysLeft < 30;
  };

  return (
    <div className="space-y-4">
      {/* Node selector */}
      <div className="flex items-center gap-2">
        {nodesQuery.data && nodesQuery.data.length > 0 && (
          <Select
            value={certNode}
            onValueChange={(node) => {
              setSelectedNode(node);
              setAcmeConfig.reset();
            }}
          >
            <SelectTrigger className="w-[200px]">
              <SelectValue placeholder="Select node..." />
            </SelectTrigger>
            <SelectContent>
              {nodesQuery.data.map((n) => (
                <SelectItem key={n.name} value={n.name}>
                  {n.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>

      {/* ACME Domain Configuration */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">ACME Domain Configuration</CardTitle>
          {canManage("certificate") &&
            certNode.length > 0 &&
            configuredDomains.length < 6 && (
              <Button size="sm" variant="outline" onClick={openAddDomain}>
                <Plus className="mr-1 h-4 w-4" />
                Add Domain
              </Button>
            )}
        </CardHeader>
        <CardContent>
          {acmeConfig?.acme && (
            <p className="mb-3 text-xs text-muted-foreground">
              Account: {acmeConfig.acme}
            </p>
          )}
          {acmeConfigQuery.isLoading ? (
            <Skeleton className="h-16 w-full" />
          ) : configuredDomains.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No ACME domains configured. Add a domain before ordering
              certificates.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Domain</TableHead>
                  <TableHead>Plugin</TableHead>
                  <TableHead>Alias</TableHead>
                  {canManage("certificate") && (
                    <TableHead className="text-right">Actions</TableHead>
                  )}
                </TableRow>
              </TableHeader>
              <TableBody>
                {configuredDomains.map((d) => {
                  const parsed = parseDomainEntry(d.value);
                  return (
                    <TableRow key={d.key}>
                      <TableCell className="font-medium text-xs">
                        {parsed.domain}
                      </TableCell>
                      <TableCell className="text-xs">
                        {parsed.plugin || "standalone"}
                      </TableCell>
                      <TableCell className="text-xs">
                        {parsed.alias || "—"}
                      </TableCell>
                      {canManage("certificate") && (
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                              openEditDomain(d.index, d.value);
                            }}
                          >
                            Edit
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
          {!domainDialogOpen && domainSaveError && (
            <p className="mt-2 text-sm text-destructive">{domainSaveError}</p>
          )}
        </CardContent>
      </Card>

      {/* Domain add/edit dialog */}
      <Dialog
        open={domainDialogOpen}
        onOpenChange={(open) => {
          if (open) openDomainDialog();
          else closeDomainDialog();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {addingDomain ? "Add ACME Domain" : "Edit Domain"}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label>Domain</Label>
              <Input
                value={editDomain}
                onChange={(e) => {
                  setEditDomain(e.target.value);
                }}
                placeholder="node1.example.com"
              />
            </div>
            <div className="space-y-2">
              <Label>Challenge Plugin (optional for standalone HTTP)</Label>
              <Select
                value={editPlugin || "__standalone__"}
                onValueChange={(v) => {
                  setEditPlugin(v === "__standalone__" ? "" : v);
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__standalone__">
                    Standalone (HTTP-01)
                  </SelectItem>
                  {(pluginsQuery.data ?? [])
                    .filter((p) => p.type === "dns")
                    .map((p) => (
                      <SelectItem key={p.plugin} value={p.plugin}>
                        {p.plugin}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>DNS Alias (optional)</Label>
              <Input
                value={editAlias}
                onChange={(e) => {
                  setEditAlias(e.target.value);
                }}
                placeholder="acme-verify.example.com"
              />
              <p className="text-xs text-muted-foreground">
                For CNAME-based DNS-01 challenge delegation
              </p>
            </div>
            <div className="space-y-2">
              <Label>ACME Account</Label>
              <Select
                value={acmeConfig?.acme?.replace("account=", "") ?? "default"}
                onValueChange={() => {
                  // Read-only: shown for context, changed in the Proxmox UI.
                  // Domain saves leave the `acme` key alone — see saveDomain.
                }}
                disabled
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(accountsQuery.data ?? []).map((a) => (
                    <SelectItem
                      key={a.name ?? "default"}
                      value={a.name ?? "default"}
                    >
                      {a.name ?? "default"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {domainSaveError && (
              <p className="text-sm text-destructive">{domainSaveError}</p>
            )}
            {domainSaveConflicted && (
              <p className="text-sm text-muted-foreground">
                Saving again will overwrite the node's current configuration
                with the values shown here.
              </p>
            )}
            {domainSaveBlocked && (
              <p className="text-sm text-muted-foreground">
                {acmeConfigQuery.isError
                  ? "This node's current configuration could not be read, so saving would risk overwriting a setting Nexara cannot see."
                  : "Reading this node's current configuration…"}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={closeDomainDialog}>
                Cancel
              </Button>
              <Button
                onClick={saveDomain}
                disabled={
                  !editDomain || setAcmeConfig.isPending || domainSaveBlocked
                }
              >
                {setAcmeConfig.isPending ? "Saving..." : "Save"}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Certificates & Actions */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">Certificates</CardTitle>
          {canManage("certificate") && certNode && (
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  orderCert.mutate(
                    { node: certNode },
                    {
                      onSuccess: (data) => {
                        setFocusedTask({
                          clusterId,
                          upid: data.upid,
                          description: `Order ACME certificate — ${certNode}`,
                        });
                        setPanelOpen(true);
                      },
                    },
                  );
                }}
                disabled={orderCert.isPending || !hasDomains}
                title={
                  hasDomains
                    ? "Order new certificate via ACME"
                    : "Configure ACME domains first"
                }
              >
                <ShieldCheck className="mr-1 h-4 w-4" />
                {orderCert.isPending ? "Ordering..." : "Order Certificate"}
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  renewCert.mutate(
                    { node: certNode, force: true },
                    {
                      onSuccess: (data) => {
                        setFocusedTask({
                          clusterId,
                          upid: data.upid,
                          description: `Renew ACME certificate — ${certNode}`,
                        });
                        setPanelOpen(true);
                      },
                    },
                  );
                }}
                disabled={renewCert.isPending}
                title="Renew existing certificate (force)"
              >
                <RefreshCw className="mr-1 h-4 w-4" />
                {renewCert.isPending ? "Renewing..." : "Renew"}
              </Button>
            </div>
          )}
        </CardHeader>
        <CardContent>
          {(orderCert.isError || renewCert.isError) && (
            <p className="mb-4 text-sm text-destructive">
              {orderCert.error?.message ?? renewCert.error?.message}
            </p>
          )}

          {certsQuery.isLoading ? (
            <Skeleton className="h-20 w-full" />
          ) : !certsQuery.data || certsQuery.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No certificates found on this node.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>File</TableHead>
                  <TableHead>Subject</TableHead>
                  <TableHead>Issuer</TableHead>
                  <TableHead>SANs</TableHead>
                  <TableHead>Valid From</TableHead>
                  <TableHead>Valid Until</TableHead>
                  <TableHead>Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {certsQuery.data.map((cert) => (
                  <TableRow key={cert.filename}>
                    <TableCell className="font-medium text-xs">
                      {cert.filename}
                    </TableCell>
                    <TableCell className="text-xs">
                      {cert.subject ?? "—"}
                    </TableCell>
                    <TableCell
                      className="text-xs max-w-[200px] truncate"
                      title={cert.issuer ?? ""}
                    >
                      {cert.issuer ?? "—"}
                    </TableCell>
                    <TableCell
                      className="text-xs max-w-[200px] truncate"
                      title={cert.san ?? ""}
                    >
                      {cert.san ?? "—"}
                    </TableCell>
                    <TableCell className="text-xs">
                      {formatDate(cert.notbefore)}
                    </TableCell>
                    <TableCell className="text-xs">
                      {formatDate(cert.notafter)}
                    </TableCell>
                    <TableCell>
                      {isExpiringSoon(cert.notafter) ? (
                        <Badge variant="destructive" className="gap-1">
                          <ShieldOff className="h-3 w-3" />
                          Expiring
                        </Badge>
                      ) : (
                        <Badge variant="default" className="gap-1">
                          <ShieldCheck className="h-3 w-3" />
                          Valid
                        </Badge>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
