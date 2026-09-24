import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

/**
 * Opts a mutation out of the global error toast in lib/query-client.ts.
 *
 * That toast is a safety net for mutations with no error handling of their own.
 * It checks `mutation.options.onError`, which only sees callbacks given to
 * useMutation — not the per-call ones passed to mutate(). So a mutation whose
 * component already renders the failure gets it reported twice without this.
 *
 * Apply it ONLY where the component surfaces the error somewhere the operator
 * is looking at the moment it happens — an open dialog, or a banner in the
 * section body. Applying it to a mutation that relies on the toast makes the
 * failure silent, which is strictly worse than reporting it twice.
 */
const errorsHandledLocally = { onError: () => undefined };

export interface ACMEAccount {
  name?: string;
  account?: unknown;
  directory?: string;
  location?: string;
  tos?: string;
  [key: string]: unknown;
}

export interface ACMEPlugin {
  plugin: string;
  type: string;
  api?: string;
  data?: string;
  [key: string]: unknown;
}

export interface ACMEChallengeSchemaField {
  description?: string;
  type?: string;
  optional?: number;
  default?: unknown;
  [key: string]: unknown;
}

export interface ACMEChallengeSchemaDetail {
  name?: string;
  description?: string;
  fields?: Record<string, ACMEChallengeSchemaField>;
  [key: string]: unknown;
}

export interface ACMEChallengeSchema {
  id: string;
  name: string;
  type: string;
  schema?: ACMEChallengeSchemaDetail;
  [key: string]: unknown;
}

export interface ACMEDirectory {
  name: string;
  url: string;
  [key: string]: unknown;
}

export interface NodeCertificate {
  filename?: string;
  fingerprint?: string;
  issuer?: string;
  notafter?: number;
  notbefore?: number;
  subject?: string;
  san?: string;
  [key: string]: unknown;
}

export function useACMEAccounts(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "accounts"],
    queryFn: () =>
      apiClient.list<ACMEAccount>(
        apiPath`/api/v1/clusters/${clusterId}/acme/accounts`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateACMEAccount(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name?: string;
      contact: string;
      directory?: string;
      tos_url?: string;
    }) =>
      apiClient.post<{ upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/acme/accounts`,
        data,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] });
    },
  });
}

export function useDeleteACMEAccount(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/acme/accounts/${name}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] });
    },
  });
}

export function useACMEPlugins(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "plugins"],
    queryFn: () =>
      apiClient.list<ACMEPlugin>(
        apiPath`/api/v1/clusters/${clusterId}/acme/plugins`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateACMEPlugin(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      id: string;
      type: string;
      api?: string;
      data?: string;
      "validation-delay"?: number;
    }) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/acme/plugins`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] });
    },
  });
}

export function useDeleteACMEPlugin(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/acme/plugins/${id}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "acme"] });
    },
  });
}

export function useACMEChallengeSchema(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "challenge-schema"],
    queryFn: () =>
      apiClient.list<ACMEChallengeSchema>(
        apiPath`/api/v1/clusters/${clusterId}/acme/challenge-schema`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useACMETOS(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "tos"],
    queryFn: () =>
      apiClient.get<{ url: string }>(
        apiPath`/api/v1/clusters/${clusterId}/acme/tos`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useACMEDirectories(clusterId: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "acme", "directories"],
    queryFn: () =>
      apiClient.list<ACMEDirectory>(
        apiPath`/api/v1/clusters/${clusterId}/acme/directories`,
      ),
    enabled: clusterId.length > 0,
  });
}

export interface NodeACMEConfig {
  acme?: string;
  acmedomain0?: string;
  acmedomain1?: string;
  acmedomain2?: string;
  acmedomain3?: string;
  acmedomain4?: string;
  acmedomain5?: string;
  /**
   * Keys to clear, on PUT only. Omitting a field leaves it as it was and there
   * is no value that means "remove", so removing a domain means naming its key
   * here. The server accepts `acme` and `acmedomain0`..`acmedomain5`, and
   * rejects a key that is also being given a value in the same request.
   */
  delete?: string[];
  /** SHA1 of the node config, returned by GET. Send it back on PUT to make the
   * write a compare-and-swap; omit it to overwrite unconditionally. */
  digest?: string;
  [key: string]: unknown;
}

export function useNodeACMEConfig(clusterId: string, node: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", node, "acme-config"],
    queryFn: () =>
      apiClient.get<NodeACMEConfig>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${node}/acme-config`,
      ),
    enabled: clusterId.length > 0 && node.length > 0,
  });
}

export function useSetNodeACMEConfig(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ node, config }: { node: string; config: NodeACMEConfig }) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${node}/acme-config`,
        config,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", vars.node],
      });
    },
    // ClusterACMETab's CertificatesTab renders this failure itself, in the
    // domain dialog while it is open and on the card once it closes, and a
    // digest conflict is a routine outcome here rather than an exception.
    // That tab is the ONLY place it is rendered: a second caller of this hook
    // gets its own mutation instance, so it must render the error too or the
    // failure is silent.
    ...errorsHandledLocally,
  });
}

export function useNodeCertificates(clusterId: string, node: string) {
  return useQuery({
    queryKey: ["clusters", clusterId, "nodes", node, "certificates"],
    queryFn: () =>
      apiClient.list<NodeCertificate>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${node}/certificates`,
      ),
    enabled: clusterId.length > 0 && node.length > 0,
  });
}

export function useOrderNodeCertificate(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ node, force }: { node: string; force?: boolean }) =>
      apiClient.post<{ upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${node}/certificates/order`,
        { force },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId] });
    },
  });
}

export function useRenewNodeCertificate(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ node, force }: { node: string; force?: boolean }) =>
      apiClient.put<{ upid: string }>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${node}/certificates/renew`,
        { force },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["clusters", clusterId] });
    },
  });
}
