import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger, DialogDescription,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Plus, RefreshCw, Trash2, ShieldCheck, ShieldOff } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { Textarea } from "@/components/ui/textarea";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useACMEAccounts, useACMEPlugins, useACMEChallengeSchema,
  useCreateACMEPlugin, useDeleteACMEPlugin,
  useNodeCertificates, useOrderNodeCertificate, useRenewNodeCertificate,
  useNodeACMEConfig, useSetNodeACMEConfig,
} from "@/features/acme/api/acme-queries";
import type { ACMEChallengeSchema } from "@/features/acme/api/acme-queries";
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
          ACME accounts must be created directly in the Proxmox web UI (Datacenter &gt; ACME). Proxmox restricts account registration to interactive root@pam sessions.
        </p>
        {accountsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
         !accountsQuery.data || accountsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No ACME accounts configured.</p>
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
                    <TableCell className="text-xs max-w-[250px] truncate">{acc.directory ?? "—"}</TableCell>
                    <TableCell className="text-xs">{typeof acc.account === "object" && acc.account !== null ? (() => { const c = (acc.account as Record<string, unknown>)["contact"]; return typeof c === "string" ? c : Array.isArray(c) ? c.join(", ") : "—"; })() : "—"}</TableCell>
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
              <Button size="sm"><Plus className="mr-1 h-4 w-4" />Add Plugin</Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader><DialogTitle>Add ACME Plugin</DialogTitle></DialogHeader>
              <CreatePluginForm clusterId={clusterId} onSuccess={() => { setCreateOpen(false); }} />
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>
      <CardContent>
        {pluginsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
         !pluginsQuery.data || pluginsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No ACME plugins configured.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Plugin</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>API</TableHead>
                <TableHead>Data</TableHead>
                {canManage("certificate") && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {pluginsQuery.data.map((p) => (
                <TableRow key={p.plugin}>
                  <TableCell className="font-medium">{p.plugin}</TableCell>
                  <TableCell><Badge variant="outline">{p.type}</Badge></TableCell>
                  <TableCell className="text-xs">{p.api ?? "—"}</TableCell>
                  <TableCell className="text-xs max-w-[200px] truncate" title={p.data ?? ""}>{p.data ?? "—"}</TableCell>
                  {canManage("certificate") && (
                    <TableCell className="text-right">
                      <Button variant="ghost" size="sm" onClick={() => { setDeleteConfirm(p.plugin); }}>
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        <Dialog open={deleteConfirm !== null} onOpenChange={(v) => { if (!v) setDeleteConfirm(null); }}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete Plugin</DialogTitle>
              <DialogDescription>
                Are you sure you want to delete plugin &quot;{deleteConfirm}&quot;?
              </DialogDescription>
            </DialogHeader>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => { setDeleteConfirm(null); }}>Cancel</Button>
              <Button variant="destructive" onClick={() => {
                if (deleteConfirm) {
                  deletePlugin.mutate(deleteConfirm, { onSuccess: () => { setDeleteConfirm(null); } });
                }
              }} disabled={deletePlugin.isPending}>Delete</Button>
            </div>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}

function getSchemaDataFields(schema: ACMEChallengeSchema): Array<{ key: string; description: string }> {
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

function CreatePluginForm({ clusterId, onSuccess }: { clusterId: string; onSuccess: () => void }) {
  const createPlugin = useCreateACMEPlugin(clusterId);
  const schemaQuery = useACMEChallengeSchema(clusterId);

  const [id, setId] = useState("");
  const [type, setType] = useState("standalone");
  const [api, setApi] = useState("");
  const [dataFields, setDataFields] = useState<Record<string, string>>({});
  const [validationDelay, setValidationDelay] = useState("30");

  const dnsSchemas = (schemaQuery.data ?? []).filter((s) => s.type === "dns").sort((a, b) => a.name.localeCompare(b.name));
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
        ...(type === "dns" && !isNaN(delay) && delay !== 30 ? { "validation-delay": delay } : {}),
      },
      { onSuccess },
    );
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
      <div className="space-y-2">
        <Label>Plugin ID</Label>
        <Input value={id} onChange={(e) => { setId(e.target.value); }} placeholder="myplugin" required />
      </div>
      <div className="space-y-2">
        <Label>Validation Type</Label>
        <Select value={type} onValueChange={(v) => { setType(v); setApi(""); setDataFields({}); }}>
          <SelectTrigger><SelectValue /></SelectTrigger>
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
            {schemaQuery.isLoading ? <Skeleton className="h-9 w-full" /> : (
              <Select value={api} onValueChange={handleApiChange}>
                <SelectTrigger>
                  <SelectValue placeholder="Select DNS provider..." />
                </SelectTrigger>
                <SelectContent className="max-h-[300px]">
                  {dnsSchemas.map((s) => (
                    <SelectItem key={s.id} value={s.id}>{s.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {api && fields.length > 0 && (
            <div className="space-y-3 rounded-md border p-3">
              <p className="text-sm font-medium">{selectedSchema?.schema?.name ?? selectedSchema?.name ?? api}</p>
              {selectedSchema?.schema?.description && (
                <p className="text-xs text-muted-foreground">{selectedSchema.schema.description}</p>
              )}
              {fields.map((f) => (
                <div key={f.key} className="space-y-1">
                  <Label className="text-xs">{f.key}</Label>
                  <Input
                    value={dataFields[f.key] ?? ""}
                    onChange={(e) => { handleFieldChange(f.key, e.target.value); }}
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
                value={Object.entries(dataFields).map(([k, v]) => `${k}=${v}`).join("\n")}
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
              onChange={(e) => { setValidationDelay(e.target.value); }}
            />
            <p className="text-xs text-muted-foreground">Time to wait for DNS propagation before validation (default 30)</p>
          </div>
        </>
      )}

      {createPlugin.isError && (
        <p className="text-sm text-destructive">{createPlugin.error.message}</p>
      )}
      {createPlugin.isSuccess && (
        <p className="text-sm text-green-600">Plugin created successfully.</p>
      )}

      <Button type="submit" disabled={!id || (type === "dns" && !api) || createPlugin.isPending}>
        {createPlugin.isPending ? "Creating..." : "Create"}
      </Button>
    </form>
  );
}

// --- Certificates Tab ---

function parseDomainEntry(val: string): { domain: string; plugin: string; alias: string } {
  const parts: Record<string, string> = {};
  for (const seg of val.split(",")) {
    const idx = seg.indexOf("=");
    if (idx > 0) {
      parts[seg.substring(0, idx)] = seg.substring(idx + 1);
    } else if (!parts["domain"]) {
      parts["domain"] = seg;
    }
  }
  return { domain: parts["domain"] ?? "", plugin: parts["plugin"] ?? "", alias: parts["alias"] ?? "" };
}

function buildDomainEntry(domain: string, plugin: string, alias: string): string {
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
  const [editIndex, setEditIndex] = useState<number | null>(null);

  const acmeConfig = acmeConfigQuery.data;
  const domainKeys = ["acmedomain0", "acmedomain1", "acmedomain2", "acmedomain3", "acmedomain4", "acmedomain5"] as const;
  const configuredDomains = domainKeys
    .map((k, i) => ({ index: i, key: k, value: acmeConfig?.[k] ?? "" }))
    .filter((d) => d.value.length > 0);

  const hasDomains = configuredDomains.length > 0;

  const openAddDomain = () => {
    setEditDomain("");
    setEditPlugin("");
    setEditAlias("");
    const nextIndex = domainKeys.findIndex((k) => !acmeConfig?.[k]);
    setEditIndex(nextIndex >= 0 ? nextIndex : null);
    setDomainDialogOpen(true);
  };

  const openEditDomain = (index: number, value: string) => {
    const parsed = parseDomainEntry(value);
    setEditDomain(parsed.domain);
    setEditPlugin(parsed.plugin);
    setEditAlias(parsed.alias);
    setEditIndex(index);
    setDomainDialogOpen(true);
  };

  const saveDomain = () => {
    if (!editDomain || editIndex === null) return;
    const entry = buildDomainEntry(editDomain, editPlugin, editAlias);
    const key = domainKeys[editIndex];
    if (!key) return;
    const account = acmeConfig?.acme ?? "account=default";
    setAcmeConfig.mutate(
      { node: certNode, config: { acme: account, [key]: entry } },
      { onSuccess: () => { setDomainDialogOpen(false); } },
    );
  };

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
          <Select value={certNode} onValueChange={setSelectedNode}>
            <SelectTrigger className="w-[200px]">
              <SelectValue placeholder="Select node..." />
            </SelectTrigger>
            <SelectContent>
              {nodesQuery.data.map((n) => (
                <SelectItem key={n.name} value={n.name}>{n.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>

      {/* ACME Domain Configuration */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">ACME Domain Configuration</CardTitle>
          {canManage("certificate") && configuredDomains.length < 6 && (
            <Button size="sm" variant="outline" onClick={openAddDomain}>
              <Plus className="mr-1 h-4 w-4" />Add Domain
            </Button>
          )}
        </CardHeader>
        <CardContent>
          {acmeConfig?.acme && (
            <p className="mb-3 text-xs text-muted-foreground">Account: {acmeConfig.acme}</p>
          )}
          {acmeConfigQuery.isLoading ? <Skeleton className="h-16 w-full" /> :
           configuredDomains.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No ACME domains configured. Add a domain before ordering certificates.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Domain</TableHead>
                  <TableHead>Plugin</TableHead>
                  <TableHead>Alias</TableHead>
                  {canManage("certificate") && <TableHead className="text-right">Actions</TableHead>}
                </TableRow>
              </TableHeader>
              <TableBody>
                {configuredDomains.map((d) => {
                  const parsed = parseDomainEntry(d.value);
                  return (
                    <TableRow key={d.key}>
                      <TableCell className="font-medium text-xs">{parsed.domain}</TableCell>
                      <TableCell className="text-xs">{parsed.plugin || "standalone"}</TableCell>
                      <TableCell className="text-xs">{parsed.alias || "—"}</TableCell>
                      {canManage("certificate") && (
                        <TableCell className="text-right">
                          <Button variant="ghost" size="sm" onClick={() => { openEditDomain(d.index, d.value); }}>
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
          {setAcmeConfig.isError && (
            <p className="mt-2 text-sm text-destructive">{setAcmeConfig.error.message}</p>
          )}
        </CardContent>
      </Card>

      {/* Domain add/edit dialog */}
      <Dialog open={domainDialogOpen} onOpenChange={setDomainDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editIndex !== null && configuredDomains.some((d) => d.index === editIndex) ? "Edit Domain" : "Add ACME Domain"}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label>Domain</Label>
              <Input value={editDomain} onChange={(e) => { setEditDomain(e.target.value); }} placeholder="node1.example.com" />
            </div>
            <div className="space-y-2">
              <Label>Challenge Plugin (optional for standalone HTTP)</Label>
              <Select value={editPlugin || "__standalone__"} onValueChange={(v) => { setEditPlugin(v === "__standalone__" ? "" : v); }}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__standalone__">Standalone (HTTP-01)</SelectItem>
                  {(pluginsQuery.data ?? []).filter((p) => p.type === "dns").map((p) => (
                    <SelectItem key={p.plugin} value={p.plugin}>{p.plugin}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>DNS Alias (optional)</Label>
              <Input value={editAlias} onChange={(e) => { setEditAlias(e.target.value); }} placeholder="acme-verify.example.com" />
              <p className="text-xs text-muted-foreground">For CNAME-based DNS-01 challenge delegation</p>
            </div>
            <div className="space-y-2">
              <Label>ACME Account</Label>
              <Select
                value={acmeConfig?.acme?.replace("account=", "") ?? "default"}
                onValueChange={() => { /* account is set on save */ }}
                disabled
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(accountsQuery.data ?? []).map((a) => (
                    <SelectItem key={a.name ?? "default"} value={a.name ?? "default"}>{a.name ?? "default"}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => { setDomainDialogOpen(false); }}>Cancel</Button>
              <Button onClick={saveDomain} disabled={!editDomain || setAcmeConfig.isPending}>
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
              <Button size="sm" variant="outline" onClick={() => {
                  orderCert.mutate({ node: certNode }, {
                    onSuccess: (data) => {
                      setFocusedTask({ clusterId, upid: data.upid, description: `Order ACME certificate — ${certNode}` });
                      setPanelOpen(true);
                    },
                  });
                }}
                disabled={orderCert.isPending || !hasDomains} title={hasDomains ? "Order new certificate via ACME" : "Configure ACME domains first"}>
                <ShieldCheck className="mr-1 h-4 w-4" />
                {orderCert.isPending ? "Ordering..." : "Order Certificate"}
              </Button>
              <Button size="sm" variant="outline" onClick={() => {
                  renewCert.mutate({ node: certNode, force: true }, {
                    onSuccess: (data) => {
                      setFocusedTask({ clusterId, upid: data.upid, description: `Renew ACME certificate — ${certNode}` });
                      setPanelOpen(true);
                    },
                  });
                }}
                disabled={renewCert.isPending} title="Renew existing certificate (force)">
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

          {certsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
           !certsQuery.data || certsQuery.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">No certificates found on this node.</p>
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
                    <TableCell className="font-medium text-xs">{cert.filename}</TableCell>
                    <TableCell className="text-xs">{cert.subject ?? "—"}</TableCell>
                    <TableCell className="text-xs max-w-[200px] truncate" title={cert.issuer ?? ""}>{cert.issuer ?? "—"}</TableCell>
                    <TableCell className="text-xs max-w-[200px] truncate" title={cert.san ?? ""}>{cert.san ?? "—"}</TableCell>
                    <TableCell className="text-xs">{formatDate(cert.notbefore)}</TableCell>
                    <TableCell className="text-xs">{formatDate(cert.notafter)}</TableCell>
                    <TableCell>
                      {isExpiringSoon(cert.notafter) ? (
                        <Badge variant="destructive" className="gap-1">
                          <ShieldOff className="h-3 w-3" />Expiring
                        </Badge>
                      ) : (
                        <Badge variant="default" className="gap-1">
                          <ShieldCheck className="h-3 w-3" />Valid
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
