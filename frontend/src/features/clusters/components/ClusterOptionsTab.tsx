import { useState, useEffect, useCallback, useId } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { Segmented } from "@/components/ui/segmented";
import { Panel } from "@/components/page/Panel";
import {
  SettingRow,
  SettingsFooter,
  SettingsSection,
} from "@/components/page/Settings";
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Copy,
  Info,
  Save,
} from "lucide-react";
import { ApiClientError } from "@/lib/api-client";
import { useAuth } from "@/hooks/useAuth";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import {
  useClusterOptions,
  useUpdateClusterOptions,
  useClusterDescription,
  useUpdateClusterDescription,
  useClusterTags,
  useUpdateClusterTags,
  useClusterJoinInfo,
  useCorosyncNodes,
} from "../api/cluster-options-queries";
import type { ClusterOptions } from "../api/cluster-options-queries";
import {
  parsePropString,
  propStringsEqual,
  serializePropString,
} from "../lib/prop-string";
import { useNetworkInterfaces } from "@/features/networks/api/network-queries";
import { isPVEAtLeast, PVE_FEATURES } from "@/lib/pve-version";
import { useTranslation } from "react-i18next";

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
  if (a === undefined || b === undefined || c === undefined || d === undefined)
    return null;
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
  console: "",
  keyboard: "",
  language: "",
  email_from: "",
  http_proxy: "",
  mac_prefix: "",
  fencing: "",
  max_workers: 0,
  migration_type: "",
  migration_network: "",
  ha_shutdown_policy: "",
  crs_ha: "",
  crs_rebalance_on_start: false,
  crs_auto_rebalance: false,
  crs_threshold: "",
  crs_hold_duration: "",
  crs_margin: "",
  crs_method: "",
  bwlimit_clone: "",
  bwlimit_migration: "",
  bwlimit_move: "",
  bwlimit_restore: "",
  bwlimit_default: "",
  next_id_lower: "",
  next_id_upper: "",
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
            "ha-auto-rebalance-threshold": form.crs_auto_rebalance
              ? form.crs_threshold
              : "",
            "ha-auto-rebalance-hold-duration": form.crs_auto_rebalance
              ? form.crs_hold_duration
              : "",
            "ha-auto-rebalance-margin": form.crs_auto_rebalance
              ? form.crs_margin
              : "",
            "ha-auto-rebalance-method": form.crs_auto_rebalance
              ? form.crs_method
              : "",
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

/** A read that failed, said in the section it belongs to. */
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
          ? "The Proxmox API token does not have permission to access this data. Ensure the token has Sys.Audit privilege on /."
          : message}
      </span>
    </p>
  );
}

/** The views of the tab, in the order the switch shows them. */
const OPTIONS_VIEWS = [
  { value: "notes", label: "Notes" },
  { value: "general", label: "General" },
  { value: "tags", label: "Tags" },
  { value: "info", label: "Cluster info" },
] as const;

