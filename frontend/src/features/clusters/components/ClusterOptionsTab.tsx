import { useState, useEffect, useCallback } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
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
import { AlertTriangle, Check, CheckCircle2, Copy, Info, Save } from "lucide-react";
import { ApiClientError } from "@/lib/api-client";
import { useAuth } from "@/hooks/useAuth";
import {
  useClusterOptions, useUpdateClusterOptions,
  useClusterDescription, useUpdateClusterDescription,
  useClusterTags, useUpdateClusterTags,
  useClusterJoinInfo, useCorosyncNodes,
} from "../api/cluster-options-queries";
import type { ClusterOptions } from "../api/cluster-options-queries";
import {
  parsePropString,
  propStringsEqual,
  serializePropString,
} from "../lib/prop-string";
import { useNetworkInterfaces } from "@/features/networks/api/network-queries";
import { isPVEAtLeast, PVE_FEATURES } from "@/lib/pve-version";

/** Compute the network address of an IPv4 CIDR, e.g.
 *  "10.10.10.5/24" → "10.10.10.0/24". Returns null on invalid input or
 *  IPv6 (which we don't currently bucket). */
function ipv4Network(cidr: string): string | null {
  const slash = cidr.indexOf("/");
  if (slash < 0) return null;
  const ip = cidr.slice(0, slash);
  const prefix = Number.parseInt(cidr.slice(slash + 1), 10);
  if (!Number.isFinite(prefix) || prefix < 0 || prefix > 32) return null;
  const octets = ip.split(".");
  if (octets.length !== 4) return null;
  const nums = octets.map((o) => Number.parseInt(o, 10));
  if (nums.some((n) => !Number.isFinite(n) || n < 0 || n > 255)) return null;
  const [a, b, c, d] = nums;
  if (a === undefined || b === undefined || c === undefined || d === undefined) return null;
  const ipNum = ((a << 24) | (b << 16) | (c << 8) | d) >>> 0;
  const mask = prefix === 0 ? 0 : (~0 << (32 - prefix)) >>> 0;
  const net = (ipNum & mask) >>> 0;
  return `${String((net >>> 24) & 0xff)}.${String((net >>> 16) & 0xff)}.${String((net >>> 8) & 0xff)}.${String(net & 0xff)}/${String(prefix)}`;
}

interface OptionsFormData {
  console: string;
  keyboard: string;
  language: string;
  email_from: string;
  http_proxy: string;
  mac_prefix: string;
  fencing: string;
  max_workers: number;
  // migration property string subfields (PVE 8+ replaces standalone `migration_type`)
  migration_type: string;
  migration_network: string;
  // HA property string subfields
  ha_shutdown_policy: string;
  // CRS property string subfields
  crs_ha: string;
  crs_rebalance_on_start: boolean;
  // CRS dynamic load-balancer subfields (PVE 9.2+)
  crs_auto_rebalance: boolean;
  crs_threshold: string;
  crs_hold_duration: string;
  crs_margin: string;
  crs_method: string;
  // bwlimit property string subfields (KiB/s, "" = unset, "0" = unlimited)
  bwlimit_clone: string;
  bwlimit_migration: string;
  bwlimit_move: string;
  bwlimit_restore: string;
  bwlimit_default: string;
  // next-id property string subfields
  next_id_lower: string;
  next_id_upper: string;
}

