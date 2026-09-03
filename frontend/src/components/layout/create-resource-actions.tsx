import { Monitor, Container, Upload } from "lucide-react";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { ContextMenuItem } from "@/components/ui/context-menu";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useCreateResourceStore,
  type CreateKind,
} from "@/stores/create-resource-store";

interface CreateActionDef {
  kind: CreateKind;
  label: string; // label in the top "Create" dropdown
  contextLabel: string; // label in right-click context menus
  Icon: React.ComponentType<{ className?: string }>;
  permission?: { action: string; resource: string };
}

// Single source of truth for the create/import entry points. Add a new
// create-type here and it appears in every menu (Create dropdown, tree and
// inventory context menus, command palette) with consistent gating.
const createActions: CreateActionDef[] = [
  {
    kind: "vm",
    label: "Virtual Machine",
    contextLabel: "Create VM",
    Icon: Monitor,
  },
  {
    kind: "ct",
    label: "Container",
    contextLabel: "Create CT",
    Icon: Container,
  },
  {
    kind: "import",
    label: "Import VM",
    contextLabel: "Import VM",
    Icon: Upload,
    permission: { action: "manage", resource: "vm_import" },
  },
];

function useVisibleCreateActions(): CreateActionDef[] {
  const { hasPermission } = usePermissions();
  return createActions.filter(
    (a) =>
      !a.permission ||
      hasPermission(a.permission.action, a.permission.resource),
  );
}

// Items for the top "Create" dropdown. clusterId is optional — when omitted the
// globally-mounted CreateResourceDialogs resolves the cluster.
export function CreateResourceDropdownItems({
  clusterId,
  disabled,
}: {
  clusterId?: string;
  disabled?: boolean;
}) {
  const request = useCreateResourceStore((s) => s.request);
  const actions = useVisibleCreateActions();
  return (
    <>
      {actions.map((a) => (
        <DropdownMenuItem
          key={a.kind}
          disabled={disabled ?? false}
          onClick={() => {
            request(a.kind, clusterId);
          }}
        >
          <a.Icon className="mr-2 h-4 w-4" />
          {a.label}
        </DropdownMenuItem>
      ))}
    </>
  );
}

// Items for a right-click context menu scoped to a specific cluster.
export function CreateResourceContextItems({
  clusterId,
}: {
  clusterId: string;
}) {
  const request = useCreateResourceStore((s) => s.request);
  const actions = useVisibleCreateActions();
  return (
    <>
      {actions.map((a) => (
        <ContextMenuItem
          key={a.kind}
          onClick={() => {
            request(a.kind, clusterId);
          }}
        >
          <a.Icon className="mr-2 h-3.5 w-3.5" />
          {a.contextLabel}
        </ContextMenuItem>
      ))}
    </>
  );
}
