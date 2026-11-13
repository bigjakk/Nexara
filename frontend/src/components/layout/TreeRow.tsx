import type { ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { OSIcon } from "@/components/OSIcon";
import { StatusIcon } from "@/components/StatusIcon";
import { VMIcon } from "@/components/VMIcon";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import { classifyOS } from "@/lib/os-classify";
import { cn } from "@/lib/utils";
import type { VMResponse } from "@/types/api";
import { treeIndent, treeRowClass } from "./tree-row-style";

/** A branch row's chevron. The row's style is in tree-row-style.ts. */
export function TreeChevron({
  expanded,
  hidden = false,
}: {
  expanded: boolean;
  /** Nothing to open: keeps its width so the labels still line up. */
  hidden?: boolean;
}) {
  return (
    <ChevronRight
      aria-hidden="true"
      className={cn(
        "h-3.5 w-3.5 transition-transform",
        hidden && "invisible",
        expanded && "rotate-90",
      )}
    />
  );
}

/** Small print at a row's right edge: a VMID, a count. */
export function TreeRowAside({ children }: { children: ReactNode }) {
  return (
    <span className="ml-auto shrink-0 pl-1.5 text-[11.5px] font-normal tabular-nums text-muted-foreground">
      {children}
    </span>
  );
}

/** A tree's one-line heading: what the rows below are grouped by. */
export function TreeHeading({
  children,
  action,
}: {
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex h-7 items-center justify-between pl-2.5 pr-1.5">
      <span className="text-[12.5px] text-muted-foreground">{children}</span>
      {action}
    </div>
  );
}

/** A line of small print in place of rows: loading, empty, nothing matches. */
export function TreeNote({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <p
      className={cn(
        "px-2.5 py-1.5 text-[12.5px] text-muted-foreground",
        className,
      )}
    >
      {children}
    </p>
  );
}

/** The first-load placeholder: two rows' worth. */
export function TreeSkeleton() {
  return (
    <div className="space-y-1.5 px-2.5 py-1" aria-hidden="true">
      <div className="h-4 w-3/4 animate-pulse rounded bg-foreground/[0.07]" />
      <div className="h-4 w-1/2 animate-pulse rounded bg-foreground/[0.07]" />
    </div>
  );
}

/**
 * A guest in the tree: its state, what it is, its name, and its VMID as small
 * print. One component for the Hosts and Folders groupings, so a guest reads
 * the same in both and carries the same menu.
 */
export function TreeGuestRow({
  vm,
  clusterId,
  currentNode,
  depth,
}: {
  vm: VMResponse;
  clusterId: string;
  /** The name of the node it runs on, for the menu's Migrate and Console. */
  currentNode: string;
  depth: number;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const kind = vm.type === "lxc" ? "lxc" : "qemu";
  const path = `/inventory/${kind}/${clusterId}/${vm.id}`;
  // The guest's page is reached under more than one kind segment (the tree
  // links to qemu and lxc, the inventory table to vm and ct), so the open row
  // is told by its cluster and id, whichever link led there.
  const open = /^\/inventory\/[^/]+\/([^/]+)\/([^/]+)$/.exec(location.pathname);
  const active = open?.[1] === clusterId && open[2] === vm.id;
  const knownOS =
    classifyOS(vm.ostype) !== "unknown" ||
    classifyOS(vm.config_ostype) !== "unknown";

  return (
    <VMContextMenu
      target={{
        clusterId,
        resourceId: vm.id,
        vmid: vm.vmid,
        name: vm.name,
        kind: vm.type === "lxc" ? "ct" : "vm",
        status: vm.status,
        currentNode,
        template: vm.template,
      }}
    >
      <button
        onClick={() => {
          void navigate(path);
        }}
        title={`${vm.name} (${String(vm.vmid)})`}
        className={treeRowClass({ active })}
        style={treeIndent(depth, true)}
      >
        <StatusIcon status={vm.status} />
        <VMIcon type={vm.type} template={vm.template} />
        {knownOS && (
          <OSIcon
            ostype={vm.ostype}
            configOstype={vm.config_ostype}
            className="h-3.5 w-3.5"
          />
        )}
        <span
          className={cn("truncate", vm.template && "text-muted-foreground")}
        >
          {vm.name}
        </span>
        <TreeRowAside>{vm.vmid}</TreeRowAside>
      </button>
    </VMContextMenu>
  );
}
