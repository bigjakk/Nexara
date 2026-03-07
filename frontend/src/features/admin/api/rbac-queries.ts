import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  RBACRole,
  RBACPermission,
  RBACUserRole,
  UserListItem,
  MyPermissionsResponse,
} from "@/types/api";

// -- Roles --

export function useRoles() {
  return useQuery({
    queryKey: ["rbac", "roles"],
    queryFn: () => apiClient.get<RBACRole[]>("/api/v1/rbac/roles"),
  });
}

export function useRole(id: string) {
  return useQuery({
    queryKey: ["rbac", "roles", id],
    queryFn: () => apiClient.get<RBACRole>(`/api/v1/rbac/roles/${id}`),
    enabled: !!id,
  });
}

export function useCreateRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      name: string;
      description: string;
      permission_ids: string[];
    }) => apiClient.post<RBACRole>("/api/v1/rbac/roles", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["rbac", "roles"] });
    },
  });
}

export function useUpdateRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...data
    }: {
      id: string;
      name?: string;
      description?: string;
      permission_ids?: string[];
    }) => apiClient.put<RBACRole>(`/api/v1/rbac/roles/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["rbac", "roles"] });
    },
  });
}

export function useDeleteRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/rbac/roles/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["rbac", "roles"] });
    },
  });
}

// -- Permissions --

export function usePermissionCatalog() {
  return useQuery({
    queryKey: ["rbac", "permissions"],
    queryFn: () =>
      apiClient.get<RBACPermission[]>("/api/v1/rbac/permissions"),
  });
}

// -- User Roles --

export function useUserRoles(userId: string) {
  return useQuery({
    queryKey: ["rbac", "user-roles", userId],
    queryFn: () =>
      apiClient.get<RBACUserRole[]>(`/api/v1/rbac/users/${userId}/roles`),
    enabled: !!userId,
  });
}

export function useAssignRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      userId,
      ...data
    }: {
      userId: string;
      role_id: string;
      scope_type: string;
      scope_id?: string;
    }) =>
      apiClient.post(`/api/v1/rbac/users/${userId}/roles`, data),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rbac", "user-roles", vars.userId],
      });
    },
  });
}

export function useRevokeRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      userId,
      assignmentId,
    }: {
      userId: string;
      assignmentId: string;
    }) =>
      apiClient.delete(
        `/api/v1/rbac/users/${userId}/roles/${assignmentId}`,
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: ["rbac", "user-roles", vars.userId],
      });
    },
  });
}

// -- My Permissions --

export function useMyPermissions() {
  return useQuery({
    queryKey: ["rbac", "me", "permissions"],
    queryFn: () =>
      apiClient.get<MyPermissionsResponse>("/api/v1/rbac/me/permissions"),
  });
}

// -- Users --

export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      email: string;
      password: string;
      display_name?: string;
    }) => apiClient.post("/api/v1/auth/register", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });
}

export function useUsers() {
  return useQuery({
    queryKey: ["admin", "users"],
    queryFn: () => apiClient.get<UserListItem[]>("/api/v1/users"),
  });
}

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...data
    }: {
      id: string;
      display_name?: string;
      is_active?: boolean;
      role?: string;
    }) => apiClient.put<UserListItem>(`/api/v1/users/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });
}

export function useDeleteUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/users/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });
}
