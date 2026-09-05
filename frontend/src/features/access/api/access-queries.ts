import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

/**
 * Proxmox access control for a single cluster: PVE users, API tokens, groups,
 * roles and ACLs.
 *
 * This is the CLUSTER's own access model, not Nexara's. Nexara's local users
 * and roles live under features/admin.
 *
 * Every path segment is encodeURIComponent'd because these identifiers legally
 * contain characters that must be encoded — a PVE user id is "name@realm".
 */

/** A PVE user as returned by the list endpoint (groups is comma-separated). */
export interface AccessUser {
  userid: string;
  enable?: boolean;
  expire?: number;
  firstname?: string;
  lastname?: string;
  email?: string;
  comment?: string;
  groups?: string;
  keys?: string;
  "realm-type"?: string;
  "totp-locked"?: boolean;
  "tfa-locked-until"?: number;
  [key: string]: unknown;
}

/**
 * A PVE user as returned by the detail endpoint.
 *
 * Note groups is an ARRAY here where the list endpoint sends a comma-separated
 * string — that difference is Proxmox's, not ours.
 */
export interface AccessUserDetail {
  userid: string;
  enable?: boolean;
  expire?: number;
  firstname?: string;
  lastname?: string;
  email?: string;
  comment?: string;
  groups?: string[];
  keys?: string;
  [key: string]: unknown;
}

export interface AccessToken {
  userid: string;
  tokenid: string;
  comment?: string;
  expire?: number;
  privsep?: boolean;
  [key: string]: unknown;
}

/**
 * The one-shot result of minting or regenerating a token.
 *
 * `value` is the secret. Proxmox has no read-back endpoint, so this is the only
 * time it will ever exist outside the cluster. It must be shown to the operator
 * immediately and never persisted anywhere by the browser.
 */
export interface AccessTokenCreated {
  "full-tokenid": string;
  value: string;
  info?: { comment?: string; expire?: number; privsep?: boolean };
}

export interface AccessGroup {
  groupid: string;
  comment?: string;
  users?: string;
  [key: string]: unknown;
}

export interface AccessGroupDetail {
  groupid: string;
  comment?: string;
  members?: string[];
  [key: string]: unknown;
}

export interface AccessRole {
  roleid: string;
  privs?: string;
  special?: boolean;
  [key: string]: unknown;
}

export interface AccessACLEntry {
  path: string;
  type: string;
  ugid: string;
  roleid: string;
  propagate?: boolean;
  [key: string]: unknown;
}

export interface AccessDomain {
  realm: string;
  type?: string;
  comment?: string;
  tfa?: string;
  default?: boolean;
  [key: string]: unknown;
}

/** path -> privilege -> propagate. */
export type AccessPermissions = Record<string, Record<string, boolean>>;

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

const base = (clusterId: string) => `/api/v1/clusters/${clusterId}/access`;
const key = (clusterId: string, ...rest: string[]) => [
  "clusters",
  clusterId,
  "access",
  ...rest,
];

/** Invalidates every access query for a cluster. */
function invalidateAccess(
  qc: ReturnType<typeof useQueryClient>,
  clusterId: string,
) {
  void qc.invalidateQueries({ queryKey: ["clusters", clusterId, "access"] });
}

// ── Users ──────────────────────────────────────────────────────────────

export function useAccessUsers(clusterId: string) {
  return useQuery({
    queryKey: key(clusterId, "users"),
    queryFn: () => apiClient.list<AccessUser>(`${base(clusterId)}/users`),
    enabled: clusterId.length > 0,
  });
}

