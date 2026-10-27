/**
 * The cluster kind of History (HistoryKind "cluster" on the server):
 * everything that happened in one cluster. A guest's row reads as on the
 * guest's own History; the rest by the shared naming, in history-cluster.json's
 * strings — a burst of sync failures as one run.
 */
import {
  Archive,
  BadgeCheck,
  Circle,
  Copy,
  Database,
  HardDrive,
  KeyRound,
  Monitor,
  Network,
  RefreshCw,
  Scale,
  Server,
  Settings2,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";
import strings from "@/locales/en/history-cluster.json";
import { historyGuestVmid, historyKind } from "@/features/history/lib/history";
import type { HistoryEntry } from "@/features/history/types/history";
import { guestRowNaming } from "@/features/vms/lib/guest-history-kind";

type ClusterHistoryCategory = keyof typeof strings.categories;

const ICONS: Record<ClusterHistoryCategory, LucideIcon> = {
  guests: Monitor,
  nodes: Server,
  storage: HardDrive,
  backup: Archive,
  ceph: Database,
  ha: ShieldCheck,
  network: Network,
  access: KeyRound,
  certificates: BadgeCheck,
  replication: Copy,
  drs: Scale,
  sync: RefreshCw,
  options: Settings2,
  other: Circle,
};

/** The server's cap on an unfiltered read (clusterHistoryRowCap). */
export const CLUSTER_HISTORY_ROW_CAP = 2_000;

const isGuestRow = (entry: HistoryEntry) =>
  entry.kind !== "run" && historyGuestVmid(entry) > 0;

export const clusterHistoryKind = historyKind("cluster", strings, {
  icons: ICONS,
  // A cluster's whole trail is long: the default is its last week.
  defaultWindow: "7d",
  rowCap: CLUSTER_HISTORY_ROW_CAP,
  eventLabel: (entry) =>
    isGuestRow(entry) ? guestRowNaming.eventLabel(entry) : undefined,
  summary: (entry) =>
    isGuestRow(entry) ? guestRowNaming.summary(entry) : undefined,
  note: (entry) => (isGuestRow(entry) ? guestRowNaming.note(entry) : undefined),
});
