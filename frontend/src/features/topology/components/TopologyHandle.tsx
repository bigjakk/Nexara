import { Handle, type HandleProps } from "@xyflow/react";
import { cn } from "@/lib/utils";

/**
 * Where an edge meets a node. The map is read-only, so the handle is only an
 * anchor: not connectable, and not drawn.
 */
export function TopologyHandle({ className, ...props }: HandleProps) {
  return (
    <Handle
      isConnectable={false}
      className={cn(
        "h-px! min-h-0! w-px! min-w-0! border-0! bg-transparent!",
        className,
      )}
      {...props}
    />
  );
}
