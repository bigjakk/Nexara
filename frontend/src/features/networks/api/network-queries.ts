import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  NodeInterfaces,
  NetworkInterface,
  CreateNetworkInterfaceRequest,
  UpdateNetworkInterfaceRequest,
  FirewallRule,
  FirewallRuleRequest,
  FirewallOptions,
  SDNZone,
  SDNVNet,
  SDNSubnet,
  CreateSDNZoneRequest,
  UpdateSDNZoneRequest,
  CreateSDNVNetRequest,
  UpdateSDNVNetRequest,
  CreateSDNSubnetRequest,
  UpdateSDNSubnetRequest,
  FirewallTemplate,
  CreateTemplateRequest,
  ApplyTemplateResponse,
} from "../types/network";

// --- Network Interfaces ---

export function useNetworkInterfaces(clusterId: string) {
  return useQuery({
    queryKey: ["networks", "interfaces", clusterId],
    queryFn: () =>
      apiClient.get<NodeInterfaces[]>(
        `/api/v1/clusters/${clusterId}/networks`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useNodeNetworkInterfaces(
  clusterId: string,
  nodeName: string,
) {
  return useQuery({
    queryKey: ["networks", "interfaces", clusterId, nodeName],
    queryFn: () =>
      apiClient.get<NetworkInterface[]>(
        `/api/v1/clusters/${clusterId}/networks/${nodeName}`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
  });
}

export function useCreateNetworkInterface(
  clusterId: string,
  nodeName: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: CreateNetworkInterfaceRequest) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/networks/${nodeName}`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["networks", "interfaces", clusterId],
      });
    },
  });
}

export function useUpdateNetworkInterface(
  clusterId: string,
  nodeName: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      iface,
      params,
    }: {
      iface: string;
      params: UpdateNetworkInterfaceRequest;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/networks/${nodeName}/${iface}`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["networks", "interfaces", clusterId],
      });
    },
  });
}

export function useDeleteNetworkInterface(
  clusterId: string,
  nodeName: string,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (iface: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/networks/${nodeName}/${iface}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["networks", "interfaces", clusterId],
      });
    },
  });
}

export function useApplyNetworkConfig(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/networks/${nodeName}/apply`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["networks", "interfaces", clusterId],
      });
    },
  });
}

export function useRevertNetworkConfig(clusterId: string, nodeName: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/networks/${nodeName}/revert`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["networks", "interfaces", clusterId],
      });
    },
  });
}

// --- Cluster Firewall Rules ---

export function useClusterFirewallRules(clusterId: string) {
  return useQuery({
    queryKey: ["firewall", "rules", clusterId],
    queryFn: () =>
      apiClient.get<FirewallRule[]>(
        `/api/v1/clusters/${clusterId}/firewall/rules`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateClusterFirewallRule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: FirewallRuleRequest) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/firewall/rules`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "rules", clusterId],
      });
    },
  });
}

export function useUpdateClusterFirewallRule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ pos, rule }: { pos: number; rule: FirewallRuleRequest }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/firewall/rules/${String(pos)}`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "rules", clusterId],
      });
    },
  });
}

export function useDeleteClusterFirewallRule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (pos: number) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/firewall/rules/${String(pos)}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "rules", clusterId],
      });
    },
  });
}

// --- VM Firewall Rules ---

export function useVMFirewallRules(clusterId: string, vmId: string) {
  return useQuery({
    queryKey: ["firewall", "vm-rules", clusterId, vmId],
    queryFn: () =>
      apiClient.get<FirewallRule[]>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/firewall/rules`,
      ),
    enabled: clusterId.length > 0 && vmId.length > 0,
  });
}

export function useCreateVMFirewallRule(clusterId: string, vmId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: FirewallRuleRequest) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/firewall/rules`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "vm-rules", clusterId, vmId],
      });
    },
  });
}

export function useDeleteVMFirewallRule(clusterId: string, vmId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (pos: number) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/vms/${vmId}/firewall/rules/${String(pos)}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "vm-rules", clusterId, vmId],
      });
    },
  });
}

// --- Firewall Options ---

