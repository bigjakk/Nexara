import { useState, useEffect, useCallback } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { AlertTriangle, Check, Copy, Info, Save } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import {
  useClusterOptions, useUpdateClusterOptions,
  useClusterDescription, useUpdateClusterDescription,
  useClusterTags, useUpdateClusterTags,
  useClusterJoinInfo, useCorosyncNodes,
} from "../api/cluster-options-queries";
import type { ClusterOptions } from "../api/cluster-options-queries";

interface OptionsFormData {
  console: string;
  keyboard: string;
  language: string;
  email_from: string;
  http_proxy: string;
  mac_prefix: string;
  migration_type: string;
  bwlimit: string;
  nextId: string;
  ha: string;
  fencing: string;
  crs: string;
  max_workers: number;
}

const EMPTY_FORM: OptionsFormData = {
  console: "", keyboard: "", language: "", email_from: "",
  http_proxy: "", mac_prefix: "", migration_type: "", bwlimit: "",
  nextId: "", ha: "", fencing: "", crs: "", max_workers: 0,
};

/** Convert a value that may be a string or object to a display string. */
function toStr(v: unknown): string {
  if (v == null) return "";
  if (typeof v === "string") return v;
  if (typeof v === "object") {
    // Convert {key: value, ...} to "key=value,key2=value2" property-string format
    return Object.entries(v as Record<string, unknown>)
      .map(([k, val]) => `${k}=${String(val)}`)
      .join(",");
  }
  return typeof v === "number" || typeof v === "boolean" ? String(v) : "";
}

function optsToForm(d: ClusterOptions): OptionsFormData {
  return {
    console: toStr(d["console"]),
    keyboard: toStr(d["keyboard"]),
    language: toStr(d["language"]),
    email_from: toStr(d["email_from"]),
    http_proxy: toStr(d["http_proxy"]),
    mac_prefix: toStr(d["mac_prefix"]),
    migration_type: toStr(d["migration_type"]),
    bwlimit: toStr(d["bwlimit"]),
    nextId: toStr(d["next-id"]),
    ha: toStr(d["ha"]),
    fencing: toStr(d["fencing"]),
    crs: toStr(d["crs"]),
    max_workers: typeof d["max_workers"] === "number" ? d["max_workers"] : 0,
  };
}

interface ClusterOptionsTabProps {
  clusterId: string;
}

function ErrorBanner({ error }: { error: Error }) {
  const message = error.message || "Failed to load data";
  const isForbidden = message.includes("403") || message.toLowerCase().includes("permission") || message.toLowerCase().includes("forbidden");
  return (
    <div className="flex items-center gap-2 rounded-md border border-orange-300 bg-orange-50 p-3 text-sm text-orange-800 dark:border-orange-700 dark:bg-orange-950 dark:text-orange-200">
      <AlertTriangle className="h-4 w-4 flex-shrink-0" />
      <span>
        {isForbidden
          ? "The Proxmox API token does not have permission to access this data. Ensure the token has Sys.Audit privilege on /."
          : message}
      </span>
    </div>
  );
}

export function ClusterOptionsTab({ clusterId }: ClusterOptionsTabProps) {
  const { canManage } = useAuth();
  const optionsQuery = useClusterOptions(clusterId);
  const descQuery = useClusterDescription(clusterId);
  const tagsQuery = useClusterTags(clusterId);
  const joinQuery = useClusterJoinInfo(clusterId);
  const nodesQuery = useCorosyncNodes(clusterId);
  const updateOpts = useUpdateClusterOptions(clusterId);
  const updateDesc = useUpdateClusterDescription(clusterId);
  const updateTags = useUpdateClusterTags(clusterId);

  return (
    <Tabs defaultValue="notes">
      <TabsList>
        <TabsTrigger value="notes">Notes</TabsTrigger>
        <TabsTrigger value="general">General</TabsTrigger>
        <TabsTrigger value="tags">Tags</TabsTrigger>
        <TabsTrigger value="info">Cluster Info</TabsTrigger>
      </TabsList>

      <TabsContent value="notes" className="mt-4">
        <NotesSection descQuery={descQuery} updateDesc={updateDesc} canEdit={canManage("cluster")} />
      </TabsContent>

      <TabsContent value="general" className="mt-4">
        <GeneralSection optionsQuery={optionsQuery} updateOpts={updateOpts} canEdit={canManage("cluster")} />
      </TabsContent>

      <TabsContent value="tags" className="mt-4">
        <TagsSection tagsQuery={tagsQuery} updateTags={updateTags} canEdit={canManage("cluster")} />
      </TabsContent>

      <TabsContent value="info" className="mt-4 space-y-4">
        <JoinInfoSection joinQuery={joinQuery} />
        <CorosyncNodesSection nodesQuery={nodesQuery} />
      </TabsContent>
    </Tabs>
  );
}

