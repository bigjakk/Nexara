import { ShieldAlert } from "lucide-react";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAuth } from "@/hooks/useAuth";
import { useAccessCapabilities } from "@/features/access/api/access-queries";
import { AccessACLSection } from "@/features/access/components/AccessACLSection";
import { AccessGroupsSection } from "@/features/access/components/AccessGroupsSection";
import { AccessRealmsSection } from "@/features/access/components/AccessRealmsSection";
import { AccessRolesSection } from "@/features/access/components/AccessRolesSection";
import { AccessUsersSection } from "@/features/access/components/AccessUsersSection";

interface ClusterAccessTabProps {
  clusterId: string;
}

/**
 * Proxmox access control for one cluster: its own users, API tokens, groups,
 * roles and ACLs.
 *
 * This manages the CLUSTER's access model. Nexara's own users and roles are a
 * separate thing entirely, under Admin.
 *
 * Sections gate on two independent things: whether the operator holds Nexara's
 * manage:access permission, and whether Nexara's cluster credential actually
 * holds the Proxmox privilege the operation needs. The second check exists
 * because two common setups fall short — a privilege-separated token has no
 * User.Modify unless granted, and the built-in PVEAdmin role carries neither
 * Sys.Modify nor Realm.Allocate. Reporting that up front beats a 403 after the
 * operator has filled in a form.
 */
export function ClusterAccessTab({ clusterId }: ClusterAccessTabProps) {
  const { canView } = useAuth();
  const capabilities = useAccessCapabilities(clusterId);

  if (!canView("access")) {
    return (
      <div className="flex items-start gap-2 rounded-md border p-4">
        <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
        <p className="text-sm text-muted-foreground">
          You do not have permission to view this cluster&apos;s access control.
        </p>
      </div>
    );
  }

  return (
    <Tabs defaultValue="users">
      <TabsList>
        <TabsTrigger value="users">Users &amp; Tokens</TabsTrigger>
        <TabsTrigger value="groups">Groups</TabsTrigger>
        <TabsTrigger value="roles">Roles</TabsTrigger>
        <TabsTrigger value="permissions">Permissions</TabsTrigger>
        <TabsTrigger value="realms">Realms</TabsTrigger>
      </TabsList>

      <TabsContent value="users" className="mt-4">
        <AccessUsersSection clusterId={clusterId} capabilities={capabilities} />
      </TabsContent>
      <TabsContent value="groups" className="mt-4">
        <AccessGroupsSection
          clusterId={clusterId}
          capabilities={capabilities}
        />
      </TabsContent>
      <TabsContent value="roles" className="mt-4">
        <AccessRolesSection clusterId={clusterId} capabilities={capabilities} />
      </TabsContent>
      <TabsContent value="permissions" className="mt-4">
        <AccessACLSection clusterId={clusterId} capabilities={capabilities} />
      </TabsContent>
      <TabsContent value="realms" className="mt-4">
        <AccessRealmsSection clusterId={clusterId} />
      </TabsContent>
    </Tabs>
  );
}
