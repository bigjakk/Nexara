export interface NetworkInterface {
  iface: string;
  type: string;
  active: number;
  autostart: number;
  method?: string;
  method6?: string;
  address?: string;
  netmask?: string;
  gateway?: string;
  cidr?: string;
  bridge_ports?: string;
  bridge_stp?: string;
  bridge_fd?: string;
  comments?: string;
}

export interface NodeInterfaces {
  node: string;
  interfaces: NetworkInterface[];
}

export interface CreateNetworkInterfaceRequest {
  iface: string;
  type: string;
  address?: string;
  netmask?: string;
  gateway?: string;
  cidr?: string;
  autostart?: number;
  bridge_ports?: string;
  bridge_stp?: string;
  bridge_fd?: string;
  comments?: string;
  method?: string;
  method6?: string;
}

export interface UpdateNetworkInterfaceRequest {
  type: string;
  address?: string;
  netmask?: string;
  gateway?: string;
  cidr?: string;
  autostart?: number;
  bridge_ports?: string;
  bridge_stp?: string;
  bridge_fd?: string;
  comments?: string;
  method?: string;
  method6?: string;
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
}

export interface SDNVNet {
  vnet: string;
  zone: string;
  tag?: number;
  alias?: string;
  vlanaware?: number;
}

export interface SDNSubnet {
  subnet: string;
  type?: string;
  gateway?: string;
  snat?: number;
  vnet?: string;
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
}

export interface UpdateSDNZoneRequest {
  bridge?: string;
  tag?: number;
  "vlan-protocol"?: string;
  peers?: string;
  mtu?: number;
  nodes?: string;
  ipam?: string;
}

export interface CreateSDNVNetRequest {
  vnet: string;
  zone: string;
  tag?: number;
  alias?: string;
  vlanaware?: number;
}

export interface UpdateSDNVNetRequest {
  zone?: string;
  tag?: number;
  alias?: string;
  vlanaware?: number;
}

export interface CreateSDNSubnetRequest {
  subnet: string;
  gateway?: string;
  snat?: number;
  type?: string;
}

export interface UpdateSDNSubnetRequest {
  gateway?: string;
  snat?: number;
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
