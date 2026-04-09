/**
 * Storage pool detail screen — mirrors the node detail layout but with
 * storage-specific sections (capacity, configuration, state).
 *
 * Route: /(app)/clusters/[id]/storage/[storageId]
 *
 * Lives inside the clusters Stack navigator alongside the VM detail,
 * node detail, and console screens, so back navigation works as a
 * normal stack pop.
 *
 * Navigated to from:
 *   - The cluster detail screen's Storage section (tap to drill in)
 *   - The global search modal (storage-type results)
 *
 * Implementation notes:
 *
 *   - `useStoragePool()` is a thin client-side filter over
 *     `useClusterStoragePools()` — same pattern as node detail —
 *     because the backend has no single-pool GET endpoint.
 *
 *   - The backend returns one row per (node × storage name) tuple.
 *     We render whatever specific row the caller tapped, so the
 *     `node_id` field is used to resolve a display name for the
 *     "Node" row by cross-referencing the cluster nodes cache.
 *
 *   - No sparklines: storage pools don't have per-collect metric
 *     history tracked in Nexara's timescale hypertables. The
 *     capacity bar is the visual element.
 *
 *   - Content listing (ISOs, templates, backups, disk images) lives
 *     below the metadata section. Read-only on mobile for v1 — no
 *     upload or delete actions, those stay desktop-only. The list is
 *     fetched lazily via `useStorageContent` and grouped by content
 *     type. Skipped entirely if the pool's `content` field doesn't
 *     advertise any browseable types.
 */

import { useCallback, useMemo, useState } from "react";
import {
  ActivityIndicator,
  RefreshControl,
  ScrollView,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, useLocalSearchParams } from "expo-router";

import {
  useStorageContent,
  useStoragePool,
} from "@/features/api/storage-queries";
import { useClusterNodes } from "@/features/api/node-queries";
import type { StorageContentItem } from "@/features/api/types";
import { ListError } from "@/components/ListEmpty";
import { StatusPill, type StatusTone } from "@/components/StatusPill";
import { Row, SectionHeader } from "@/components/detail-screen";
import { formatBytes, formatRelative } from "@/lib/format";

export default function StorageDetailScreen() {
  const params = useLocalSearchParams<{ id: string; storageId: string }>();
  const clusterId = params.id;
  const storageId = params.storageId;

  const pool = useStoragePool(clusterId, storageId);
  // Nodes list is usually already in the cache (user came from cluster
  // detail or search) — the hook uses the shared query key so we share
  // the cached list with the cluster detail and node detail screens.
  const nodes = useClusterNodes(clusterId);
  const content = useStorageContent(clusterId, storageId);

  const nodeName = useMemo(() => {
    if (!pool.data || !nodes.data) return undefined;
    return nodes.data.find((n) => n.id === pool.data?.node_id)?.name;
  }, [pool.data, nodes.data]);

  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([pool.refetch(), nodes.refetch(), content.refetch()]);
    } finally {
      setRefreshing(false);
    }
  }, [pool, nodes, content]);

  if (pool.isLoading && !pool.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      </SafeAreaView>
    );
  }

  if (pool.isError || !pool.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <ListError
          detail={
            pool.error instanceof Error
              ? pool.error.message
              : "Storage pool not found"
          }
        />
      </SafeAreaView>
    );
  }

  const p = pool.data;
  const usagePct = p.total > 0 ? (p.used / p.total) * 100 : 0;
  const contentTypes = p.content
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  const { statusLabel, statusTone } = deriveStatus(p);

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen options={{ title: p.storage }} />
      <ScrollView
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={onRefresh}
            tintColor="#22c55e"
          />
        }
      >
        <View className="p-4">
          {/* Header card */}
          <View className="rounded-lg border border-border bg-card p-4">
            <View className="flex-row items-start justify-between">
              <View className="flex-1">
                <Text className="text-xl font-bold text-foreground">
                  {p.storage}
                </Text>
                <Text className="mt-1 text-xs text-muted-foreground">
                  {describeStorageType(p.type)}
                  {p.shared ? " · shared" : ""}
                </Text>
              </View>
              <StatusPill label={statusLabel} tone={statusTone} />
            </View>
          </View>

          {/* Capacity */}
          <SectionHeader title="Capacity" />
          <View className="rounded-lg border border-border bg-card p-4">
            {p.total > 0 ? (
              <>
                <View className="mb-2 flex-row items-baseline justify-between">
                  <Text className="text-lg font-semibold text-foreground">
                    {formatBytes(p.used)}
                    <Text className="text-sm font-normal text-muted-foreground">
                      {" "}
                      / {formatBytes(p.total)}
                    </Text>
                  </Text>
                  <Text className="text-sm text-muted-foreground">
                    {usagePct.toFixed(1)}%
                  </Text>
                </View>
                <CapacityBar pct={usagePct} />
                <View className="mt-3 flex-row gap-3">
                  <Stat label="Used" value={formatBytes(p.used)} />
                  <Stat label="Free" value={formatBytes(p.avail)} />
                </View>
              </>
            ) : (
              <Text className="text-xs text-muted-foreground">
                No capacity data — Proxmox may not have reported usage for
                this storage yet, or the storage is inactive.
              </Text>
            )}
          </View>

          {/* Configuration */}
          <SectionHeader title="Configuration" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="Type" value={describeStorageType(p.type)} />
            <Row label="Node" value={nodeName ?? "—"} />
            <Row label="Shared" value={p.shared ? "yes" : "no"} />
            <Row label="Enabled" value={p.enabled ? "yes" : "no"} />
            <Row
              label="Active"
              value={p.active ? "yes" : "no"}
              last={contentTypes.length === 0}
            />
            {contentTypes.length > 0 ? (
              <Row
                label="Content"
                value={contentTypes.join(", ")}
                last
              />
            ) : null}
          </View>

          {/*
            Contents section. Skipped entirely if the pool's content
            field doesn't advertise any browseable types — for example
            an LVM-thin pool that only stores VM disks (`images,rootdir`)
            wouldn't be useful to list here, and fetching it would just
            spend bandwidth on data the UI then ignores.
          */}
          {hasBrowseableContent(contentTypes) ? (
            <>
              <SectionHeader title="Contents" />
              <ContentList
                state={content}
                emptyMessage={emptyMessageFor(contentTypes)}
              />
            </>
          ) : null}

          {/* Metadata */}
          <SectionHeader title="Metadata" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="Last seen" value={formatRelative(p.last_seen_at)} />
            <Row label="Created" value={formatRelative(p.created_at)} last />
          </View>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

