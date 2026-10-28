/**
 * The Histories of the cluster page: one for each tab, and one for each object
 * in a tab's rows that has a stable identity (the HistoryKinds "ha", "pools",
 * … on the server, internal/api/registry_cluster_page_history.go). A placement is
 * the kind and where its route is, which the tab or the row hands to
 * <HistoryTimeline> or <HistoryButton>:
 *
 *     <HistoryButton {...clusterHistories.haGroup(clusterId, name)} title={name} />
 *
 * Their strings are history-<name>.json, registered in lib/i18n-history.ts.
 * What every kind shares — the filters, the columns, the naming — is
 * history.ts's; here is only what a cluster object adds:
 * the icons of its categories, and, for a tab that reads several types of
 * object, what each row is about.
 */
import {
  AtSign,
  BadgeCheck,
  CheckCheck,
  Circle,
  Cpu,
  Eye,
  FileText,
  FolderTree,
  Gauge,
  Globe,
  Hash,
  HardDrive,
  KeyRound,
  Layers,
  List,
  ListChecks,
  ListFilter,
  Network,
  Package,
  Plug,
  Power,
  RefreshCw,
  Server,
  Settings2,
  ShieldCheck,
  SlidersHorizontal,
  StickyNote,
  Tag,
  Usb,
  User,
  Users,
  Waypoints,
  Activity,
  type LucideIcon,
} from "lucide-react";
import { apiPath, keepColons, type ApiPath } from "@/lib/api-path";
import { historyKind, typedEventLabel } from "./history";
import type {
  HistoryEntry,
  HistoryKind,
  HistoryTarget,
} from "../types/history";
import { historyClusterObjectsEN as S } from "@/locales/history-cluster-objects";

/**
 * Every category any of these kinds has, with its icon; a kind uses the ones
 * it has and the timeline draws a plain circle for the rest.
 */
const ICONS: Readonly<Record<string, LucideIcon>> = {
  lifecycle: Package,
  config: Settings2,
  other: Circle,
  // A tab's kinds of object
  resources: Server,
  groups: Users,
  rules: ListChecks,
  arming: ShieldCheck,
  usb: Usb,
  pci: Cpu,
  users: User,
  tokens: KeyRound,
  roles: BadgeCheck,
  permissions: ShieldCheck,
  osds: HardDrive,
  pools: Layers,
  monitors: Eye,
  filesystems: FolderTree,
  interfaces: Network,
  applied: CheckCheck,
  zones: Globe,
  vnets: Waypoints,
  subnets: Network,
  controllers: Cpu,
  ipams: Hash,
  dns: Globe,
  options: SlidersHorizontal,
  aliases: AtSign,
  ipsets: ListFilter,
  templates: FileText,
  accounts: User,
  plugins: Plug,
  nodes: Server,
  evaluations: Gauge,
  ha_rules: ShieldCheck,
  notes: StickyNote,
  tags: Tag,
  // What happened to one object
  tasks: Activity,
  members: Users,
  sync: RefreshCw,
  state: Power,
  entries: List,
  certificate: FileText,
};

/**
 * What a row of a tab's History is about: the id its audit row is filed under,
 * but for the types whose id says nothing — the cluster's own uuid. Ceph's
 * OSDs are filed under their number.
 */
function subjectOf(skip: readonly string[] = []) {
  return (entry: HistoryEntry): string | undefined => {
    if (skip.includes(entry.resource_type) || entry.resource_id === "") {
      return undefined;
    }
    return entry.resource_type === "ceph_osd"
      ? `osd.${entry.resource_id}`
      : entry.resource_id;
  };
}

/**
 * What a row of one object's History is about when it is not the object's own:
 * the id of the row it is, such as a token or a permission, or the guest.
 */
function subjectBesides(primary: string) {
  return (entry: HistoryEntry): string | undefined => {
    if (entry.resource_type === primary) return undefined;
    // A guest's row is filed under its inventory row, which says nothing.
    if (entry.resource_type === "vm") {
      return entry.vmid > 0 ? `Guest ${String(entry.vmid)}` : undefined;
    }
    return entry.resource_id === "" ? undefined : entry.resource_id;
  };
}

type Strings = { categories: Record<string, string> };

/** The window every cluster page History opens on: the server's default for a
 *  request that names none (handlers.WithObjectWindow), so All time asks for
 *  all_time. */
const WINDOW = "7d";

/** A tab's kind: it reads several types of object, and says which each row is. */
function tabKind(
  name: string,
  strings: Strings,
  skip: readonly string[] = [],
): HistoryKind {
  return historyKind(name, strings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    eventLabel: typedEventLabel,
    subject: subjectOf(skip),
  });
}

/** One object's kind. A kind that reads other rows beside its own names the
 *  type that is its own, so those say which they are. */
function rowKind(
  name: string,
  strings: Strings,
  primary?: string,
): HistoryKind {
  return historyKind(name, strings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    eventLabel: typedEventLabel,
    ...(primary === undefined ? {} : { subject: subjectBesides(primary) }),
  });
}

// ── The kinds ────────────────────────────────────────────────────────────

