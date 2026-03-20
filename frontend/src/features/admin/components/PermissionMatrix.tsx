import { useMemo } from "react";
import { Checkbox } from "@/components/ui/checkbox";
import type { RBACPermission } from "@/types/api";

interface PermissionMatrixProps {
  permissions: RBACPermission[];
  selected: string[];
  onChange: (ids: string[]) => void;
}

const ACTION_ORDER = ["view", "manage", "execute", "delete"];

export function PermissionMatrix({
  permissions,
  selected,
  onChange,
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

  return (
    <div className="rounded-md border">
      <table className="text-sm">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="px-3 py-2 text-left font-medium">
              <div className="flex items-center gap-2">
                Resource
                <Checkbox
                  checked={selectedSet.size === permissions.length}
                  onCheckedChange={toggleAll}
                />
              </div>
            </th>
            {actions.map((a) => (
              <th key={a} className="px-3 py-2 text-center font-medium capitalize">
                {a}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {resources.map((resource) => (
            <tr key={resource} className="border-b last:border-b-0">
              <td className="px-3 py-1.5 capitalize">{resource}</td>
              {actions.map((action) => {
                const id = matrix.get(`${action}:${resource}`);
                if (!id) {
                  return (
                    <td key={action} className="px-3 py-1.5 text-center">
                      <span className="text-muted-foreground">-</span>
                    </td>
                  );
                }
                return (
                  <td key={action} className="px-3 py-1.5 text-center">
                    <Checkbox
                      checked={selectedSet.has(id)}
                      onCheckedChange={() => { toggle(id); }}
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