// ─── Content listing components ────────────────────────────────────────────

/**
 * Browseable content types that get a section in the Contents list.
 * `images` and `rootdir` are intentionally NOT browseable on mobile —
 * VM disk images don't make sense to list outside the VM detail context,
 * and they typically dominate the row count on shared storages, drowning
 * out the more interesting items (ISOs, templates, backups). Same logic
 * applies to `snippets`, which is a niche Proxmox feature.
 */
const BROWSEABLE_TYPES: ReadonlySet<string> = new Set([
  "iso",
  "vztmpl",
  "backup",
]);

const GROUP_ORDER: readonly string[] = ["iso", "vztmpl", "backup"];

const GROUP_LABELS: Record<string, string> = {
  iso: "ISO images",
  vztmpl: "Container templates",
  backup: "Backups",
};

function hasBrowseableContent(contentTypes: string[]): boolean {
  return contentTypes.some((t) => BROWSEABLE_TYPES.has(t));
}

function emptyMessageFor(contentTypes: string[]): string {
  const browseable = contentTypes.filter((t) => BROWSEABLE_TYPES.has(t));
  if (browseable.length === 0) return "Nothing to list";
  const labels = browseable.map((t) => GROUP_LABELS[t] ?? t).join(", ");
  return `No ${labels.toLowerCase()} on this storage yet.`;
}

interface ContentQueryState {
  data: StorageContentItem[] | undefined;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
}