export function ClusterOptionsTab({
  clusterId,
  pveVersion,
}: ClusterOptionsTabProps) {
  const { canManage } = useAuth();
  const optionsQuery = useClusterOptions(clusterId);
  const descQuery = useClusterDescription(clusterId);
  const tagsQuery = useClusterTags(clusterId);
  const joinQuery = useClusterJoinInfo(clusterId);
  const nodesQuery = useCorosyncNodes(clusterId);
  const updateOpts = useUpdateClusterOptions(clusterId);
  const updateDesc = useUpdateClusterDescription(clusterId);
  const updateTags = useUpdateClusterTags(clusterId);

  // The panels are Radix tab panels, so only the view on show is mounted;
  // the switch above them is what picks it.
  const [view, setView] = useState("notes");
  const history = clusterHistories.options(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  const shown = view === "history" && !canHistory ? "notes" : view;

  return (
    <Tabs value={shown} onValueChange={setView}>
      <Segmented
        aria-label="Options view"
        size="md"
        value={shown}
        onValueChange={setView}
        options={[
          ...OPTIONS_VIEWS,
          ...(canHistory ? [{ value: "history", label: "History" }] : []),
        ]}
      />

      {/* Settings read as one column: a label and its control stay close.
          Each panel is named by its view: a dialog whose opener is gone when
          it closes sends focus to the panel. */}
      <TabsContent
        value="notes"
        aria-label="Notes"
        aria-labelledby={undefined}
        className="mt-4 max-w-4xl"
      >
        <NotesSection
          descQuery={descQuery}
          updateDesc={updateDesc}
          canEdit={canManage("cluster")}
        />
      </TabsContent>

      <TabsContent
        value="general"
        aria-label="General"
        aria-labelledby={undefined}
        className="mt-4 max-w-4xl"
      >
        <GeneralSection
          optionsQuery={optionsQuery}
          updateOpts={updateOpts}
          canEdit={canManage("cluster")}
          clusterId={clusterId}
          pveVersion={pveVersion}
        />
      </TabsContent>

      <TabsContent
        value="tags"
        aria-label="Tags"
        aria-labelledby={undefined}
        className="mt-4 max-w-4xl"
      >
        <TagsSection
          tagsQuery={tagsQuery}
          updateTags={updateTags}
          canEdit={canManage("cluster")}
        />
      </TabsContent>

      <TabsContent
        value="info"
        aria-label="Cluster info"
        aria-labelledby={undefined}
        className="mt-4 space-y-4"
      >
        <JoinInfoSection joinQuery={joinQuery} />
        <CorosyncNodesSection nodesQuery={nodesQuery} />
      </TabsContent>

      <HistoryTabContent history={history} label="History" />
    </Tabs>
  );
}

// --- Notes Section ---

function NotesSection({
  descQuery,
  updateDesc,
  canEdit,
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
      onSuccess: () => {
        setDirty(false);
      },
    });
  };

  return (
    <Panel title="Notes" aside="Kept as the datacenter's notes in Proxmox">
      <div className="space-y-3">
        {descQuery.isError && <ErrorBanner error={descQuery.error} />}
        <Textarea
          aria-label="Cluster notes"
          value={description}
          onChange={(e) => {
            setDescription(e.target.value);
            setDirty(true);
          }}
          placeholder="Enter cluster notes or description (supports markdown)..."
          rows={10}
          disabled={!canEdit || descQuery.isError}
        />
      </div>
      {canEdit && !descQuery.isError && (
        <SettingsFooter>
          <Button
            onClick={handleSave}
            disabled={!dirty || updateDesc.isPending}
            size="sm"
          >
            <Save className="mr-2 h-4 w-4" />
            {updateDesc.isPending ? "Saving..." : "Save description"}
          </Button>
        </SettingsFooter>
      )}
    </Panel>
  );
}

// --- General Options Section (Editable) ---

