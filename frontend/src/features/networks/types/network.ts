/** Settings shared by an interface as read, created and updated. Field names
 *  mirror the Proxmox API parameters exactly. */
export interface NetworkInterfaceOptions {
  address?: string;
  netmask?: string;
  gateway?: string;
  cidr?: string;
  address6?: string;
  netmask6?: string;
  gateway6?: string;
  cidr6?: string;
  autostart?: number;
  comments?: string;
  comments6?: string;
  mtu?: number;
  method?: string;
  method6?: string;
  bridge_ports?: string;
  bridge_stp?: string;
  bridge_fd?: string;
  bridge_vlan_aware?: number;
  bridge_vids?: string;
  slaves?: string;
  bond_mode?: string;
  bond_xmit_hash_policy?: string;
  "bond-primary"?: string;
  ovs_bridge?: string;
  ovs_ports?: string;
  ovs_bonds?: string;
  ovs_options?: string;
  ovs_tag?: number;
  "vlan-id"?: number;
  "vlan-raw-device"?: string;
}

export interface NetworkInterface extends NetworkInterfaceOptions {
  iface: string;
  type: string;
  active: number;
  autostart: number;
}

export interface NodeInterfaces {
  node: string;
  interfaces: NetworkInterface[];
}

export interface CreateNetworkInterfaceRequest extends NetworkInterfaceOptions {
  iface: string;
  type: string;
}

export interface UpdateNetworkInterfaceRequest extends NetworkInterfaceOptions {
  type: string;
  /** Settings to unset. Proxmox ignores an empty value, so clearing a field
   *  means naming it here. */
  delete?: string[];
}

export interface FirewallRule {
  pos: number;
  type: string;
  action: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  proto?: string;
  enable: number;
  comment?: string;
  macro?: string;
  log?: string;
  iface?: string;
  /** Digest of the WHOLE list this rule was read from — the same on every
   *  rule of one listing. Sent back with an update or delete of the rule so
   *  Proxmox refuses it if the list changed since (firewall-rule-digest.ts). */
  digest: string;
}

export interface FirewallRuleRequest {
  type: string;
  action: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  proto?: string;
  enable: number;
  comment?: string;
  macro?: string;
  log?: string;
  iface?: string;
}

export interface FirewallOptions {
  enable?: number;
  policy_in?: string;
  policy_out?: string;
  log_level_in?: string;
  log_level_out?: string;
}

export interface SDNZone {
  zone: string;
  type: string;
  nodes?: string;
  ipam?: string;
  dns?: string;
  reversedns?: string;
  dnszone?: string;
  bridge?: string;
  tag?: number;
  "vlan-protocol"?: string;
  peers?: string;
  mtu?: number;
  controller?: string;
  "vrf-vxlan"?: number;
  exitnodes?: string;
  mac?: string;
  "advertise-subnets"?: number;
  "disable-arp-nd-suppression"?: number;
  "bridge-disable-mac-learning"?: number;
}

export interface SDNVNet {
  vnet: string;
  zone: string;
  tag?: number;
  alias?: string;
  vlanaware?: number;
  isolate?: number;
}

export interface SDNSubnet {
  subnet: string;
  type?: string;
  gateway?: string;
  snat?: number;
  vnet?: string;
  "dhcp-range"?: string;
  "dhcp-dns-server"?: string;
}

export interface SDNController {
  controller: string;
  type: string;
  nodes?: string;
  asn?: number;
  peers?: string;
  "isis-domain"?: string;
  "isis-ifaces"?: string;
  "isis-net"?: string;
  "bgp-multipath-as-path-relax"?: number;
  "ebgp-multihop"?: number;
  loopback?: string;
  node?: string;
}

export interface SDNIPAM {
  ipam: string;
  type: string;
  url?: string;
  token?: string;
  section?: number;
}

export interface SDNDNS {
  dns: string;
  type: string;
  url?: string;
  key?: string;
  reversemaskv6?: number;
}

export interface CreateSDNZoneRequest {
  zone: string;
  type: string;
  bridge?: string;
  tag?: number;
  "vlan-protocol"?: string;
  peers?: string;
  mtu?: number;
  nodes?: string;
  ipam?: string;
  dns?: string;
  reversedns?: string;
  dnszone?: string;
  controller?: string;
  "vrf-vxlan"?: number;
  exitnodes?: string;
  mac?: string;
  "advertise-subnets"?: number;
  "disable-arp-nd-suppression"?: number;
}

export interface UpdateSDNZoneRequest {
  bridge?: string;
  tag?: number;
  "vlan-protocol"?: string;
  peers?: string;
  mtu?: number;
  nodes?: string;
  ipam?: string;
  dns?: string;
  reversedns?: string;
  dnszone?: string;
  controller?: string;
  "vrf-vxlan"?: number;
  exitnodes?: string;
  mac?: string;
  "advertise-subnets"?: number;
  "disable-arp-nd-suppression"?: number;
}

export interface CreateSDNVNetRequest {
  vnet: string;
  zone: string;
  tag?: number;
  alias?: string;
  vlanaware?: number;
  isolate?: number;
}

export interface UpdateSDNVNetRequest {
  zone?: string;
  tag?: number;
  alias?: string;
  vlanaware?: number;
  isolate?: number;
}

export interface CreateSDNSubnetRequest {
  subnet: string;
  gateway?: string;
  snat?: number;
  type?: string;
  "dhcp-range"?: string;
  "dhcp-dns-server"?: string;
}

export interface UpdateSDNSubnetRequest {
  gateway?: string;
  snat?: number;
  "dhcp-range"?: string;
  "dhcp-dns-server"?: string;
}

export interface CreateSDNControllerRequest {
  controller: string;
  type: string;
  asn?: number;
  peers?: string;
  nodes?: string;
  "isis-domain"?: string;
  "isis-ifaces"?: string;
  "isis-net"?: string;
  "ebgp-multihop"?: number;
  loopback?: string;
  node?: string;
}

export interface UpdateSDNControllerRequest {
  asn?: number;
  peers?: string;
  nodes?: string;
  "isis-domain"?: string;
  "isis-ifaces"?: string;
  "isis-net"?: string;
  "ebgp-multihop"?: number;
  loopback?: string;
  node?: string;
}

export interface CreateSDNIPAMRequest {
  ipam: string;
  type: string;
  url?: string;
  token?: string;
  section?: number;
}

export interface UpdateSDNIPAMRequest {
  url?: string;
  token?: string;
  section?: number;
}

export interface CreateSDNDNSRequest {
  dns: string;
  type: string;
  url?: string;
  key?: string;
}

export interface UpdateSDNDNSRequest {
  url?: string;
  key?: string;
}

export interface FirewallTemplate {
  id: string;
  name: string;
  description: string;
  rules: FirewallRuleRequest[];
  created_at: string;
  updated_at: string;
}

export interface CreateTemplateRequest {
  name: string;
  description: string;
  rules: FirewallRuleRequest[];
}

export interface ApplyTemplateResponse {
  status: string;
  applied: number;
  total: number;
}
