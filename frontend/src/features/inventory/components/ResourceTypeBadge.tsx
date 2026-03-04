import { Badge } from "@/components/ui/badge";
import { Monitor, Box, Server } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ResourceType } from "../types/inventory";

const typeConfig: Record<ResourceType, { label: string; icon: typeof Monitor; className: string }> = {
  vm: { label: "VM", icon: Monitor, className: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-400" },
  ct: { label: "CT", icon: Box, className: "border-purple-500/30 bg-purple-500/10 text-purple-700 dark:text-purple-400" },
  node: { label: "Node", icon: Server, className: "border-orange-500/30 bg-orange-500/10 text-orange-700 dark:text-orange-400" },
};

interface ResourceTypeBadgeProps {
  type: ResourceType;
}

export function ResourceTypeBadge({ type }: ResourceTypeBadgeProps) {
  const config = typeConfig[type];
  const Icon = config.icon;
  return (
    <Badge variant="outline" className={cn("gap-1 text-xs font-medium", config.className)}>
      <Icon className="h-3 w-3" />
      {config.label}
    </Badge>
  );
}