function ContentList({
  state,
  emptyMessage,
}: {
  state: ContentQueryState;
  emptyMessage: string;
}) {
  if (state.isLoading && !state.data) {
    return (
      <View className="rounded-lg border border-border bg-card p-4">
        <ActivityIndicator color="#22c55e" />
      </View>
    );
  }
  if (state.isError) {
    return (
      <View className="rounded-lg border border-border bg-card p-4">
        <Text className="text-xs text-destructive">
          {state.error instanceof Error
            ? state.error.message
            : "Failed to load contents"}
        </Text>
      </View>
    );
  }
  // Filter to browseable items (drop images / rootdir / snippets) and
  // group by content type. Each group is sorted newest-first by ctime.
  const browseable = (state.data ?? []).filter((item) =>
    BROWSEABLE_TYPES.has(item.content),
  );
  if (browseable.length === 0) {
    return (
      <View className="rounded-lg border border-border bg-card p-4">
        <Text className="text-xs text-muted-foreground">{emptyMessage}</Text>
      </View>
    );
  }

  const grouped = new Map<string, StorageContentItem[]>();
  for (const item of browseable) {
    const list = grouped.get(item.content) ?? [];
    list.push(item);
    grouped.set(item.content, list);
  }
  for (const list of grouped.values()) {
    list.sort((a, b) => b.ctime - a.ctime);
  }

  return (
    <View className="gap-4">
      {GROUP_ORDER.filter((t) => grouped.has(t)).map((t) => {
        const items = grouped.get(t) ?? [];
        return (
          <View key={t}>
            <Text className="mb-2 px-1 text-[11px] font-medium uppercase text-muted-foreground">
              {GROUP_LABELS[t]} · {items.length}
            </Text>
            <View className="overflow-hidden rounded-lg border border-border bg-card">
              {items.map((item, idx) => {
                const last = idx === items.length - 1;
                return (
                  <View
                    key={item.volid}
                    className={`px-4 py-3 ${last ? "" : "border-b border-border"}`}
                  >
                    <Text
                      className="text-sm font-medium text-foreground"
                      numberOfLines={1}
                    >
                      {extractVolumeName(item.volid)}
                    </Text>
                    <Text className="mt-0.5 text-[11px] text-muted-foreground">
                      {formatBytes(item.size)}
                      {item.ctime > 0
                        ? ` · ${formatRelative(
                            new Date(item.ctime * 1000).toISOString(),
                          )}`
                        : ""}
                      {typeof item.vmid === "number"
                        ? ` · vm ${String(item.vmid)}`
                        : ""}
                    </Text>
                  </View>
                );
              })}
            </View>
          </View>
        );
      })}
    </View>
  );
}

/**
 * Extract a human-readable filename from a Proxmox volid.
 *
 *   "local:iso/debian-12.iso" → "debian-12.iso"
 *   "local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst"
 *     → "debian-12-standard_12.7-1_amd64.tar.zst"
 *   "local:backup/vzdump-qemu-100-2024_01_15-12_00_00.vma.zst"
 *     → "vzdump-qemu-100-2024_01_15-12_00_00.vma.zst"
 *   "local-lvm:vm-100-disk-0" → "vm-100-disk-0"
 *
 * Falls back to the raw volid for anything that doesn't match the
 * expected `<storage>:<typedir>/<filename>` or `<storage>:<filename>`
 * shapes — defensive against future Proxmox formats.
 */
function extractVolumeName(volid: string): string {
  // Strip everything up to and including the LAST `/`. If there's no
  // slash, fall back to splitting on `:` and taking the trailing part.
  const slash = volid.lastIndexOf("/");
  if (slash >= 0) return volid.slice(slash + 1);
  const colon = volid.lastIndexOf(":");
  if (colon >= 0) return volid.slice(colon + 1);
  return volid;
}

// ─── Local helpers (storage-specific) ──────────────────────────────────────
// `Row` and `SectionHeader` come from `@/components/detail-screen`; the
// helpers below are unique to the storage detail screen and stay local.

function deriveStatus(p: {
  active: boolean;
  enabled: boolean;
}): { statusLabel: string; statusTone: StatusTone } {
  if (!p.enabled)
    return { statusLabel: "disabled", statusTone: "neutral" };
  if (!p.active) return { statusLabel: "inactive", statusTone: "warning" };
  return { statusLabel: "active", statusTone: "success" };
}

/**
 * Friendlier labels for the compact Proxmox storage plugin types.
 * Falls back to the raw type for anything unknown.
 */
function describeStorageType(type: string): string {
  const m: Record<string, string> = {
    dir: "Directory",
    nfs: "NFS",
    cifs: "CIFS / SMB",
    lvm: "LVM",
    lvmthin: "LVM-Thin",
    zfspool: "ZFS pool",
    iscsi: "iSCSI",
    iscsidirect: "iSCSI direct",
    rbd: "Ceph RBD",
    cephfs: "CephFS",
    glusterfs: "GlusterFS",
    btrfs: "Btrfs",
    pbs: "Proxmox Backup Server",
  };
  return m[type] ?? type;
}

function CapacityBar({ pct }: { pct: number }) {
  const clamped = Math.max(0, Math.min(100, pct));
  // Color scales from green → amber → red as the pool fills. Matches the
  // ad-hoc visual language the desktop frontend uses for capacity warnings.
  const color =
    clamped >= 90 ? "#ef4444" : clamped >= 75 ? "#f59e0b" : "#22c55e";
  const width = `${clamped}%` as const;
  return (
    <View className="h-2 overflow-hidden rounded-full bg-muted">
      <View
        style={{ width, backgroundColor: color }}
        className="h-full"
      />
    </View>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <View className="flex-1 rounded border border-border bg-background p-3">
      <Text className="text-xs text-muted-foreground">{label}</Text>
      <Text className="mt-1 text-sm font-semibold text-foreground">
        {value}
      </Text>
    </View>
  );
}