const EMPTY_FORM: OptionsFormData = {
  console: "", keyboard: "", language: "", email_from: "",
  http_proxy: "", mac_prefix: "", fencing: "",
  max_workers: 0,
  migration_type: "", migration_network: "",
  ha_shutdown_policy: "",
  crs_ha: "", crs_rebalance_on_start: false,
  crs_auto_rebalance: false, crs_threshold: "", crs_hold_duration: "",
  crs_margin: "", crs_method: "",
  bwlimit_clone: "", bwlimit_migration: "", bwlimit_move: "",
  bwlimit_restore: "", bwlimit_default: "",
  next_id_lower: "", next_id_upper: "",
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
  const migration = parsePropString(toStr(d["migration"]));
  const ha = parsePropString(toStr(d["ha"]));
  const crs = parsePropString(toStr(d["crs"]));
  const bw = parsePropString(toStr(d["bwlimit"]));
  const nextId = parsePropString(toStr(d["next-id"]));

  return {
    console: toStr(d["console"]),
    keyboard: toStr(d["keyboard"]),
    language: toStr(d["language"]),
    email_from: toStr(d["email_from"]),
    http_proxy: toStr(d["http_proxy"]),
    mac_prefix: toStr(d["mac_prefix"]),
    fencing: toStr(d["fencing"]),
    max_workers: typeof d["max_workers"] === "number" ? d["max_workers"] : 0,

    // PVE 8+ stores migration type inside the `migration` property string;
    // older clusters may still have a top-level `migration_type` field.
    migration_type: migration["type"] ?? toStr(d["migration_type"]),
    migration_network: migration["network"] ?? "",

    ha_shutdown_policy: ha["shutdown_policy"] ?? "",

    crs_ha: crs["ha"] ?? "",
    crs_rebalance_on_start: crs["ha-rebalance-on-start"] === "1",
    crs_auto_rebalance: crs["ha-auto-rebalance"] === "1",
    crs_threshold: crs["ha-auto-rebalance-threshold"] ?? "",
    crs_hold_duration: crs["ha-auto-rebalance-hold-duration"] ?? "",
    crs_margin: crs["ha-auto-rebalance-margin"] ?? "",
    crs_method: crs["ha-auto-rebalance-method"] ?? "",

    bwlimit_clone: bw["clone"] ?? "",
    bwlimit_migration: bw["migration"] ?? "",
    bwlimit_move: bw["move"] ?? "",
    bwlimit_restore: bw["restore"] ?? "",
    bwlimit_default: bw["default"] ?? "",

    next_id_lower: nextId["lower"] ?? "",
    next_id_upper: nextId["upper"] ?? "",
  };
}

/** Build the property-string values from the form (used for both
 *  diffing against the original and for sending the PUT body). */
function buildPropStrings(form: OptionsFormData): {
  migration: string;
  ha: string;
  crs: string;
  bwlimit: string;
  nextId: string;
} {
  return {
    migration: serializePropString({
      type: form.migration_type,
      network: form.migration_network,
    }),
    ha: serializePropString({ shutdown_policy: form.ha_shutdown_policy }),
    crs: serializePropString({
      ha: form.crs_ha,
      "ha-rebalance-on-start": form.crs_rebalance_on_start ? "1" : "",
      // Auto-rebalance keys apply only to the dynamic scheduler; omit them
      // otherwise so Proxmox doesn't reject ha-auto-rebalance with a
      // non-dynamic scheduler. (serializePropString drops empty values.)
      ...(form.crs_ha === "dynamic"
        ? {
            "ha-auto-rebalance": form.crs_auto_rebalance ? "1" : "",
            "ha-auto-rebalance-threshold": form.crs_auto_rebalance ? form.crs_threshold : "",
            "ha-auto-rebalance-hold-duration": form.crs_auto_rebalance ? form.crs_hold_duration : "",
            "ha-auto-rebalance-margin": form.crs_auto_rebalance ? form.crs_margin : "",
            "ha-auto-rebalance-method": form.crs_auto_rebalance ? form.crs_method : "",
          }
        : {}),
    }),
    bwlimit: serializePropString({
      clone: form.bwlimit_clone,
      migration: form.bwlimit_migration,
      move: form.bwlimit_move,
      restore: form.bwlimit_restore,
      default: form.bwlimit_default,
    }),
    nextId: serializePropString({
      lower: form.next_id_lower,
      upper: form.next_id_upper,
    }),
  };
}

interface ClusterOptionsTabProps {
  clusterId: string;
  pveVersion: string;
}

function ErrorBanner({ error }: { error: Error }) {
  const message = error.message || "Failed to load data";
  const isForbidden = message.includes("403") || message.toLowerCase().includes("permission") || message.toLowerCase().includes("forbidden");
  return (
    <div className="flex items-center gap-2 rounded-md border border-orange-300 bg-orange-50 p-3 text-sm text-orange-800 dark:border-orange-700 dark:bg-orange-950 dark:text-orange-200">
      <AlertTriangle className="h-4 w-4 shrink-0" />
      <span>
        {isForbidden
          ? "The Proxmox API token does not have permission to access this data. Ensure the token has Sys.Audit privilege on /."
          : message}
      </span>
    </div>
  );
}

