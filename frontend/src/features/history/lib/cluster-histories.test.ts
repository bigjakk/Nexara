/// <reference types="vite/client" />
import { describe, it, expect } from "vitest";
import i18n from "@/lib/i18n";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { historyEventLabel, historyNamespace, historySummary } from "./history";
import {
  clusterHistories,
  clusterHistoryKinds,
  type ClusterHistory,
} from "./cluster-histories";

const c = "c1";
const base = "/api/v1/clusters/c1/history/";

// Every placement, with the route it reads. The Go side holds the routes to
// the objects each asks for (TestClusterPageHistoryRoutes) and to these paths
// (TestClusterPageHistoryPathsAreTheSPAs).
const placements: Record<
  keyof typeof clusterHistories,
  [ClusterHistory, string]
> = {
  ha: [clusterHistories.ha(c), "ha"],
  haResource: [
    clusterHistories.haResource(c, "vm:100"),
    "ha/resources/vm%3A100",
  ],
  haGroup: [clusterHistories.haGroup(c, "group01"), "ha/groups/group01"],
  haRule: [clusterHistories.haRule(c, "rule01"), "ha/rules/rule01"],
  pools: [clusterHistories.pools(c), "pools"],
  pool: [clusterHistories.pool(c, "pool01"), "pools/pool01"],
  mappings: [clusterHistories.mappings(c), "mappings"],
  usbMapping: [clusterHistories.usbMapping(c, "map01"), "mappings/usb/map01"],
  pciMapping: [clusterHistories.pciMapping(c, "map02"), "mappings/pci/map02"],
  access: [clusterHistories.access(c), "access"],
  accessUser: [
    clusterHistories.accessUser(c, "alice@pve"),
    "access/users/alice%40pve",
  ],
  accessGroup: [
    clusterHistories.accessGroup(c, "group01"),
    "access/groups/group01",
  ],
  accessRole: [clusterHistories.accessRole(c, "role01"), "access/roles/role01"],
  replication: [clusterHistories.replication(c), "replication"],
  replicationJob: [
    clusterHistories.replicationJob(c, "100-0"),
    "replication/100-0",
  ],
  ceph: [clusterHistories.ceph(c), "ceph"],
  cephOsd: [clusterHistories.cephOsd(c, 12), "ceph/osds/12"],
  cephPool: [clusterHistories.cephPool(c, "pool#x"), "ceph/pools/pool%23x"],
  cephMonitor: [
    clusterHistories.cephMonitor(c, "pve-01"),
    "ceph/monitors/pve-01",
  ],
  networks: [clusterHistories.networks(c), "networks"],
  // An interface and a subnet id keep their colons, as their routes' do.
  networkInterface: [
    clusterHistories.networkInterface(c, "pve-01", "eth0:0"),
    "networks/interfaces/pve-01/eth0:0",
  ],
  sdnZone: [clusterHistories.sdnZone(c, "zone01"), "networks/zones/zone01"],
  sdnVnet: [clusterHistories.sdnVnet(c, "vnet01"), "networks/vnets/vnet01"],
  sdnSubnet: [
    clusterHistories.sdnSubnet(c, "vnet01", "zone01-2001:db8::-64"),
    "networks/vnets/vnet01/subnets/zone01-2001:db8::-64",
  ],
  sdnController: [
    clusterHistories.sdnController(c, "ctl01"),
    "networks/controllers/ctl01",
  ],
  sdnIpam: [clusterHistories.sdnIpam(c, "ipam01"), "networks/ipams/ipam01"],
  sdnDns: [clusterHistories.sdnDns(c, "dns01"), "networks/dns/dns01"],
  firewall: [clusterHistories.firewall(c), "firewall"],
  firewallAlias: [
    clusterHistories.firewallAlias(c, "web"),
    "firewall/aliases/web",
  ],
  firewallIpset: [
    clusterHistories.firewallIpset(c, "mgmt"),
    "firewall/ipsets/mgmt",
  ],
  firewallGroup: [
    clusterHistories.firewallGroup(c, "admins"),
    "firewall/groups/admins",
  ],
  firewallTemplate: [
    clusterHistories.firewallTemplate("t1"),
    "/api/v1/history/firewall-templates/t1",
  ],
  certificates: [clusterHistories.certificates(c), "certificates"],
  acmeAccount: [
    clusterHistories.acmeAccount(c, "default"),
    "certificates/accounts/default",
  ],
  acmePlugin: [
    clusterHistories.acmePlugin(c, "dns01"),
    "certificates/plugins/dns01",
  ],
  nodeCertificate: [
    clusterHistories.nodeCertificate(c, "pve-01"),
    "certificates/nodes/pve-01",
  ],
  metrics: [clusterHistories.metrics(c), "metric-servers"],
  metricServer: [
    clusterHistories.metricServer(c, "influx01"),
    "metric-servers/influx01",
  ],
  drs: [clusterHistories.drs(c), "drs"],
  drsRule: [clusterHistories.drsRule(c, "r1"), "drs/rules/r1"],
  options: [clusterHistories.options(c), "options"],
};

