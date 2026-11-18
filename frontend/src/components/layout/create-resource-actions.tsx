import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { ContextMenuItem } from "@/components/ui/context-menu";
import { useCreateResourceStore } from "@/stores/create-resource-store";
import { useVisibleCreateActions } from "./create-actions";

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