export function ClusterOptionsTab({ clusterId, pveVersion }: ClusterOptionsTabProps) {
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
        <GeneralSection
          optionsQuery={optionsQuery}
          updateOpts={updateOpts}
          canEdit={canManage("cluster")}
          clusterId={clusterId}
          pveVersion={pveVersion}
        />
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
  optionsQuery, updateOpts, canEdit, clusterId, pveVersion,
}: {
  optionsQuery: ReturnType<typeof useClusterOptions>;
  updateOpts: ReturnType<typeof useUpdateClusterOptions>;
  canEdit: boolean;
  clusterId: string;
  pveVersion: string;
}) {
  const crsDynamicSupported = isPVEAtLeast(pveVersion, PVE_FEATURES.CRS_DYNAMIC);
  const [form, setForm] = useState<OptionsFormData>({ ...EMPTY_FORM });
  const [dirty, setDirty] = useState(false);
  const [feedback, setFeedback] = useState<{ kind: "success" | "error"; message: string } | null>(null);
  const networksQuery = useNetworkInterfaces(clusterId);

  // Unique IPv4 network CIDRs across all node interfaces, used as
  // autocomplete suggestions for the Migration Network field — mirrors
  // Proxmox's own Datacenter → Options → Migration Settings dropdown.
  const networkSuggestions = (() => {
    const seen = new Set<string>();
    for (const node of networksQuery.data ?? []) {
      for (const iface of node.interfaces) {
        if (iface.cidr) {
          const net = ipv4Network(iface.cidr);
          if (net) seen.add(net);
        }
      }
    }
    return Array.from(seen).sort();
  })();

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
    setFeedback(null);
    const origData = optionsQuery.data;
    const orig = origData ? optsToForm(origData) : EMPTY_FORM;
    const params: Record<string, unknown> = {};
    const toDelete: string[] = [];

    // Diff a simple string field. Empty value means "clear" — Proxmox
    // requires this to be sent via the `delete` parameter, not as an
    // empty form value (which it 500s on).
    const diffStr = (cur: string, prev: string, key: string) => {
      if (cur === prev) return;
      if (cur === "") toDelete.push(key);
      else params[key] = cur;
    };
    const diffProp = (cur: string, prev: string, key: string) => {
      if (propStringsEqual(cur, prev)) return;
      if (cur === "") toDelete.push(key);
      else params[key] = cur;
    };

    diffStr(form.console, orig.console, "console");
    diffStr(form.keyboard, orig.keyboard, "keyboard");
    diffStr(form.language, orig.language, "language");
    diffStr(form.email_from, orig.email_from, "email_from");
    diffStr(form.http_proxy, orig.http_proxy, "http_proxy");
    diffStr(form.mac_prefix, orig.mac_prefix, "mac_prefix");
    diffStr(form.fencing, orig.fencing, "fencing");
    if (form.max_workers !== orig.max_workers) params["max_workers"] = form.max_workers;

    // Property-string fields: rebuild from subfields, compare to original
    // raw value (whitespace + key-order tolerant) so we only PUT when the
    // user actually changed something.
    const next = buildPropStrings(form);
    const origRaw = {
      migration: toStr(origData?.["migration"]),
      ha: toStr(origData?.["ha"]),
      crs: toStr(origData?.["crs"]),
      bwlimit: toStr(origData?.["bwlimit"]),
      nextId: toStr(origData?.["next-id"]),
    };
    diffProp(next.migration, origRaw.migration, "migration");
    diffProp(next.ha, origRaw.ha, "ha");
    diffProp(next.crs, origRaw.crs, "crs");
    diffProp(next.bwlimit, origRaw.bwlimit, "bwlimit");
    diffProp(next.nextId, origRaw.nextId, "next-id");

    if (toDelete.length > 0) params["delete"] = toDelete.join(",");

    if (Object.keys(params).length === 0) {
      setDirty(false);
      setFeedback({ kind: "success", message: "No changes to save." });
      return;
    }

    updateOpts.mutate(params, {
      onSuccess: () => {
        setDirty(false);
        setFeedback({ kind: "success", message: "Datacenter options saved." });
      },
      onError: (err) => {
        const msg = err instanceof ApiClientError ? err.body.message
          : err instanceof Error ? err.message : "Save failed";
        setFeedback({ kind: "error", message: msg });
      },
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
        {feedback && (
          <div
            className={
              feedback.kind === "success"
                ? "mb-4 flex items-start gap-2 rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-800 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-200"
                : "mb-4 flex items-start gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive"
            }
          >
            {feedback.kind === "success"
              ? <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
              : <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />}
            <span>{feedback.message}</span>
          </div>
        )}
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

          <div className="space-y-2">
            <Label className="text-sm font-medium">Migration Network (CIDR)</Label>
            <Input
              value={form.migration_network}
              onChange={(e) => { updateField("migration_network", e.target.value); }}
              disabled={!canEdit}
              placeholder={networkSuggestions[0] ?? "10.10.10.0/24"}
              list="cluster-migration-networks"
            />
            <datalist id="cluster-migration-networks">
              {networkSuggestions.map((cidr) => (
                <option key={cidr} value={cidr} />
              ))}
            </datalist>
            {networkSuggestions.length > 0 && (
              <div className="flex flex-wrap items-center gap-1.5 text-xs">
                <span className="text-muted-foreground">From cluster:</span>
                {networkSuggestions.map((cidr) => (
                  <button
                    key={cidr}
                    type="button"
                    disabled={!canEdit}
                    onClick={() => { updateField("migration_network", cidr); }}
                    className="rounded-md border bg-muted px-2 py-0.5 font-mono text-xs hover:bg-muted/70 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {cidr}
                  </button>
                ))}
              </div>
            )}
          </div>

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

        {/* High Availability */}
        <Section title="High Availability">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-2">
              <Label className="text-sm font-medium">Shutdown Policy</Label>
              <Select
                value={form.ha_shutdown_policy || "__default__"}
                onValueChange={(v) => { updateField("ha_shutdown_policy", v === "__default__" ? "" : v); }}
                disabled={!canEdit}
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">Default (conditional)</SelectItem>
                  <SelectItem value="freeze">Freeze — pause services on shutdown</SelectItem>
                  <SelectItem value="failover">Failover — stop and let HA recover</SelectItem>
                  <SelectItem value="migrate">Migrate — evacuate HA services first</SelectItem>
                  <SelectItem value="conditional">Conditional — depends on action</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                What HA does when a node shuts down or reboots. Set to <code>migrate</code> if you want HA-managed VMs evacuated automatically.
              </p>
            </div>
          </div>
        </Section>

        {/* Cluster Resource Scheduling */}
        <Section title="Cluster Resource Scheduling (CRS)">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-2">
              <Label className="text-sm font-medium">Scheduler</Label>
              <Select
                value={form.crs_ha || "__default__"}
                onValueChange={(v) => { updateField("crs_ha", v === "__default__" ? "" : v); }}
                disabled={!canEdit}
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">Default (basic)</SelectItem>
                  <SelectItem value="basic">Basic — round-robin failover</SelectItem>
                  <SelectItem value="static">Static — assignment-based</SelectItem>
                  {(crsDynamicSupported || form.crs_ha === "dynamic") && (
                    <SelectItem value="dynamic">Dynamic — live load balancing</SelectItem>
                  )}
                </SelectContent>
              </Select>
              {!crsDynamicSupported && (
                <p className="text-xs text-muted-foreground">
                  Dynamic load balancing requires Proxmox VE 9.2 or newer.
                </p>
              )}
            </div>
            <div className="flex items-center justify-between rounded-md border p-3">
              <div>
                <Label className="text-sm font-medium">Rebalance on Start</Label>
                <p className="text-xs text-muted-foreground">Re-evaluate placement when the cluster comes online.</p>
              </div>
              <Switch
                checked={form.crs_rebalance_on_start}
                onCheckedChange={(v) => { updateField("crs_rebalance_on_start", v); }}
                disabled={!canEdit}
              />
            </div>
          </div>
          {form.crs_ha === "dynamic" && (
            <div className="mt-4 space-y-4 rounded-md border p-4">
              <div className="flex items-center justify-between">
                <div>
                  <Label className="text-sm font-medium">Automatic Rebalancing</Label>
                  <p className="text-xs text-muted-foreground">
                    Let Proxmox live-migrate HA-managed guests to even out node load. While this is on,
                    Nexara DRS automatic mode is disabled to avoid conflicting migrations.
                  </p>
                </div>
                <Switch
                  checked={form.crs_auto_rebalance}
                  onCheckedChange={(v) => { updateField("crs_auto_rebalance", v); }}
                  disabled={!canEdit}
                />
              </div>
              {form.crs_auto_rebalance && (
                <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                  <NumField label="Threshold %" value={form.crs_threshold} onChange={(v) => { updateField("crs_threshold", v); }} disabled={!canEdit} placeholder="30" />
                  <NumField label="Hold (rounds)" value={form.crs_hold_duration} onChange={(v) => { updateField("crs_hold_duration", v); }} disabled={!canEdit} placeholder="3" />
                  <NumField label="Margin %" value={form.crs_margin} onChange={(v) => { updateField("crs_margin", v); }} disabled={!canEdit} placeholder="10" />
                  <div className="space-y-2">
                    <Label className="text-sm font-medium">Method</Label>
                    <Select
                      value={form.crs_method || "__default__"}
                      onValueChange={(v) => { updateField("crs_method", v === "__default__" ? "" : v); }}
                      disabled={!canEdit}
                    >
                      <SelectTrigger><SelectValue /></SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__default__">Default (bruteforce)</SelectItem>
                        <SelectItem value="bruteforce">Bruteforce</SelectItem>
                        <SelectItem value="topsis">TOPSIS</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                </div>
              )}
            </div>
          )}
        </Section>

        {/* Bandwidth Limits */}
        <Section title="Bandwidth Limits (KiB/s)">
          <p className="text-xs text-muted-foreground mb-3">Per-operation rate limits. Leave blank to use Proxmox defaults; <code>0</code> means unlimited.</p>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
            <NumField label="Default" value={form.bwlimit_default} onChange={(v) => { updateField("bwlimit_default", v); }} disabled={!canEdit} />
            <NumField label="Clone" value={form.bwlimit_clone} onChange={(v) => { updateField("bwlimit_clone", v); }} disabled={!canEdit} />
            <NumField label="Migration" value={form.bwlimit_migration} onChange={(v) => { updateField("bwlimit_migration", v); }} disabled={!canEdit} />
            <NumField label="Move" value={form.bwlimit_move} onChange={(v) => { updateField("bwlimit_move", v); }} disabled={!canEdit} />
            <NumField label="Restore" value={form.bwlimit_restore} onChange={(v) => { updateField("bwlimit_restore", v); }} disabled={!canEdit} />
          </div>
        </Section>

        {/* Next VMID Range */}
        <Section title="Next VMID Range">
          <p className="text-xs text-muted-foreground mb-3">Inclusive lower / upper bounds used by Proxmox when assigning a new VMID automatically.</p>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <NumField label="Lower" value={form.next_id_lower} onChange={(v) => { updateField("next_id_lower", v); }} disabled={!canEdit} placeholder="100" />
            <NumField label="Upper" value={form.next_id_upper} onChange={(v) => { updateField("next_id_upper", v); }} disabled={!canEdit} placeholder="1000000" />
          </div>
        </Section>
      </CardContent>
    </Card>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mt-6 border-t pt-4">
      <h3 className="mb-3 text-sm font-semibold">{title}</h3>
      {children}
    </div>
  );
}

function NumField({ label, value, onChange, disabled, placeholder }: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  disabled: boolean;
  placeholder?: string;
}) {
  return (
    <div className="space-y-1">
      <Label className="text-xs font-medium">{label}</Label>
      <Input
        type="number"
        min={0}
        value={value}
        onChange={(e) => { onChange(e.target.value); }}
        disabled={disabled}
        placeholder={placeholder}
      />
    </div>
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
                        {copied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}
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
