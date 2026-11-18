import type { ComponentType } from "react";
import { Monitor, Container, Upload } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import type { CreateKind } from "@/stores/create-resource-store";

export interface CreateActionDef {
  kind: CreateKind;
  label: string; // label in the top "Create" dropdown
  contextLabel: string; // label in right-click context menus
  Icon: ComponentType<{ className?: string }>;
  /** What the action's create route declares. */
  permission: { action: string; resource: string };
}

// Single source of truth for the create/import entry points. Add a new
// create-type here and it appears in every menu (the Create dropdown, the tree
// and inventory context menus) with consistent gating. The command palette
// keeps its own entries (SearchBar.tsx), gated the same way.
export const createActions: CreateActionDef[] = [
  {
    kind: "vm",
    label: "Virtual machine",
    contextLabel: "Create VM",
    Icon: Monitor,
    permission: { action: "manage", resource: "vm" },
  },
  {
    kind: "ct",
    label: "Container",
    contextLabel: "Create CT",
    Icon: Container,
    permission: { action: "manage", resource: "container" },
  },
  {
    kind: "import",
    label: "Import VM",
    contextLabel: "Import VM",
    Icon: Upload,
    permission: { action: "manage", resource: "vm_import" },
  },
];

/** The create actions the signed-in user may run. */
export function useVisibleCreateActions(): CreateActionDef[] {
  const { hasPermission } = usePermissions();
  return createActions.filter((a) =>
    hasPermission(a.permission.action, a.permission.resource),
  );
}