/** The types whose id says nothing: the cluster's own uuid, a scope, a uuid. */
const opaqueIdTypes = [
  "ha",
  "drs",
  "drs_rule",
  "sdn",
  "cluster_options",
  "firewall_rule",
  "firewall_options",
  "firewall_template",
];

export const clusterHistoryKinds = {
  ha: tabKind("ha", S["history-ha"], opaqueIdTypes),
  haResource: rowKind("ha-resource", S["history-ha-resource"]),
  haGroup: rowKind("ha-group", S["history-ha-group"]),
  haRule: rowKind("ha-rule", S["history-ha-rule"]),
  pools: tabKind("pools", S["history-pools"]),
  pool: rowKind("pool", S["history-pool"], "pool"),
  mappings: tabKind("mappings", S["history-mappings"]),
  usbMapping: rowKind("usb-mapping", S["history-usb-mapping"]),
  pciMapping: rowKind("pci-mapping", S["history-pci-mapping"]),
  access: tabKind("access", S["history-access"]),
  accessUser: rowKind("access-user", S["history-access-user"], "pve_user"),
  accessGroup: rowKind("access-group", S["history-access-group"], "pve_group"),
  accessRole: rowKind("access-role", S["history-access-role"], "pve_role"),
  replication: tabKind("replication", S["history-replication"]),
  replicationJob: rowKind("replication-job", S["history-replication-job"]),
  ceph: tabKind("ceph", S["history-ceph"]),
  cephOsd: rowKind("ceph-osd", S["history-ceph-osd"]),
  cephPool: rowKind("ceph-pool", S["history-ceph-pool"]),
  cephMonitor: rowKind("ceph-monitor", S["history-ceph-monitor"]),
  networks: tabKind("networks", S["history-networks"], opaqueIdTypes),
  networkInterface: rowKind(
    "network-interface",
    S["history-network-interface"],
  ),
  sdnZone: rowKind("sdn-zone", S["history-sdn-zone"]),
  sdnVnet: rowKind("sdn-vnet", S["history-sdn-vnet"], "sdn_vnet"),
  sdnSubnet: rowKind("sdn-subnet", S["history-sdn-subnet"]),
  sdnController: rowKind("sdn-controller", S["history-sdn-controller"]),
  sdnIpam: rowKind("sdn-ipam", S["history-sdn-ipam"]),
  sdnDns: rowKind("sdn-dns", S["history-sdn-dns"]),
  firewall: tabKind("firewall", S["history-firewall"], opaqueIdTypes),
  firewallAlias: rowKind("firewall-alias", S["history-firewall-alias"]),
  firewallIpset: rowKind(
    "firewall-ipset",
    S["history-firewall-ipset"],
    "firewall_ipset",
  ),
  firewallGroup: rowKind("firewall-group", S["history-firewall-group"]),
  firewallTemplate: rowKind(
    "firewall-template",
    S["history-firewall-template"],
  ),
  certificates: tabKind("certificates", S["history-certificates"]),
  acmeAccount: rowKind("acme-account", S["history-acme-account"]),
  acmePlugin: rowKind("acme-plugin", S["history-acme-plugin"]),
  nodeCertificate: rowKind("node-certificate", S["history-node-certificate"]),
  metrics: tabKind("metrics", S["history-metrics"]),
  metricServer: rowKind("metric-server", S["history-metric-server"]),
  drs: tabKind("drs", S["history-drs"], opaqueIdTypes),
  drsRule: rowKind("drs-rule", S["history-drs-rule"]),
  options: tabKind("options", S["history-options"], opaqueIdTypes),
} as const satisfies Record<string, HistoryKind>;

// ── The placements ───────────────────────────────────────────────────────

/** A kind and where to read it: what <HistoryTimeline> and <HistoryButton> take. */
export interface ClusterHistory {
  kind: HistoryKind;
  target: HistoryTarget;
}

function placement(
  kind: HistoryKind,
  clusterId: string,
  path: (query: URLSearchParams) => ApiPath,
): ClusterHistory {
  return { kind, target: { clusterId, path } };
}

const k = clusterHistoryKinds;

/**
 * Where each History is read: the cluster's own, /clusters/:id/history/<tab>,
 * and under it the objects of the tab. An id is one path segment, which
 * apiPath encodes and the server decodes where its object's name needs it; an
 * interface and a subnet keep their colons, as their own routes' do.
 */