function GeneralSection({
  optionsQuery,
  updateOpts,
  canEdit,
  clusterId,
  pveVersion,
}: {
  optionsQuery: ReturnType<typeof useClusterOptions>;
  updateOpts: ReturnType<typeof useUpdateClusterOptions>;
  canEdit: boolean;
  clusterId: string;
  pveVersion: string;
}) {
  const { t } = useTranslation("clusters");
  const crsDynamicSupported = isPVEAtLeast(
    pveVersion,
    PVE_FEATURES.CRS_DYNAMIC,
  );
  const [form, setForm] = useState<OptionsFormData>({ ...EMPTY_FORM });
  const [dirty, setDirty] = useState(false);
  const [feedback, setFeedback] = useState<{
    kind: "success" | "error";
    message: string;
  } | null>(null);
  const networksQuery = useNetworkInterfaces(clusterId);
  // The section shows a failed save itself (the hook opts out of the app's
  // toast), and one that lands after it was left is toasted instead.
  const settle = useSaveOutcome(clusterId);
  // One prefix for the ids that tie each row's label to its control.
  const uid = useId();
  const id = (name: string) => `${uid}-${name}`;

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

  const updateField = useCallback(
    <K extends keyof OptionsFormData>(key: K, value: OptionsFormData[K]) => {
      setForm((prev) => ({ ...prev, [key]: value }));
      setDirty(true);
    },
    [],
  );

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
    if (form.max_workers !== orig.max_workers)
      params["max_workers"] = form.max_workers;

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

    settle(updateOpts.mutateAsync(params), {
      action: t("clusterOptionsTabActions.savingTheDatacenterOptions"),
      onSuccess: () => {
        setDirty(false);
        setFeedback({ kind: "success", message: "Datacenter options saved." });
      },
      onError: (err) => {
        const msg =
          err instanceof ApiClientError
            ? err.body.message
            : err instanceof Error
              ? err.message
              : "Save failed";
        setFeedback({ kind: "error", message: msg });
      },
    });
  };

  if (optionsQuery.isLoading) {
    return (
      <Panel title="Datacenter options">
        <div className="space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      </Panel>
    );
  }

  if (optionsQuery.isError) {
    return (
      <Panel title="Datacenter options">
        <ErrorBanner error={optionsQuery.error} />
      </Panel>
    );
  }

  /** A select whose first entry clears the option back to Proxmox's default. */
  const select = (
    name: keyof OptionsFormData,
    value: string,
    onChange: (v: string) => void,
    defaultLabel: string,
    options: readonly (readonly [value: string, label: string])[],
  ) => (
    <Select
      value={value || "__default__"}
      onValueChange={(v) => {
        onChange(v === "__default__" ? "" : v);
      }}
      disabled={!canEdit}
    >
      <SelectTrigger id={id(name)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="__default__">{defaultLabel}</SelectItem>
        {options.map(([v, label]) => (
          <SelectItem key={v} value={v}>
            {label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );

  /* One save for every section below, offered at both ends of a form this
     long: wherever the change was made, the button is a short scroll away. */
  const saveBar = (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <div className="min-w-0 flex-1 text-sm">
        {feedback === null ? (
          <p className="text-muted-foreground">
            Datacenter options apply to every node of this cluster.
            {dirty && " You have unsaved changes."}
          </p>
        ) : feedback.kind === "success" ? (
          <p className="flex items-start gap-2">
            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
            <span>{feedback.message}</span>
          </p>
        ) : (
          <p className="flex items-start gap-2 text-destructive">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{feedback.message}</span>
          </p>
        )}
      </div>
      {canEdit && (
        <Button
          onClick={handleSave}
          disabled={!dirty || updateOpts.isPending}
          size="sm"
        >
          <Save className="mr-2 h-4 w-4" />
          {updateOpts.isPending ? "Saving..." : "Save changes"}
        </Button>
      )}
    </div>
  );

  return (
    <div className="space-y-4">
      {saveBar}

      <SettingsSection
        title="Web console"
        description="Defaults for the Proxmox web interface and its consoles."
      >
        <SettingRow label="Console viewer" htmlFor={id("console")}>
          {select(
            "console",
            form.console,
            (v) => {
              updateField("console", v);
            },
            "Default",
            [
              ["applet", "Java applet (deprecated)"],
              ["vv", "SPICE (virt-viewer)"],
              ["html5", "HTML5 (noVNC)"],
              ["xtermjs", "xterm.js"],
            ],
          )}
        </SettingRow>
        <SettingRow label="Keyboard layout" htmlFor={id("keyboard")}>
          {select(
            "keyboard",
            form.keyboard,
            (v) => {
              updateField("keyboard", v);
            },
            "Default",
            [
              ["en-us", "English (US)"],
              ["en-gb", "English (UK)"],
              ["de", "German"],
              ["de-ch", "German (Swiss)"],
              ["fr", "French"],
              ["fr-ch", "French (Swiss)"],
              ["es", "Spanish"],
              ["it", "Italian"],
              ["ja", "Japanese"],
              ["pt-br", "Portuguese (Brazil)"],
              ["sv", "Swedish"],
              ["no", "Norwegian"],
              ["da", "Danish"],
              ["fi", "Finnish"],
              ["hu", "Hungarian"],
              ["pl", "Polish"],
              ["tr", "Turkish"],
            ],
          )}
        </SettingRow>
        <SettingRow label="Language" htmlFor={id("language")}>
          {select(
            "language",
            form.language,
            (v) => {
              updateField("language", v);
            },
            "Default",
            [
              ["en", "English"],
              ["de", "German"],
              ["fr", "French"],
              ["es", "Spanish"],
              ["it", "Italian"],
              ["ja", "Japanese"],
              ["zh_CN", "Chinese (Simplified)"],
              ["zh_TW", "Chinese (Traditional)"],
            ],
          )}
        </SettingRow>
      </SettingsSection>

      <SettingsSection title="Mail and proxy">
        <SettingRow
          label="Email from"
          description="The sender address of the mail Proxmox sends."
          htmlFor={id("email_from")}
        >
          <Input
            id={id("email_from")}
            value={form.email_from}
            onChange={(e) => {
              updateField("email_from", e.target.value);
            }}
            disabled={!canEdit}
            placeholder="admin@example.com"
          />
        </SettingRow>
        <SettingRow
          label="HTTP proxy"
          description="Used for downloads such as templates and updates."
          htmlFor={id("http_proxy")}
        >
          <Input
            id={id("http_proxy")}
            value={form.http_proxy}
            onChange={(e) => {
              updateField("http_proxy", e.target.value);
            }}
            disabled={!canEdit}
            placeholder="http://proxy:3128"
          />
        </SettingRow>
      </SettingsSection>

      <SettingsSection title="New guests">
        <SettingRow
          label="MAC prefix"
          description="The start of the MAC addresses Proxmox generates for guests."
          htmlFor={id("mac_prefix")}
        >
          <Input
            id={id("mac_prefix")}
            value={form.mac_prefix}
            onChange={(e) => {
              updateField("mac_prefix", e.target.value);
            }}
            disabled={!canEdit}
            placeholder="BC:24:11"
          />
        </SettingRow>
        <SettingRow
          label="Next VMID range"
          description="Inclusive lower and upper bounds used by Proxmox when assigning a new VMID automatically."
          htmlFor={id("next_id_lower")}
        >
          <NumField
            id={id("next_id_lower")}
            ariaLabel="Next VMID lower bound"
            value={form.next_id_lower}
            onChange={(v) => {
              updateField("next_id_lower", v);
            }}
            disabled={!canEdit}
            placeholder="100"
          />
          <span className="text-sm text-muted-foreground">to</span>
          <NumField
            ariaLabel="Next VMID upper bound"
            value={form.next_id_upper}
            onChange={(v) => {
              updateField("next_id_upper", v);
            }}
            disabled={!canEdit}
            placeholder="1000000"
          />
        </SettingRow>
      </SettingsSection>

      <SettingsSection title="Migration">
        <SettingRow label="Migration type" htmlFor={id("migration_type")}>
          {select(
            "migration_type",
            form.migration_type,
            (v) => {
              updateField("migration_type", v);
            },
            "Default (secure)",
            [
              ["secure", "Secure (encrypted)"],
              ["insecure", "Insecure (fast)"],
            ],
          )}
        </SettingRow>
        <SettingRow
          label="Migration network"
          description="The network migration traffic uses, as a CIDR."
          htmlFor={id("migration_network")}
        >
          <Input
            id={id("migration_network")}
            value={form.migration_network}
            onChange={(e) => {
              updateField("migration_network", e.target.value);
            }}
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
            <div className="flex w-full flex-wrap items-center gap-1.5 text-xs">
              <span className="text-muted-foreground">From cluster:</span>
              {networkSuggestions.map((cidr) => (
                <button
                  key={cidr}
                  type="button"
                  disabled={!canEdit}
                  onClick={() => {
                    updateField("migration_network", cidr);
                  }}
                  className="rounded-[5px] bg-foreground/[0.07] px-1.5 py-0.5 font-mono text-xs hover:bg-foreground/[0.12] disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {cidr}
                </button>
              ))}
            </div>
          )}
        </SettingRow>
        <SettingRow
          label="Max workers"
          description="Max parallel worker processes (0 = auto)."
          htmlFor={id("max_workers")}
        >
          <Input
            id={id("max_workers")}
            type="number"
            min={0}
            className="w-32"
            value={form.max_workers}
            onChange={(e) => {
              updateField("max_workers", parseInt(e.target.value, 10) || 0);
            }}
            disabled={!canEdit}
            placeholder="4"
          />
        </SettingRow>
      </SettingsSection>

      <SettingsSection title="High availability">
        <SettingRow
          label="Shutdown policy"
          description={
            <>
              What HA does when a node shuts down or reboots. Set to{" "}
              <code>migrate</code> if you want HA-managed VMs evacuated
              automatically.
            </>
          }
          htmlFor={id("ha_shutdown_policy")}
        >
          {select(
            "ha_shutdown_policy",
            form.ha_shutdown_policy,
            (v) => {
              updateField("ha_shutdown_policy", v);
            },
            "Default (conditional)",
            [
              ["freeze", "Freeze: pause services on shutdown"],
              ["failover", "Failover: stop and let HA recover"],
              ["migrate", "Migrate: evacuate HA services first"],
              ["conditional", "Conditional: depends on action"],
            ],
          )}
        </SettingRow>
        <SettingRow label="Fencing" htmlFor={id("fencing")}>
          {select(
            "fencing",
            form.fencing,
            (v) => {
              updateField("fencing", v);
            },
            "Default (watchdog)",
            [
              ["watchdog", "Watchdog"],
              ["hardware", "Hardware"],
              ["both", "Both"],
            ],
          )}
        </SettingRow>
      </SettingsSection>

      <SettingsSection title="Resource scheduling (CRS)">
        <SettingRow
          label="Scheduler"
          description={
            crsDynamicSupported
              ? undefined
              : "Dynamic load balancing requires Proxmox VE 9.2 or newer."
          }
          htmlFor={id("crs_ha")}
        >
          {select(
            "crs_ha",
            form.crs_ha,
            (v) => {
              updateField("crs_ha", v);
            },
            "Default (basic)",
            [
              ["basic", "Basic: round-robin failover"],
              ["static", "Static: assignment-based"],
              ...(crsDynamicSupported || form.crs_ha === "dynamic"
                ? ([["dynamic", "Dynamic: live load balancing"]] as const)
                : []),
            ],
          )}
        </SettingRow>
        <SettingRow
          label="Rebalance on start"
          description="Re-evaluate placement when the cluster comes online."
          htmlFor={id("crs_rebalance_on_start")}
        >
          <Switch
            id={id("crs_rebalance_on_start")}
            checked={form.crs_rebalance_on_start}
            onCheckedChange={(v) => {
              updateField("crs_rebalance_on_start", v);
            }}
            disabled={!canEdit}
          />
        </SettingRow>
        {form.crs_ha === "dynamic" && (
          <SettingRow
            label="Automatic rebalancing"
            description="Let Proxmox live-migrate HA-managed guests to even out node load. While this is on, Nexara DRS automatic mode is disabled to avoid conflicting migrations."
            htmlFor={id("crs_auto_rebalance")}
          >
            <Switch
              id={id("crs_auto_rebalance")}
              checked={form.crs_auto_rebalance}
              onCheckedChange={(v) => {
                updateField("crs_auto_rebalance", v);
              }}
              disabled={!canEdit}
            />
          </SettingRow>
        )}
        {form.crs_ha === "dynamic" && form.crs_auto_rebalance && (
          <>
            <SettingRow label="Threshold" htmlFor={id("crs_threshold")}>
              <NumField
                id={id("crs_threshold")}
                value={form.crs_threshold}
                onChange={(v) => {
                  updateField("crs_threshold", v);
                }}
                disabled={!canEdit}
                placeholder="30"
                unit="%"
              />
            </SettingRow>
            <SettingRow label="Hold" htmlFor={id("crs_hold_duration")}>
              <NumField
                id={id("crs_hold_duration")}
                value={form.crs_hold_duration}
                onChange={(v) => {
                  updateField("crs_hold_duration", v);
                }}
                disabled={!canEdit}
                placeholder="3"
                unit="rounds"
              />
            </SettingRow>
            <SettingRow label="Margin" htmlFor={id("crs_margin")}>
              <NumField
                id={id("crs_margin")}
                value={form.crs_margin}
                onChange={(v) => {
                  updateField("crs_margin", v);
                }}
                disabled={!canEdit}
                placeholder="10"
                unit="%"
              />
            </SettingRow>
            <SettingRow label="Method" htmlFor={id("crs_method")}>
              {select(
                "crs_method",
                form.crs_method,
                (v) => {
                  updateField("crs_method", v);
                },
                "Default (bruteforce)",
                [
                  ["bruteforce", "Bruteforce"],
                  ["topsis", "TOPSIS"],
                ],
              )}
            </SettingRow>
          </>
        )}
      </SettingsSection>

      <SettingsSection
        title="Bandwidth limits"
        description={
          <>
            Per-operation rate limits. Leave blank to use Proxmox defaults;{" "}
            <code>0</code> means unlimited.
          </>
        }
      >
        {(
          [
            ["bwlimit_default", "Default"],
            ["bwlimit_clone", "Clone"],
            ["bwlimit_migration", "Migration"],
            ["bwlimit_move", "Move"],
            ["bwlimit_restore", "Restore"],
          ] as const
        ).map(([key, label]) => (
          <SettingRow key={key} label={label} htmlFor={id(key)}>
            <NumField
              id={id(key)}
              value={form[key]}
              onChange={(v) => {
                updateField(key, v);
              }}
              disabled={!canEdit}
              unit="KiB/s"
            />
          </SettingRow>
        ))}
      </SettingsSection>

      {canEdit && saveBar}
    </div>
  );
}

/** A short number box, with the unit it is in said beside it. */
function NumField({
  id,
  ariaLabel,
  value,
  onChange,
  disabled,
  placeholder,
  unit,
}: {
  id?: string;
  /** For a box its row's label does not name (the second of a pair). */
  ariaLabel?: string;
  value: string;
  onChange: (v: string) => void;
  disabled: boolean;
  placeholder?: string;
  unit?: string;
}) {
  return (
    <>
      <Input
        id={id}
        aria-label={ariaLabel}
        type="number"
        min={0}
        className="w-32"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        disabled={disabled}
        placeholder={placeholder}
      />
      {unit !== undefined && (
        <span className="w-12 text-sm text-muted-foreground">{unit}</span>
      )}
    </>
  );
}

// --- Tags Section ---

function TagsSection({
  tagsQuery,
  updateTags,
  canEdit,
}: {
  tagsQuery: ReturnType<typeof useClusterTags>;
  updateTags: ReturnType<typeof useUpdateClusterTags>;
  canEdit: boolean;
}) {
  const [tagInput, setTagInput] = useState("");
  const [tagAccess, setTagAccess] = useState("");
  const [initialized, setInitialized] = useState(false);
  const uid = useId();

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

  if (tagsQuery.isError) {
    return (
      <Panel title="Tags">
        <ErrorBanner error={tagsQuery.error} />
      </Panel>
    );
  }

  return (
    <SettingsSection
      title="Tags"
      description="The tags guests may carry, and who may set them."
    >
      <SettingRow
        label="Registered tags"
        description="Separated by semicolons."
        htmlFor={`${uid}-tags`}
      >
        <Input
          id={`${uid}-tags`}
          value={tagInput}
          onChange={(e) => {
            setTagInput(e.target.value);
          }}
          placeholder="tag1;tag2;tag3"
          disabled={!canEdit}
        />
        {tagInput && (
          <div className="flex w-full flex-wrap gap-1">
            {tagInput
              .split(";")
              .filter(Boolean)
              .map((tag) => (
                <Badge key={tag} variant="secondary">
                  {tag.trim()}
                </Badge>
              ))}
          </div>
        )}
      </SettingRow>
      <SettingRow label="User tag access" htmlFor={`${uid}-access`}>
        <Select
          value={tagAccess || "__default__"}
          onValueChange={(v) => {
            setTagAccess(v === "__default__" ? "" : v);
          }}
          disabled={!canEdit}
        >
          <SelectTrigger id={`${uid}-access`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__default__">Default (free)</SelectItem>
            <SelectItem value="free">Free</SelectItem>
            <SelectItem value="list">List (from registered)</SelectItem>
            <SelectItem value="existing">Existing (already used)</SelectItem>
            <SelectItem value="none">None (disabled)</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      {canEdit && (
        <SettingsFooter>
          <Button
            onClick={handleSave}
            disabled={updateTags.isPending}
            size="sm"
          >
            <Save className="mr-2 h-4 w-4" />
            {updateTags.isPending ? "Saving..." : "Save tags"}
          </Button>
        </SettingsFooter>
      )}
    </SettingsSection>
  );
}

// --- Join Info Section ---

function buildJoinCommand(
  data: NonNullable<ReturnType<typeof useClusterJoinInfo>["data"]>,
): string {
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
    setTimeout(() => {
      setCopied(false);
    }, 2000);
  };

  const data = joinQuery.data;
  const hasJoinInfo =
    data != null &&
    (data.fingerprint || (data.nodelist && data.nodelist.length > 0));

  return (
    <Panel
      title="Join info"
      aside="What another node needs to join this cluster"
      actions={
        hasJoinInfo ? (
          <Dialog>
            <DialogTrigger asChild>
              <Button variant="outline" size="sm">
                <Info className="mr-2 h-4 w-4" />
                Join information
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-2xl">
              <DialogHeader>
                <DialogTitle>Cluster join information</DialogTitle>
              </DialogHeader>
              <div className="space-y-4">
                <p className="text-sm text-muted-foreground">
                  Use this information to join additional nodes to this cluster.
                  Run the command below on the node you want to add.
                </p>

                <div className="space-y-2">
                  <Label className="text-sm font-medium">Join command</Label>
                  <div className="relative">
                    <pre className="rounded-md bg-muted p-4 text-sm font-mono overflow-x-auto whitespace-pre-wrap break-all">
                      {buildJoinCommand(data)}
                    </pre>
                    <Button
                      aria-label="Copy join command"
                      variant="ghost"
                      size="sm"
                      className="absolute right-2 top-2"
                      onClick={() => {
                        copyToClipboard(buildJoinCommand(data));
                      }}
                    >
                      {copied ? (
                        <Check className="h-4 w-4 text-emerald-500" />
                      ) : (
                        <Copy className="h-4 w-4" />
                      )}
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
                      <Button
                        aria-label="Copy fingerprint"
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          copyToClipboard(data.fingerprint ?? "");
                        }}
                      >
                        <Copy className="h-4 w-4" />
                      </Button>
                    </div>
                  </div>
                )}

                {data.config_digest && (
                  <div className="space-y-2">
                    <Label className="text-sm font-medium">Config digest</Label>
                    <code className="block rounded bg-muted px-3 py-2 text-xs font-mono">
                      {data.config_digest}
                    </code>
                  </div>
                )}

                {data.nodelist && data.nodelist.length > 0 && (
                  <div className="space-y-2">
                    <Label className="text-sm font-medium">Peer nodes</Label>
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
                            <TableCell className="font-medium">
                              {n.name}
                            </TableCell>
                            <TableCell>{n.nodeid}</TableCell>
                            <TableCell>{n.pve_addr}</TableCell>
                            <TableCell>{n.ring0_addr}</TableCell>
                            <TableCell
                              className="font-mono text-xs max-w-[200px] truncate"
                              title={n.pve_fp}
                            >
                              {n.pve_fp}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}
              </div>
            </DialogContent>
          </Dialog>
        ) : undefined
      }
    >
      {joinQuery.isLoading && <Skeleton className="h-16 w-full" />}
      {joinQuery.isError && <ErrorBanner error={joinQuery.error} />}
      {!joinQuery.isLoading && !joinQuery.isError && hasJoinInfo && (
        <dl className="space-y-3.5 text-sm">
          {data.fingerprint && (
            <div>
              <dt className="text-xs text-muted-foreground">
                Cluster fingerprint
              </dt>
              <dd className="mt-0.5 font-mono text-xs break-all select-all">
                {data.fingerprint}
              </dd>
            </div>
          )}
          {data.nodelist && data.nodelist.length > 0 && (
            <div>
              <dt className="text-xs text-muted-foreground">
                Peer nodes ({data.nodelist.length})
              </dt>
              <dd className="mt-1 flex flex-wrap gap-1.5">
                {data.nodelist.map((n) => (
                  <Badge
                    key={n.name}
                    variant="secondary"
                    className="font-mono text-xs"
                  >
                    {n.name} ({n.pve_addr ?? n.ring0_addr})
                  </Badge>
                ))}
              </dd>
            </div>
          )}
        </dl>
      )}
      {!joinQuery.isLoading && !joinQuery.isError && !hasJoinInfo && (
        <p className="text-sm text-muted-foreground">
          No join info available. This may be a standalone node.
        </p>
      )}
    </Panel>
  );
}

// --- Corosync Nodes Section ---

function CorosyncNodesSection({
  nodesQuery,
}: {
  nodesQuery: ReturnType<typeof useCorosyncNodes>;
}) {
  const nodes = nodesQuery.data ?? [];
  const listed = !nodesQuery.isLoading && !nodesQuery.isError;
  return (
    <Panel
      flush
      title="Corosync nodes"
      aside={listed && nodes.length > 0 ? String(nodes.length) : undefined}
    >
      {nodesQuery.isLoading && (
        <div className="px-[18px]">
          <Skeleton className="h-20 w-full" />
        </div>
      )}
      {nodesQuery.isError && (
        <div className="px-[18px]">
          <ErrorBanner error={nodesQuery.error} />
        </div>
      )}
      {listed && nodes.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Node ID</TableHead>
              <TableHead>PVE address</TableHead>
              <TableHead>Ring 0</TableHead>
              <TableHead>Votes</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {nodes.map((n) => (
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
      {listed && nodes.length === 0 && (
        <p className="px-[18px] text-sm text-muted-foreground">
          No corosync nodes found. This may be a standalone node.
        </p>
      )}
    </Panel>
  );
}
