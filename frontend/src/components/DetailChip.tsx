import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Small metadata chip used in detail-page headers (type, VMID, node, …). */
export function DetailChip({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] leading-none text-muted-foreground",
        className,
      )}
    >
      {children}
    </span>
  );
}