export function useFirewallOptions(clusterId: string) {
  return useQuery({
    queryKey: ["firewall", "options", clusterId],
    queryFn: () =>
      apiClient.get<FirewallOptions>(
        `/api/v1/clusters/${clusterId}/firewall/options`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useSetFirewallOptions(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (opts: FirewallOptions) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/firewall/options`,
        opts,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "options", clusterId],
      });
    },
  });
}

// --- SDN ---

export function useSDNZones(clusterId: string) {
  return useQuery({
    queryKey: ["sdn", "zones", clusterId],
    queryFn: () =>
      apiClient.get<SDNZone[]>(
        `/api/v1/clusters/${clusterId}/sdn/zones`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useSDNVNets(clusterId: string) {
  return useQuery({
    queryKey: ["sdn", "vnets", clusterId],
    queryFn: () =>
      apiClient.get<SDNVNet[]>(
        `/api/v1/clusters/${clusterId}/sdn/vnets`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateSDNZone(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: CreateSDNZoneRequest) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/zones`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "zones", clusterId],
      });
    },
  });
}

export function useUpdateSDNZone(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      zone,
      params,
    }: {
      zone: string;
      params: UpdateSDNZoneRequest;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/zones/${zone}`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "zones", clusterId],
      });
    },
  });
}

export function useDeleteSDNZone(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (zone: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/zones/${zone}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "zones", clusterId],
      });
    },
  });
}

export function useCreateSDNVNet(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: CreateSDNVNetRequest) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/vnets`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "vnets", clusterId],
      });
    },
  });
}

export function useUpdateSDNVNet(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      vnet,
      params,
    }: {
      vnet: string;
      params: UpdateSDNVNetRequest;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/vnets/${vnet}`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "vnets", clusterId],
      });
    },
  });
}

export function useDeleteSDNVNet(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vnet: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/vnets/${vnet}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "vnets", clusterId],
      });
    },
  });
}

export function useSDNSubnets(clusterId: string, vnet: string) {
  return useQuery({
    queryKey: ["sdn", "subnets", clusterId, vnet],
    queryFn: () =>
      apiClient.get<SDNSubnet[]>(
        `/api/v1/clusters/${clusterId}/sdn/vnets/${vnet}/subnets`,
      ),
    enabled: clusterId.length > 0 && vnet.length > 0,
  });
}

export function useCreateSDNSubnet(clusterId: string, vnet: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: CreateSDNSubnetRequest) =>
      apiClient.post<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/vnets/${vnet}/subnets`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "subnets", clusterId, vnet],
      });
    },
  });
}

export function useUpdateSDNSubnet(clusterId: string, vnet: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      subnet,
      params,
    }: {
      subnet: string;
      params: UpdateSDNSubnetRequest;
    }) =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/vnets/${vnet}/subnets/${subnet}`,
        params,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "subnets", clusterId, vnet],
      });
    },
  });
}

export function useDeleteSDNSubnet(clusterId: string, vnet: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (subnet: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/vnets/${vnet}/subnets/${subnet}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn", "subnets", clusterId, vnet],
      });
    },
  });
}

export function useApplySDN(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiClient.put<{ status: string }>(
        `/api/v1/clusters/${clusterId}/sdn/apply`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["sdn"],
      });
    },
  });
}

// --- Firewall Templates ---

export function useFirewallTemplates() {
  return useQuery({
    queryKey: ["firewall", "templates"],
    queryFn: () =>
      apiClient.get<FirewallTemplate[]>(`/api/v1/firewall-templates`),
  });
}

export function useFirewallTemplate(templateId: string) {
  return useQuery({
    queryKey: ["firewall", "templates", templateId],
    queryFn: () =>
      apiClient.get<FirewallTemplate>(
        `/api/v1/firewall-templates/${templateId}`,
      ),
    enabled: templateId.length > 0,
  });
}

export function useCreateFirewallTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateTemplateRequest) =>
      apiClient.post<FirewallTemplate>(`/api/v1/firewall-templates`, req),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "templates"],
      });
    },
  });
}

export function useUpdateFirewallTemplate(templateId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateTemplateRequest) =>
      apiClient.put<FirewallTemplate>(
        `/api/v1/firewall-templates/${templateId}`,
        req,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "templates"],
      });
    },
  });
}

export function useDeleteFirewallTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (templateId: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/firewall-templates/${templateId}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "templates"],
      });
    },
  });
}

export function useApplyFirewallTemplate(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (templateId: string) =>
      apiClient.post<ApplyTemplateResponse>(
        `/api/v1/clusters/${clusterId}/firewall-templates/${templateId}/apply`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["firewall", "rules", clusterId],
      });
    },
  });
}
