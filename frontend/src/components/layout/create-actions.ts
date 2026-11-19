import { useCallback, type ComponentType } from "react";
import { Monitor, Container, Upload } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import type { CreateKind } from "@/stores/create-resource-store";

export interface CreateActionDef {
  kind: CreateKind;
  label: string; // label in the top "Create" dropdown
  contextLabel: string; // label in right-click context menus
  Icon: ComponentType<{ className?: string }>;
  /** What the action's create route declares: every one of them. */
  permissions: { action: string; resource: string }[];
}

// Single source of truth for the create/import entry points. Add a new
// create-type here and it appears in every menu (the Create dropdown, the tree
// and inventory context menus) with consistent gating. The command palette and
// the Imports page keep their own entries, gated through useCreateAllowed.
export const createActions: CreateActionDef[] = [
  {
    kind: "vm",
    label: "Virtual machine",
    contextLabel: "Create VM",
    Icon: Monitor,
    permissions: [{ action: "manage", resource: "vm" }],
  },
  {
    kind: "ct",
    label: "Container",
    contextLabel: "Create CT",
    Icon: Container,
    permissions: [{ action: "manage", resource: "container" }],
  },
  {
    kind: "import",
    label: "Import VM",
    contextLabel: "Import VM",
    Icon: Upload,
    // An import creates a VM.
    permissions: [
      { action: "manage", resource: "vm_import" },
      { action: "manage", resource: "vm" },
    ],
  },
];

/** Whether the signed-in user may start each kind of create. */
export function useCreateAllowed(): (kind: CreateKind) => boolean {
  const { hasPermission } = usePermissions();
  return useCallback(
    (kind: CreateKind) =>
      createActions.some(
        (a) =>
          a.kind === kind &&
          a.permissions.every((p) => hasPermission(p.action, p.resource)),
      ),
    [hasPermission],
  );
}

/** The create actions the signed-in user may run. */
export function useVisibleCreateActions(): CreateActionDef[] {
  const allowed = useCreateAllowed();
  return createActions.filter((a) => allowed(a.kind));
}