export function useAccessUser(clusterId: string, userid: string) {
  return useQuery({
    queryKey: key(clusterId, "users", userid),
    queryFn: () =>
      apiClient.get<AccessUserDetail>(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}`,
      ),
    enabled: clusterId.length > 0 && userid.length > 0,
  });
}

export interface CreateAccessUserInput {
  userid: string;
  password?: string;
  comment?: string;
  email?: string;
  firstname?: string;
  lastname?: string;
  groups?: string;
  enable?: boolean;
  expire?: number;
}

export function useCreateAccessUser(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateAccessUserInput) =>
      apiClient.post(`${base(clusterId)}/users`, data),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useUpdateAccessUser(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      userid,
      ...data
    }: { userid: string } & Omit<
      CreateAccessUserInput,
      "userid" | "password"
    >) =>
      apiClient.put(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}`,
        data,
      ),
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useDeleteAccessUser(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ userid, force }: { userid: string; force?: boolean }) =>
      apiClient.delete(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}${force ? "?force=true" : ""}`,
      ),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

// ── API tokens ─────────────────────────────────────────────────────────

export function useAccessTokens(clusterId: string, userid: string) {
  return useQuery({
    queryKey: key(clusterId, "users", userid, "tokens"),
    queryFn: () =>
      apiClient.list<AccessToken>(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}/tokens`,
      ),
    enabled: clusterId.length > 0 && userid.length > 0,
  });
}

export function useCreateAccessToken(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      userid,
      tokenid,
      ...data
    }: {
      userid: string;
      tokenid: string;
      comment?: string;
      expire?: number;
      privsep?: boolean;
    }) =>
      apiClient.post<AccessTokenCreated>(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}/tokens/${encodeURIComponent(tokenid)}`,
        data,
      ),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useUpdateAccessToken(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      userid,
      tokenid,
      force,
      ...data
    }: {
      userid: string;
      tokenid: string;
      force?: boolean;
      comment?: string;
      expire?: number;
      privsep?: boolean;
      regenerate?: boolean;
    }) =>
      apiClient.put<AccessTokenCreated>(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}/tokens/${encodeURIComponent(tokenid)}${force ? "?force=true" : ""}`,
        data,
      ),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useDeleteAccessToken(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      userid,
      tokenid,
      force,
    }: {
      userid: string;
      tokenid: string;
      force?: boolean;
    }) =>
      apiClient.delete(
        `${base(clusterId)}/users/${encodeURIComponent(userid)}/tokens/${encodeURIComponent(tokenid)}${force ? "?force=true" : ""}`,
      ),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

// ── Groups ─────────────────────────────────────────────────────────────

export function useAccessGroups(clusterId: string) {
  return useQuery({
    queryKey: key(clusterId, "groups"),
    queryFn: () => apiClient.list<AccessGroup>(`${base(clusterId)}/groups`),
    enabled: clusterId.length > 0,
  });
}

export function useAccessGroup(clusterId: string, groupid: string) {
  return useQuery({
    queryKey: key(clusterId, "groups", groupid),
    queryFn: () =>
      apiClient.get<AccessGroupDetail>(
        `${base(clusterId)}/groups/${encodeURIComponent(groupid)}`,
      ),
    enabled: clusterId.length > 0 && groupid.length > 0,
  });
}

export function useCreateAccessGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { groupid: string; comment?: string }) =>
      apiClient.post(`${base(clusterId)}/groups`, data),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useUpdateAccessGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupid, comment }: { groupid: string; comment: string }) =>
      apiClient.put(
        `${base(clusterId)}/groups/${encodeURIComponent(groupid)}`,
        { comment },
      ),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useDeleteAccessGroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (groupid: string) =>
      apiClient.delete(
        `${base(clusterId)}/groups/${encodeURIComponent(groupid)}`,
      ),
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

// ── Roles ──────────────────────────────────────────────────────────────