describe("the cluster page's History placements", () => {
  const query = new URLSearchParams({ limit: "50" });

  it.each(Object.entries(placements))(
    "%s reads its route",
    (_name, [h, path]) => {
      expect(String(h.target.path(query))).toBe(
        `${path.startsWith("/") ? path : base + path}?limit=50`,
      );
    },
  );

  // A placement nothing shows is a History nobody can open.
  it("is used by a tab or a row", () => {
    const sources = Object.entries(
      import.meta.glob(["/src/features/**/*.tsx", "!/src/**/*.test.tsx"], {
        query: "?raw",
        import: "default",
        eager: true,
      }),
    ).map(([, source]) => source);
    for (const name of Object.keys(placements)) {
      expect(
        sources.some((source) => source.includes(`clusterHistories.${name}(`)),
        name,
      ).toBe(true);
    }
  });
});

describe("how a cluster History names its rows", () => {
  const t = (kind: keyof typeof clusterHistoryKinds) =>
    i18n.getFixedT(null, historyNamespace(clusterHistoryKinds[kind]));

  it.each<
    [
      keyof typeof clusterHistoryKinds,
      Parameters<typeof makeHistoryEntry>[0],
      string,
      string,
    ]
  >([
    // A tab reads several types: the type is in the Event, the id in the Summary.
    [
      "ha",
      { resource_type: "ha_group", resource_id: "group01", action: "created" },
      "HA group created",
      "group01",
    ],
    [
      "ha",
      { resource_type: "ha", resource_id: "c1", action: "arm_ha" },
      "HA armed",
      "",
    ],
    [
      "access",
      {
        resource_type: "pve_token",
        resource_id: "a@pve!t1",
        action: "regenerated",
      },
      "API token regenerated",
      "a@pve!t1",
    ],
    [
      "ceph",
      { resource_type: "ceph_osd", resource_id: "12", action: "osd_in" },
      "OSD marked in",
      "osd.12",
    ],
    [
      "options",
      {
        resource_type: "cluster_options",
        resource_id: "c1",
        action: "tags_updated",
      },
      "Tags changed",
      "",
    ],
    // A template applied is a row of the rule set, which names the template.
    [
      "firewall",
      {
        resource_type: "firewall_rule",
        resource_id: "cluster",
        action: "template_applied",
        details: JSON.stringify({
          template_name: "base",
          applied: 3,
          total: 4,
        }),
      },
      "Template applied",
      "base: 3 of 4 rules",
    ],
    // Proxmox's own task reads the same, and so does a change Nexara saw it make.
    [
      "certificates",
      {
        source: "proxmox",
        resource_type: "certificate",
        resource_id: "pve-01",
        action: "ordered",
      },
      "Certificate ordered",
      "pve-01",
    ],
    [
      "ha",
      {
        kind: "observed",
        resource_type: "ha_resource",
        action: "changed",
        fields: ["state"],
      },
      "Changed in Proxmox: state",
      "",
    ],
    // An object's own rows need no type; the rows that are not its own say what they are.
    [
      "haGroup",
      { resource_type: "ha_group", resource_id: "group01", action: "updated" },
      "Updated",
      "",
    ],
    [
      "accessUser",
      { resource_type: "pve_acl", resource_id: "/vms/100", action: "granted" },
      "Permission granted",
      "/vms/100",
    ],
    [
      "pool",
      {
        resource_type: "vm",
        vmid: 100,
        action: "set_pool",
        details: JSON.stringify({ old_pool: "", new_pool: "pool01" }),
      },
      "Guest moved",
      "Guest 100 · pool none → pool01",
    ],
  ])("%s: %o is %s (%s)", (name, over, label, summary) => {
    const e = makeHistoryEntry({ source: "nexara", ...over });
    expect(historyEventLabel(clusterHistoryKinds[name], e, t(name))).toBe(
      label,
    );
    expect(historySummary(clusterHistoryKinds[name], e, t(name))).toBe(summary);
  });
});