export const clusterHistories = {
  ha: (c: string) =>
    placement(k.ha, c, (q) => apiPath`/api/v1/clusters/${c}/history/ha?${q}`),
  haResource: (c: string, sid: string) =>
    placement(
      k.haResource,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ha/resources/${sid}?${q}`,
    ),
  haGroup: (c: string, group: string) =>
    placement(
      k.haGroup,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ha/groups/${group}?${q}`,
    ),
  haRule: (c: string, rule: string) =>
    placement(
      k.haRule,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ha/rules/${rule}?${q}`,
    ),
  pools: (c: string) =>
    placement(
      k.pools,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/pools?${q}`,
    ),
  pool: (c: string, id: string) =>
    placement(
      k.pool,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/pools/${id}?${q}`,
    ),
  mappings: (c: string) =>
    placement(
      k.mappings,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/mappings?${q}`,
    ),
  usbMapping: (c: string, id: string) =>
    placement(
      k.usbMapping,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/mappings/usb/${id}?${q}`,
    ),
  pciMapping: (c: string, id: string) =>
    placement(
      k.pciMapping,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/mappings/pci/${id}?${q}`,
    ),
  access: (c: string) =>
    placement(
      k.access,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/access?${q}`,
    ),
  accessUser: (c: string, userid: string) =>
    placement(
      k.accessUser,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/access/users/${userid}?${q}`,
    ),
  accessGroup: (c: string, id: string) =>
    placement(
      k.accessGroup,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/access/groups/${id}?${q}`,
    ),
  accessRole: (c: string, id: string) =>
    placement(
      k.accessRole,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/access/roles/${id}?${q}`,
    ),
  replication: (c: string) =>
    placement(
      k.replication,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/replication?${q}`,
    ),
  replicationJob: (c: string, id: string) =>
    placement(
      k.replicationJob,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/replication/${id}?${q}`,
    ),
  ceph: (c: string) =>
    placement(
      k.ceph,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ceph?${q}`,
    ),
  cephOsd: (c: string, id: number) =>
    placement(
      k.cephOsd,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ceph/osds/${id}?${q}`,
    ),
  cephPool: (c: string, name: string) =>
    placement(
      k.cephPool,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ceph/pools/${name}?${q}`,
    ),
  cephMonitor: (c: string, name: string) =>
    placement(
      k.cephMonitor,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/ceph/monitors/${name}?${q}`,
    ),
  networks: (c: string) =>
    placement(
      k.networks,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/networks?${q}`,
    ),
  networkInterface: (c: string, node: string, iface: string) =>
    placement(
      k.networkInterface,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/networks/interfaces/${node}/${keepColons(iface)}?${q}`,
    ),
  sdnZone: (c: string, id: string) =>
    placement(
      k.sdnZone,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/networks/zones/${id}?${q}`,
    ),
  sdnVnet: (c: string, id: string) =>
    placement(
      k.sdnVnet,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/networks/vnets/${id}?${q}`,
    ),
  sdnSubnet: (c: string, vnet: string, id: string) =>
    placement(
      k.sdnSubnet,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/networks/vnets/${vnet}/subnets/${keepColons(id)}?${q}`,
    ),
  sdnController: (c: string, id: string) =>
    placement(
      k.sdnController,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/networks/controllers/${id}?${q}`,
    ),
  sdnIpam: (c: string, id: string) =>
    placement(
      k.sdnIpam,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/networks/ipams/${id}?${q}`,
    ),
  sdnDns: (c: string, id: string) =>
    placement(
      k.sdnDns,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/networks/dns/${id}?${q}`,
    ),
  firewall: (c: string) =>
    placement(
      k.firewall,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/firewall?${q}`,
    ),
  firewallAlias: (c: string, name: string) =>
    placement(
      k.firewallAlias,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/firewall/aliases/${name}?${q}`,
    ),
  firewallIpset: (c: string, name: string) =>
    placement(
      k.firewallIpset,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/firewall/ipsets/${name}?${q}`,
    ),
  firewallGroup: (c: string, name: string) =>
    placement(
      k.firewallGroup,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/firewall/groups/${name}?${q}`,
    ),
  /** A template belongs to no cluster: its History is read across all. */
  firewallTemplate: (id: string): ClusterHistory => ({
    kind: k.firewallTemplate,
    target: {
      clusterId: null,
      path: (q) => apiPath`/api/v1/history/firewall-templates/${id}?${q}`,
    },
  }),
  certificates: (c: string) =>
    placement(
      k.certificates,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/certificates?${q}`,
    ),
  acmeAccount: (c: string, name: string) =>
    placement(
      k.acmeAccount,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/certificates/accounts/${name}?${q}`,
    ),
  acmePlugin: (c: string, id: string) =>
    placement(
      k.acmePlugin,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/certificates/plugins/${id}?${q}`,
    ),
  nodeCertificate: (c: string, node: string) =>
    placement(
      k.nodeCertificate,
      c,
      (q) =>
        apiPath`/api/v1/clusters/${c}/history/certificates/nodes/${node}?${q}`,
    ),
  metrics: (c: string) =>
    placement(
      k.metrics,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/metric-servers?${q}`,
    ),
  metricServer: (c: string, id: string) =>
    placement(
      k.metricServer,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/metric-servers/${id}?${q}`,
    ),
  drs: (c: string) =>
    placement(k.drs, c, (q) => apiPath`/api/v1/clusters/${c}/history/drs?${q}`),
  drsRule: (c: string, id: string) =>
    placement(
      k.drsRule,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/drs/rules/${id}?${q}`,
    ),
  options: (c: string) =>
    placement(
      k.options,
      c,
      (q) => apiPath`/api/v1/clusters/${c}/history/options?${q}`,
    ),
} as const;