// --- Notes Section ---

function NotesSection({
  descQuery, updateDesc, canEdit,
}: {
  descQuery: ReturnType<typeof useClusterDescription>;
  updateDesc: ReturnType<typeof useUpdateClusterDescription>;
  canEdit: boolean;
}) {
  const [description, setDescription] = useState("");
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (descQuery.data && !dirty) {
      setDescription(descQuery.data.description);
    }
  }, [descQuery.data, dirty]);

  const handleSave = () => {
    updateDesc.mutate(description, {
      onSuccess: () => { setDirty(false); },
    });
  };

  return (
    <Card>
      <CardHeader><CardTitle>Cluster Description / Notes</CardTitle></CardHeader>
      <CardContent className="space-y-4">
        {descQuery.isError && <ErrorBanner error={descQuery.error} />}
        <Textarea
          value={description}
          onChange={(e) => { setDescription(e.target.value); setDirty(true); }}
          placeholder="Enter cluster notes or description (supports markdown)..."
          rows={8}
          disabled={!canEdit || descQuery.isError}
        />
        {canEdit && !descQuery.isError && (
          <Button onClick={handleSave} disabled={!dirty || updateDesc.isPending} size="sm">
            <Save className="mr-2 h-4 w-4" />
            {updateDesc.isPending ? "Saving..." : "Save Description"}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

// --- General Options Section (Editable) ---

function GeneralSection({
  optionsQuery, updateOpts, canEdit,
}: {
  optionsQuery: ReturnType<typeof useClusterOptions>;
  updateOpts: ReturnType<typeof useUpdateClusterOptions>;
  canEdit: boolean;
}) {
  const [form, setForm] = useState<OptionsFormData>({ ...EMPTY_FORM });
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (optionsQuery.data && !dirty) {
      setForm(optsToForm(optionsQuery.data));
    }
  }, [optionsQuery.data, dirty]);

  const updateField = useCallback(<K extends keyof OptionsFormData>(key: K, value: OptionsFormData[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
    setDirty(true);
  }, []);

  const handleSave = () => {
    const orig = optionsQuery.data ? optsToForm(optionsQuery.data) : EMPTY_FORM;
    const params: Record<string, unknown> = {};

    if (form.console !== orig.console) params["console"] = form.console;
    if (form.keyboard !== orig.keyboard) params["keyboard"] = form.keyboard;
    if (form.language !== orig.language) params["language"] = form.language;
    if (form.email_from !== orig.email_from) params["email_from"] = form.email_from;
    if (form.http_proxy !== orig.http_proxy) params["http_proxy"] = form.http_proxy;
    if (form.mac_prefix !== orig.mac_prefix) params["mac_prefix"] = form.mac_prefix;
    if (form.migration_type !== orig.migration_type) params["migration_type"] = form.migration_type;
    if (form.bwlimit !== orig.bwlimit) params["bwlimit"] = form.bwlimit;
    if (form.nextId !== orig.nextId) params["next-id"] = form.nextId;
    if (form.ha !== orig.ha) params["ha"] = form.ha;
    if (form.fencing !== orig.fencing) params["fencing"] = form.fencing;
    if (form.crs !== orig.crs) params["crs"] = form.crs;
    if (form.max_workers !== orig.max_workers) params["max_workers"] = form.max_workers;

    if (Object.keys(params).length === 0) {
      setDirty(false);
      return;
    }

    updateOpts.mutate(params as Partial<ClusterOptions>, {
      onSuccess: () => { setDirty(false); },
    });
  };

  if (optionsQuery.isLoading) {
    return (
      <Card>
        <CardHeader><CardTitle>Datacenter Options</CardTitle></CardHeader>
        <CardContent className="space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </CardContent>
      </Card>
    );
  }

  if (optionsQuery.isError) {
    return (
      <Card>
        <CardHeader><CardTitle>Datacenter Options</CardTitle></CardHeader>
        <CardContent><ErrorBanner error={optionsQuery.error} /></CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>Datacenter Options</CardTitle>
          {canEdit && (
            <Button onClick={handleSave} disabled={!dirty || updateOpts.isPending} size="sm">
              <Save className="mr-2 h-4 w-4" />
              {updateOpts.isPending ? "Saving..." : "Save Changes"}
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
          <div className="space-y-2">
            <Label className="text-sm font-medium">Console</Label>
            <Select value={form.console || "__default__"} onValueChange={(v) => { updateField("console", v === "__default__" ? "" : v); }} disabled={!canEdit}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">Default</SelectItem>
                <SelectItem value="applet">Java Applet (deprecated)</SelectItem>
                <SelectItem value="vv">SPICE (virt-viewer)</SelectItem>
                <SelectItem value="html5">HTML5 (noVNC)</SelectItem>
                <SelectItem value="xtermjs">xterm.js</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label className="text-sm font-medium">Keyboard Layout</Label>
            <Select value={form.keyboard || "__default__"} onValueChange={(v) => { updateField("keyboard", v === "__default__" ? "" : v); }} disabled={!canEdit}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">Default</SelectItem>
                <SelectItem value="en-us">English (US)</SelectItem>
                <SelectItem value="en-gb">English (UK)</SelectItem>
                <SelectItem value="de">German</SelectItem>
                <SelectItem value="de-ch">German (Swiss)</SelectItem>
                <SelectItem value="fr">French</SelectItem>
                <SelectItem value="fr-ch">French (Swiss)</SelectItem>
                <SelectItem value="es">Spanish</SelectItem>
                <SelectItem value="it">Italian</SelectItem>
                <SelectItem value="ja">Japanese</SelectItem>
                <SelectItem value="pt-br">Portuguese (Brazil)</SelectItem>
                <SelectItem value="sv">Swedish</SelectItem>
                <SelectItem value="no">Norwegian</SelectItem>
                <SelectItem value="da">Danish</SelectItem>
                <SelectItem value="fi">Finnish</SelectItem>
                <SelectItem value="hu">Hungarian</SelectItem>
                <SelectItem value="pl">Polish</SelectItem>
                <SelectItem value="tr">Turkish</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label className="text-sm font-medium">Language</Label>
            <Select value={form.language || "__default__"} onValueChange={(v) => { updateField("language", v === "__default__" ? "" : v); }} disabled={!canEdit}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">Default</SelectItem>
                <SelectItem value="en">English</SelectItem>
                <SelectItem value="de">German</SelectItem>
                <SelectItem value="fr">French</SelectItem>
                <SelectItem value="es">Spanish</SelectItem>
                <SelectItem value="it">Italian</SelectItem>
                <SelectItem value="ja">Japanese</SelectItem>
                <SelectItem value="zh_CN">Chinese (Simplified)</SelectItem>
                <SelectItem value="zh_TW">Chinese (Traditional)</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <InputField label="Email From" value={form.email_from} onChange={(v) => { updateField("email_from", v); }} disabled={!canEdit} placeholder="admin@example.com" />
          <InputField label="HTTP Proxy" value={form.http_proxy} onChange={(v) => { updateField("http_proxy", v); }} disabled={!canEdit} placeholder="http://proxy:3128" />
          <InputField label="MAC Prefix" value={form.mac_prefix} onChange={(v) => { updateField("mac_prefix", v); }} disabled={!canEdit} placeholder="BC:24:11" />

          <div className="space-y-2">
            <Label className="text-sm font-medium">Migration Type</Label>
            <Select value={form.migration_type || "__default__"} onValueChange={(v) => { updateField("migration_type", v === "__default__" ? "" : v); }} disabled={!canEdit}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">Default (secure)</SelectItem>
                <SelectItem value="secure">Secure (encrypted)</SelectItem>
                <SelectItem value="insecure">Insecure (fast)</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <InputField label="Bandwidth Limit (KB/s)" value={form.bwlimit} onChange={(v) => { updateField("bwlimit", v); }} disabled={!canEdit} placeholder="clone=0,migration=0,move=0,restore=0" />
          <InputField label="Next VMID Range" value={form.nextId} onChange={(v) => { updateField("nextId", v); }} disabled={!canEdit} placeholder="lower=100,upper=1000000" />
          <InputField label="HA Shutdown Policy" value={form.ha} onChange={(v) => { updateField("ha", v); }} disabled={!canEdit} placeholder="shutdown_policy=conditional" />

          <div className="space-y-2">
            <Label className="text-sm font-medium">Fencing</Label>
            <Select value={form.fencing || "__default__"} onValueChange={(v) => { updateField("fencing", v === "__default__" ? "" : v); }} disabled={!canEdit}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">Default (watchdog)</SelectItem>
                <SelectItem value="watchdog">Watchdog</SelectItem>
                <SelectItem value="hardware">Hardware</SelectItem>
                <SelectItem value="both">Both</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label className="text-sm font-medium">Cluster Resource Scheduling</Label>
            <Select value={form.crs || "__default__"} onValueChange={(v) => { updateField("crs", v === "__default__" ? "" : v); }} disabled={!canEdit}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">Default (static)</SelectItem>
                <SelectItem value="ha=static">Static</SelectItem>
                <SelectItem value="ha=basic">Basic</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label className="text-sm font-medium">Max Workers</Label>
            <Input
              type="number"
              min={0}
              value={form.max_workers}
              onChange={(e) => { updateField("max_workers", parseInt(e.target.value, 10) || 0); }}
              disabled={!canEdit}
              placeholder="4"
            />
            <p className="text-xs text-muted-foreground">Max parallel worker processes (0 = auto)</p>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function InputField({ label, value, onChange, disabled, placeholder }: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  disabled: boolean;
  placeholder?: string;
}) {
  return (
    <div className="space-y-2">
      <Label className="text-sm font-medium">{label}</Label>
      <Input
        value={value}
        onChange={(e) => { onChange(e.target.value); }}
        disabled={disabled}
        placeholder={placeholder}
      />
    </div>
  );
}

// --- Tags Section ---

function TagsSection({
  tagsQuery, updateTags, canEdit,
}: {
  tagsQuery: ReturnType<typeof useClusterTags>;
  updateTags: ReturnType<typeof useUpdateClusterTags>;
  canEdit: boolean;
}) {
  const [tagInput, setTagInput] = useState("");
  const [tagAccess, setTagAccess] = useState("");
  const [initialized, setInitialized] = useState(false);

  useEffect(() => {
    if (tagsQuery.data && !initialized) {
      setTagInput(tagsQuery.data.registered_tags);
      setTagAccess(toStr(tagsQuery.data.user_tag_access));
      setInitialized(true);
    }
  }, [tagsQuery.data, initialized]);

  const handleSave = () => {
    const params: { registered_tags?: string; user_tag_access?: string } = {};
    if (tagInput) params.registered_tags = tagInput;
    if (tagAccess) params.user_tag_access = tagAccess;
    updateTags.mutate(params);
  };

  return (
    <Card>
      <CardHeader><CardTitle>Tags Management</CardTitle></CardHeader>
      <CardContent className="space-y-4">
        {tagsQuery.isError && <ErrorBanner error={tagsQuery.error} />}
        {!tagsQuery.isError && (
          <>
            <div className="space-y-2">
              <Label>Registered Tags (semicolon-separated)</Label>
              <Input
                value={tagInput}
                onChange={(e) => { setTagInput(e.target.value); }}
                placeholder="tag1;tag2;tag3"
                disabled={!canEdit}
              />
            </div>
            <div className="space-y-2">
              <Label>User Tag Access</Label>
              <Select value={tagAccess || "__default__"} onValueChange={(v) => { setTagAccess(v === "__default__" ? "" : v); }} disabled={!canEdit}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">Default (free)</SelectItem>
                  <SelectItem value="free">Free</SelectItem>
                  <SelectItem value="list">List (from registered)</SelectItem>
                  <SelectItem value="existing">Existing (already used)</SelectItem>
                  <SelectItem value="none">None (disabled)</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {tagInput && (
              <div className="flex flex-wrap gap-1">
                {tagInput.split(";").filter(Boolean).map((tag) => (
                  <Badge key={tag} variant="secondary">{tag.trim()}</Badge>
                ))}
              </div>
            )}
            {canEdit && (
              <Button onClick={handleSave} disabled={updateTags.isPending} size="sm">
                <Save className="mr-2 h-4 w-4" />
                {updateTags.isPending ? "Saving..." : "Save Tags"}
              </Button>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// --- Join Info Section ---

function buildJoinCommand(data: NonNullable<ReturnType<typeof useClusterJoinInfo>["data"]>): string {
  const parts: string[] = [];
  const firstNode = data.nodelist?.[0];
  const peerAddr = firstNode?.pve_addr ?? firstNode?.ring0_addr ?? "";

  if (peerAddr) parts.push(`  peer ${peerAddr}`);
  if (data.fingerprint) parts.push(`  fingerprint ${data.fingerprint}`);
  parts.push("  password <PASSWORD>");

  return `pvecm add ${peerAddr} \\\n${parts.join(" \\\n")}`;
}

function JoinInfoSection({
  joinQuery,
}: {
  joinQuery: ReturnType<typeof useClusterJoinInfo>;
}) {
  const [copied, setCopied] = useState(false);

  const copyToClipboard = (text: string) => {
    void navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => { setCopied(false); }, 2000);
  };

  const data = joinQuery.data;
  const hasJoinInfo = data != null && (data.fingerprint || (data.nodelist && data.nodelist.length > 0));

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>Join Info</CardTitle>
          {hasJoinInfo && (
            <Dialog>
              <DialogTrigger asChild>
                <Button variant="outline" size="sm">
                  <Info className="mr-2 h-4 w-4" />
                  Join Information
                </Button>
              </DialogTrigger>
              <DialogContent className="max-w-2xl">
                <DialogHeader>
                  <DialogTitle>Cluster Join Information</DialogTitle>
                </DialogHeader>
                <div className="space-y-4">
                  <p className="text-sm text-muted-foreground">
                    Use this information to join additional nodes to this cluster.
                    Run the command below on the node you want to add.
                  </p>

                  <div className="space-y-2">
                    <Label className="text-sm font-medium">Join Command</Label>
                    <div className="relative">
                      <pre className="rounded-md bg-muted p-4 text-sm font-mono overflow-x-auto whitespace-pre-wrap break-all">
                        {buildJoinCommand(data)}
                      </pre>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="absolute right-2 top-2"
                        onClick={() => { copyToClipboard(buildJoinCommand(data)); }}
                      >
                        {copied ? <Check className="h-4 w-4 text-green-500" /> : <Copy className="h-4 w-4" />}
                      </Button>
                    </div>
                  </div>

                  {data.fingerprint && (
                    <div className="space-y-2">
                      <Label className="text-sm font-medium">Fingerprint</Label>
                      <div className="flex items-center gap-2">
                        <code className="flex-1 rounded bg-muted px-3 py-2 text-xs font-mono break-all select-all">
                          {data.fingerprint}
                        </code>
                        <Button variant="ghost" size="sm" onClick={() => { copyToClipboard(data.fingerprint ?? ""); }}>
                          <Copy className="h-4 w-4" />
                        </Button>
                      </div>
                    </div>
                  )}

                  {data.config_digest && (
                    <div className="space-y-2">
                      <Label className="text-sm font-medium">Config Digest</Label>
                      <code className="block rounded bg-muted px-3 py-2 text-xs font-mono">{data.config_digest}</code>
                    </div>
                  )}

                  {data.nodelist && data.nodelist.length > 0 && (
                    <div className="space-y-2">
                      <Label className="text-sm font-medium">Peer Nodes</Label>
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>Name</TableHead>
                            <TableHead>Node ID</TableHead>
                            <TableHead>Address</TableHead>
                            <TableHead>Ring 0</TableHead>
                            <TableHead>Fingerprint</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.nodelist.map((n) => (
                            <TableRow key={n.name}>
                              <TableCell className="font-medium">{n.name}</TableCell>
                              <TableCell>{n.nodeid}</TableCell>
                              <TableCell>{n.pve_addr}</TableCell>
                              <TableCell>{n.ring0_addr}</TableCell>
                              <TableCell className="font-mono text-xs max-w-[200px] truncate" title={n.pve_fp}>{n.pve_fp}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  )}
                </div>
              </DialogContent>
            </Dialog>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {joinQuery.isLoading && <Skeleton className="h-16 w-full" />}
        {joinQuery.isError && <ErrorBanner error={joinQuery.error} />}
        {!joinQuery.isLoading && !joinQuery.isError && hasJoinInfo && (
          <div className="space-y-3 text-sm">
            {data.fingerprint && (
              <div className="space-y-1">
                <Label className="text-muted-foreground">Cluster Fingerprint</Label>
                <code className="block rounded bg-muted px-3 py-2 text-xs font-mono break-all select-all">
                  {data.fingerprint}
                </code>
              </div>
            )}
            {data.nodelist && data.nodelist.length > 0 && (
              <div className="space-y-1">
                <Label className="text-muted-foreground">Peer Nodes ({data.nodelist.length})</Label>
                <div className="flex flex-wrap gap-2">
                  {data.nodelist.map((n) => (
                    <Badge key={n.name} variant="secondary" className="font-mono text-xs">
                      {n.name} ({n.pve_addr ?? n.ring0_addr})
                    </Badge>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
        {!joinQuery.isLoading && !joinQuery.isError && !hasJoinInfo && (
          <p className="text-sm text-muted-foreground">No join info available. This may be a standalone node.</p>
        )}
      </CardContent>
    </Card>
  );
}

// --- Corosync Nodes Section ---

function CorosyncNodesSection({
  nodesQuery,
}: {
  nodesQuery: ReturnType<typeof useCorosyncNodes>;
}) {
  return (
    <Card>
      <CardHeader><CardTitle>Corosync Nodes</CardTitle></CardHeader>
      <CardContent>
        {nodesQuery.isLoading && <Skeleton className="h-20 w-full" />}
        {nodesQuery.isError && <ErrorBanner error={nodesQuery.error} />}
        {!nodesQuery.isLoading && !nodesQuery.isError && nodesQuery.data != null && nodesQuery.data.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Node ID</TableHead>
                <TableHead>PVE Address</TableHead>
                <TableHead>Ring 0</TableHead>
                <TableHead>Votes</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {nodesQuery.data.map((n) => (
                <TableRow key={n.name}>
                  <TableCell className="font-medium">{n.name}</TableCell>
                  <TableCell>{n.nodeid}</TableCell>
                  <TableCell>{n.pve_addr}</TableCell>
                  <TableCell>{n.ring0_addr}</TableCell>
                  <TableCell>{n.quorum_votes}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {!nodesQuery.isLoading && !nodesQuery.isError && (nodesQuery.data == null || nodesQuery.data.length === 0) && (
          <p className="text-sm text-muted-foreground">No corosync nodes found. This may be a standalone node.</p>
        )}
      </CardContent>
    </Card>
  );
}