export function useAccessRoles(clusterId: string) {
  return useQuery({
    queryKey: key(clusterId, "roles"),
    queryFn: () => apiClient.list<AccessRole>(`${base(clusterId)}/roles`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateAccessRole(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { roleid: string; privs: string }) =>
      apiClient.post(`${base(clusterId)}/roles`, data),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useUpdateAccessRole(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    // privs is always sent: the API rejects an omitted value rather than
    // treating it as "clear", because clearing strips every privilege.
    mutationFn: ({ roleid, privs }: { roleid: string; privs: string }) =>
      apiClient.put(`${base(clusterId)}/roles/${encodeURIComponent(roleid)}`, {
        privs,
      }),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

export function useDeleteAccessRole(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (roleid: string) =>
      apiClient.delete(
        `${base(clusterId)}/roles/${encodeURIComponent(roleid)}`,
      ),
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

// ── ACL ────────────────────────────────────────────────────────────────

export function useAccessACL(clusterId: string) {
  return useQuery({
    queryKey: key(clusterId, "acl"),
    queryFn: () => apiClient.list<AccessACLEntry>(`${base(clusterId)}/acl`),
    enabled: clusterId.length > 0,
  });
}

export interface UpdateACLInput {
  path: string;
  roles: string;
  users?: string;
  groups?: string;
  tokens?: string;
  propagate?: boolean;
  delete?: boolean;
}

/**
 * Grants, or revokes when `delete` is set — Proxmox uses one endpoint for both.
 *
 * Because both directions share this mutation, the global toast is suppressed
 * for both. AccessACLSection therefore has to surface revoke failures itself,
 * not only the grant-dialog ones.
 */
export function useUpdateAccessACL(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: UpdateACLInput) =>
      apiClient.put(`${base(clusterId)}/acl`, data),
    ...errorsHandledLocally,
    onSuccess: () => {
      invalidateAccess(qc, clusterId);
    },
  });
}

// ── Realms + capability probe ──────────────────────────────────────────

export function useAccessDomains(clusterId: string) {
  return useQuery({
    queryKey: key(clusterId, "domains"),
    queryFn: () => apiClient.list<AccessDomain>(`${base(clusterId)}/domains`),
    enabled: clusterId.length > 0,
  });
}

/**
 * What Nexara's own cluster credential is permitted to do.
 *
 * The upstream endpoint answers for any authenticated caller regardless of its
 * privileges, so this is dependable even when the token can do nothing else.
 * Used to disable sections up front rather than surfacing a 403 after a form
 * submit — see useAccessCapabilities.
 */
export function useAccessPermissions(clusterId: string) {
  return useQuery({
    queryKey: key(clusterId, "permissions"),
    queryFn: () =>
      apiClient.get<AccessPermissions>(`${base(clusterId)}/permissions`),
    enabled: clusterId.length > 0,
    staleTime: 5 * 60_000,
  });
}

export interface AccessCapabilities {
  loading: boolean;
  /** Manage PVE users, groups and API tokens. */
  canModifyUsers: boolean;
  /** Create, edit and delete custom roles. */
  canModifyRoles: boolean;
  /** Grant and revoke ACL entries. */
  canModifyACL: boolean;
  /** Manage authentication realms. Nothing but Administrator carries this. */
  canModifyRealms: boolean;
}

/**
 * Derives what Nexara's cluster token can actually do from its privilege map.
 *
 * Two common setups fall short in ways worth reporting before the operator
 * fills in a form: a privilege-separated token holds no User.Modify unless one
 * was granted explicitly, and the built-in PVEAdmin role carries neither
 * Sys.Modify (needed for role management) nor Realm.Allocate.
 *
 * Privileges propagate down the ACL tree, so a privilege held on "/" applies
 * everywhere — hence checking the root path alongside the specific one.
 */
export function useAccessCapabilities(clusterId: string): AccessCapabilities {
  const { data, isLoading } = useAccessPermissions(clusterId);

  /**
   * The response shape is `path -> privilege -> propagate`. Key PRESENCE means
   * the privilege is held on that path; the VALUE is only whether it also
   * applies to child paths.
   *
   * So the two checks are deliberately different: on the exact path we ask
   * whether the key exists, while on an ancestor we require the propagate flag
   * to be true. Treating the value as "held" everywhere reported a grant made
   * directly on /access with propagate unticked — the natural way to scope
   * one — as absent, and disabled a section that would have worked fine.
   */
  const has = (path: string, priv: string): boolean => {
    if (!data) return false;
    if (data[path] && priv in data[path]) return true;

    // Walk the ancestors, nearest first; a propagating grant covers this path.
    const parts = path.split("/").filter(Boolean);
    for (let i = parts.length - 1; i >= 0; i--) {
      const ancestor = "/" + parts.slice(0, i).join("/");
      const normalised = ancestor === "/" ? "/" : ancestor;
      if (data[normalised]?.[priv]) return true;
    }
    return Boolean(data["/"]?.[priv]);
  };

  return {
    loading: isLoading,
    canModifyUsers: has("/access", "User.Modify"),
    canModifyRoles: has("/access", "Sys.Modify"),
    canModifyACL: has("/access", "Permissions.Modify"),
    canModifyRealms: has("/access/realm", "Realm.Allocate"),
  };
}
