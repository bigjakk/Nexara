import type { CephCrushRule } from "../types/ceph";

interface CrushRuleTableProps {
  rules: CephCrushRule[];
}

function ruleTypeName(type: number): string {
  switch (type) {
    case 1:
      return "replicated";
    case 3:
      return "erasure";
    default:
      return String(type);
  }
}

export function CrushRuleTable({ rules }: CrushRuleTableProps) {
  return (
    <div className="overflow-x-auto rounded-md border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="px-4 py-2 text-left font-medium">ID</th>
            <th className="px-4 py-2 text-left font-medium">Name</th>
            <th className="px-4 py-2 text-left font-medium">Type</th>
            <th className="px-4 py-2 text-right font-medium">Min Size</th>
            <th className="px-4 py-2 text-right font-medium">Max Size</th>
          </tr>
        </thead>
        <tbody>
          {rules.map((rule) => (
            <tr key={rule.rule_id} className="border-b last:border-0">
              <td className="px-4 py-2 font-mono">{rule.rule_id}</td>
              <td className="px-4 py-2 font-medium">{rule.rule_name}</td>
              <td className="px-4 py-2">{ruleTypeName(rule.type)}</td>
              <td className="px-4 py-2 text-right font-mono">
                {rule.min_size}
              </td>
              <td className="px-4 py-2 text-right font-mono">
                {rule.max_size}
              </td>
            </tr>
          ))}
          {rules.length === 0 && (
            <tr>
              <td
                colSpan={5}
                className="px-4 py-8 text-center text-muted-foreground"
              >
                No CRUSH rules found.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
