import { useMemo } from "react";
import { Checkbox } from "@/components/ui/checkbox";
import { cn } from "@/lib/utils";
import type { RBACPermission } from "@/types/api";

interface PermissionMatrixProps {
  permissions: RBACPermission[];
  selected: string[];
  onChange: (ids: string[]) => void;
  /** On the scroller: the negative margin that runs it to a dialog's edges. */
  className?: string;
}

const ACTION_ORDER = ["view", "console", "manage", "execute", "delete"];

export function PermissionMatrix({
  permissions,
  selected,
  onChange,
  className,
}: PermissionMatrixProps) {
  const { resources, actions, matrix } = useMemo(() => {
    const resourceSet = new Set<string>();
    const actionSet = new Set<string>();
    const m = new Map<string, string>();

    for (const p of permissions) {
      resourceSet.add(p.resource);
      actionSet.add(p.action);
      m.set(`${p.action}:${p.resource}`, p.id);
    }

    const sortedActions = [...actionSet].sort(
      (a, b) => ACTION_ORDER.indexOf(a) - ACTION_ORDER.indexOf(b),
    );
    const sortedResources = [...resourceSet].sort();

    return { resources: sortedResources, actions: sortedActions, matrix: m };
  }, [permissions]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);

  const toggle = (id: string) => {
    const next = new Set(selectedSet);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    onChange([...next]);
  };

  const toggleAll = () => {
    if (selectedSet.size === permissions.length) {
      onChange([]);
    } else {
      onChange(permissions.map((p) => p.id));
    }
  };

  // The scroller is the matrix's own, so the action names and the resource
  // column stay put while the rest moves under them. Sticky cells are opaque
  // (the dialog's surface) and the table keeps separate borders, which is what
  // lets a hairline travel with a sticky cell.
  const headClass =
    "sticky top-0 border-b bg-popover px-3 py-2 text-xs font-medium text-muted-foreground last:pr-6";
  const cellClass = "border-b px-3 py-1.5 text-center last:pr-6";
  return (
    <div className={cn("max-h-[55vh] overflow-auto border-t", className)}>
      <table className="w-full border-separate border-spacing-0 text-sm">
        <thead>
          <tr>
            <th
              scope="col"
              className={cn(headClass, "left-0 z-20 pl-6 text-left")}
            >
              <div className="flex items-center gap-2">
                Resource
                <Checkbox
                  aria-label="All permissions"
                  checked={selectedSet.size === permissions.length}
                  onCheckedChange={toggleAll}
                />
              </div>
            </th>
            {actions.map((a) => (
              <th
                key={a}
                scope="col"
                className={cn(headClass, "z-10 text-center capitalize")}
              >
                {a}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {resources.map((resource) => (
            <tr key={resource}>
              <th
                scope="row"
                className="sticky left-0 z-10 whitespace-nowrap border-b bg-popover py-1.5 pl-6 pr-3 text-left font-normal capitalize"
              >
                {resource.replace(/_/g, " ")}
              </th>
              {actions.map((action) => {
                const id = matrix.get(`${action}:${resource}`);
                if (!id) {
                  return (
                    <td key={action} className={cellClass}>
                      <span className="text-muted-foreground/50">-</span>
                    </td>
                  );
                }
                return (
                  <td key={action} className={cellClass}>
                    <Checkbox
                      aria-label={`${action} ${resource.replace(/_/g, " ")}`}
                      checked={selectedSet.has(id)}
                      onCheckedChange={() => {
                        toggle(id);
                      }}
                    />
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
