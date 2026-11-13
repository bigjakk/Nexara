import { useState } from "react";
import { ShieldAlert } from "lucide-react";

import { Panel } from "@/components/page/Panel";
import { Segmented } from "@/components/ui/segmented";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { useAuth } from "@/hooks/useAuth";
import { useAccessCapabilities } from "@/features/access/api/access-queries";
import { AccessACLSection } from "@/features/access/components/AccessACLSection";
import { AccessGroupsSection } from "@/features/access/components/AccessGroupsSection";
import { AccessRealmsSection } from "@/features/access/components/AccessRealmsSection";
import { AccessRolesSection } from "@/features/access/components/AccessRolesSection";
import { AccessUsersSection } from "@/features/access/components/AccessUsersSection";
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

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
  // The panels are Radix tab panels, so only the view on show is mounted and
  // reads its data; the switch above them is what picks it.
  const [view, setView] = useState("users");
  const history = clusterHistories.access(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );

  if (!canView("access")) {
    return (
      <Panel>
        <p className="flex items-start gap-2.5 text-sm text-muted-foreground">
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
          You do not have permission to view this cluster&apos;s access control.
        </p>
      </Panel>
    );
  }

  const shown = view === "history" && !canHistory ? "users" : view;

  return (
    <Tabs value={shown} onValueChange={setView}>
      <Segmented
        aria-label="Access control view"
        size="md"
        value={shown}
        onValueChange={setView}
        options={[
          { value: "users", label: "Users and tokens" },
          { value: "groups", label: "Groups" },
          { value: "roles", label: "Roles" },
          { value: "permissions", label: "Permissions" },
          { value: "realms", label: "Realms" },
          ...(canHistory ? [{ value: "history", label: "History" }] : []),
        ]}
      />

      <TabsContent
        value="users"
        aria-label="Users and tokens"
        aria-labelledby={undefined}
      >
        <AccessUsersSection clusterId={clusterId} capabilities={capabilities} />
      </TabsContent>
      <TabsContent
        value="groups"
        aria-label="Groups"
        aria-labelledby={undefined}
      >
        <AccessGroupsSection
          clusterId={clusterId}
          capabilities={capabilities}
        />
      </TabsContent>
      <TabsContent value="roles" aria-label="Roles" aria-labelledby={undefined}>
        <AccessRolesSection clusterId={clusterId} capabilities={capabilities} />
      </TabsContent>
      <TabsContent
        value="permissions"
        aria-label="Permissions"
        aria-labelledby={undefined}
      >
        <AccessACLSection clusterId={clusterId} capabilities={capabilities} />
      </TabsContent>
      <TabsContent
        value="realms"
        aria-label="Realms"
        aria-labelledby={undefined}
      >
        <AccessRealmsSection clusterId={clusterId} />
      </TabsContent>
      <HistoryTabContent history={history} label="History" />
    </Tabs>
  );
}
