/**
 * The strings of the cluster page's Histories: one namespace per kind,
 * history-<name> (features/history/lib/cluster-histories.ts says what the
 * kinds are). lib/i18n.ts registers them; the kinds read their categories from
 * them.
 */
import haEN from "./en/history-ha.json";
import haResourceEN from "./en/history-ha-resource.json";
import haGroupEN from "./en/history-ha-group.json";
import haRuleEN from "./en/history-ha-rule.json";
import poolsEN from "./en/history-pools.json";
import poolEN from "./en/history-pool.json";
import mappingsEN from "./en/history-mappings.json";
import usbMappingEN from "./en/history-usb-mapping.json";
import pciMappingEN from "./en/history-pci-mapping.json";
import accessEN from "./en/history-access.json";
import accessUserEN from "./en/history-access-user.json";
import accessGroupEN from "./en/history-access-group.json";
import accessRoleEN from "./en/history-access-role.json";
import replicationEN from "./en/history-replication.json";
import replicationJobEN from "./en/history-replication-job.json";
import cephEN from "./en/history-ceph.json";
import cephOsdEN from "./en/history-ceph-osd.json";
import cephPoolEN from "./en/history-ceph-pool.json";
import cephMonitorEN from "./en/history-ceph-monitor.json";
import networksEN from "./en/history-networks.json";
import networkInterfaceEN from "./en/history-network-interface.json";
import sdnZoneEN from "./en/history-sdn-zone.json";
import sdnVnetEN from "./en/history-sdn-vnet.json";
import sdnSubnetEN from "./en/history-sdn-subnet.json";
import sdnControllerEN from "./en/history-sdn-controller.json";
import sdnIpamEN from "./en/history-sdn-ipam.json";
import sdnDnsEN from "./en/history-sdn-dns.json";
import firewallEN from "./en/history-firewall.json";
import firewallAliasEN from "./en/history-firewall-alias.json";
import firewallIpsetEN from "./en/history-firewall-ipset.json";
import firewallGroupEN from "./en/history-firewall-group.json";
import firewallTemplateEN from "./en/history-firewall-template.json";
import certificatesEN from "./en/history-certificates.json";
import acmeAccountEN from "./en/history-acme-account.json";
import acmePluginEN from "./en/history-acme-plugin.json";
import nodeCertificateEN from "./en/history-node-certificate.json";
import metricsEN from "./en/history-metrics.json";
import metricServerEN from "./en/history-metric-server.json";
import drsEN from "./en/history-drs.json";
import drsRuleEN from "./en/history-drs-rule.json";
import optionsEN from "./en/history-options.json";

export const historyClusterObjectsEN = {
  "history-ha": haEN,
  "history-ha-resource": haResourceEN,
  "history-ha-group": haGroupEN,
  "history-ha-rule": haRuleEN,
  "history-pools": poolsEN,
  "history-pool": poolEN,
  "history-mappings": mappingsEN,
  "history-usb-mapping": usbMappingEN,
  "history-pci-mapping": pciMappingEN,
  "history-access": accessEN,
  "history-access-user": accessUserEN,
  "history-access-group": accessGroupEN,
  "history-access-role": accessRoleEN,
  "history-replication": replicationEN,
  "history-replication-job": replicationJobEN,
  "history-ceph": cephEN,
  "history-ceph-osd": cephOsdEN,
  "history-ceph-pool": cephPoolEN,
  "history-ceph-monitor": cephMonitorEN,
  "history-networks": networksEN,
  "history-network-interface": networkInterfaceEN,
  "history-sdn-zone": sdnZoneEN,
  "history-sdn-vnet": sdnVnetEN,
  "history-sdn-subnet": sdnSubnetEN,
  "history-sdn-controller": sdnControllerEN,
  "history-sdn-ipam": sdnIpamEN,
  "history-sdn-dns": sdnDnsEN,
  "history-firewall": firewallEN,
  "history-firewall-alias": firewallAliasEN,
  "history-firewall-ipset": firewallIpsetEN,
  "history-firewall-group": firewallGroupEN,
  "history-firewall-template": firewallTemplateEN,
  "history-certificates": certificatesEN,
  "history-acme-account": acmeAccountEN,
  "history-acme-plugin": acmePluginEN,
  "history-node-certificate": nodeCertificateEN,
  "history-metrics": metricsEN,
  "history-metric-server": metricServerEN,
  "history-drs": drsEN,
  "history-drs-rule": drsRuleEN,
  "history-options": optionsEN,
} as const;
