import { useTranslation } from "react-i18next";
import { useNavigate, useLocation } from "react-router-dom";
import { Server, Wrench } from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import { TreeChevron, TreeRowAside } from "@/components/layout/TreeRow";
import {
  TREE_ROW_TOGGLE,
  treeIndent,
  treeRowClass,
} from "@/components/layout/tree-row-style";
import { StatusIcon } from "@/components/StatusIcon";
import { guestState } from "@/lib/guest-state";
import { OSIcon } from "@/components/OSIcon";
import { VMIcon } from "@/components/VMIcon";
import { classifyOS } from "@/lib/os-classify";
import { useSidebarStore } from "@/stores/sidebar-store";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import type { Favorite } from "@/types/api";
import { useFavorites, type FavoriteTarget } from "../api/favorites-queries";
import { FavoriteContextItem } from "./FavoriteMenuItem";

/** The identity triple to send back when unstarring this row. */
function targetOf(fav: Favorite): FavoriteTarget {
  return {
    resource_type: fav.resource_type,
    cluster_id: fav.cluster_id,
    ref: fav.ref,
  };
}

/**
 * Where clicking a favorite goes.
 *
 * Built from target_id, which the server re-resolves on every read — the
 * collector re-issues these row UUIDs, so a link built from a cached id would
 * 404 after a live migration.
 */
function routeOf(fav: Favorite): string {
  switch (fav.resource_type) {
    case "cluster":
      return `/clusters/${fav.cluster_id}`;
    case "node":
      return `/clusters/${fav.cluster_id}/nodes/${fav.target_id}`;
    case "vm": {
      const kind = fav.vm_kind === "lxc" ? "lxc" : "qemu";
      return `/inventory/${kind}/${fav.cluster_id}/${fav.target_id}`;
    }
  }
}

/** A stable React key. target_id churns; the identity triple does not. */
function keyOf(fav: Favorite): string {
  return `${fav.resource_type}:${fav.cluster_id}:${fav.ref}`;
}

function FavoriteRow({
  fav,
  clusterStatus,
}: {
  fav: Favorite;
  /** Only read for a cluster row; see the note in FavoritesSection. */
  clusterStatus: string;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const path = routeOf(fav);
  const isActive = location.pathname === path;

  const row = (
    <button
      onClick={() => {
        void navigate(path);
      }}
      title={
        fav.resource_type === "cluster"
          ? fav.name
          : `${fav.name} · ${fav.cluster_name}`
      }
      // The tree's own row, so the panel reads as one list. A favourite has
      // nothing to open under it, so it steps in as a leaf does and its dot
      // lines up with the dots of the cluster rows below.
      className={treeRowClass({ active: isActive })}
      style={treeIndent(0, true)}
    >
      {fav.resource_type === "cluster" && (
        <>
          <StatusIcon status={clusterStatus} />
          <span className="truncate font-semibold text-foreground">
            {fav.name}
          </span>
        </>
      )}

      {fav.resource_type === "node" && (
        <>
          {/* The tree replaces the status dot with a wrench for a node in HA
              maintenance. A starred node has to say the same thing two inches
              above it, or the same node reads as healthy in one panel and
              drained in the other. */}
          {fav.ha_state === "maintenance" ? (
            <Wrench
              aria-label="Maintenance"
              className="h-3 w-3 shrink-0 text-amber-600 dark:text-amber-500"
            />
          ) : (
            <StatusIcon status={fav.status} />
          )}
          <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{fav.name}</span>
        </>
      )}

      {fav.resource_type === "vm" && (
        <>
          <StatusIcon status={guestState(fav.status, fav.run_state)} />
          <VMIcon type={fav.vm_kind} template={fav.template} />
          {(classifyOS(fav.ostype) !== "unknown" ||
            classifyOS(fav.config_ostype) !== "unknown") && (
            <OSIcon
              ostype={fav.ostype}
              configOstype={fav.config_ostype}
              className="h-3.5 w-3.5"
            />
          )}
          <span
            className={cn("truncate", fav.template && "text-muted-foreground")}
          >
            {fav.name}
          </span>
          <TreeRowAside>{fav.vmid}</TreeRowAside>
        </>
      )}
    </button>
  );

  // Guests get the full guest menu, so a starred VM can be started, consoled or
  // migrated from here exactly as it can from the tree — a shortcut that only
  // navigates would be a worse version of the row it replaces.
  if (fav.resource_type === "vm") {
    return (
      <VMContextMenu
        target={{
          clusterId: fav.cluster_id,
          resourceId: fav.target_id,
          vmid: fav.vmid,
          name: fav.name,
          kind: fav.vm_kind === "lxc" ? "ct" : "vm",
          status: fav.status,
          runState: fav.run_state,
          currentNode: fav.node_name,
          template: fav.template,
        }}
      >
        {row}
      </VMContextMenu>
    );
  }

  return (
    <ContextMenu modal={false}>
      <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
      <ContextMenuContent className="w-48">
        <FavoriteContextItem target={targetOf(fav)} />
      </ContextMenuContent>
    </ContextMenu>
  );
}

/**
 * The caller's starred resources, pinned above the tree.
 *
 * Rendered outside the perspective switch because a favorite is not a
 * perspective — the same shortcut list is wanted whether the tree below is
 * showing hosts, guests or storage.
 *
 * Hidden entirely when nothing is starred. An empty "Favorites" heading would
 * cost a permanent two lines at the top of every sidebar to advertise a feature
 * whose entry point is the context menu on the rows below it.
 */
export function FavoritesSection() {
  const { t } = useTranslation("common");
  const { data: favorites } = useFavorites();
  // Clusters carry no status of their own — it is computed from their nodes —
  // so read it from the cluster list the tree below has already loaded rather
  // than recomputing it server-side for this one row. Resolved once here
  // rather than per row: every row would otherwise subscribe to the cluster
  // query to answer a question only cluster rows ask.
  const { data: clusters } = useClusters();
  const collapsed = useSidebarStore((s) => s.favoritesCollapsed);
  const toggleCollapsed = useSidebarStore((s) => s.toggleFavoritesCollapsed);

  if (!favorites || favorites.length === 0) {
    return null;
  }

  return (
    <div className="mb-1 border-b pb-1">
      {/* The heading is the fold: the tree's heading line, with its chevron. */}
      <button
        onClick={toggleCollapsed}
        aria-expanded={!collapsed}
        className="group/fav flex h-7 w-full items-center gap-1.5 pl-2.5 pr-2 text-left text-[12.5px] text-muted-foreground outline-hidden hover:text-foreground focus-visible:bg-foreground/[0.07]"
      >
        <span
          className={cn(TREE_ROW_TOGGLE, "group-hover/fav:text-foreground")}
        >
          <TreeChevron expanded={!collapsed} />
        </span>
        <span>{t("favorites")}</span>
        <TreeRowAside>{favorites.length}</TreeRowAside>
      </button>

      {!collapsed && (
        <div>
          {favorites.map((fav) => (
            <FavoriteRow
              key={keyOf(fav)}
              fav={fav}
              clusterStatus={
                clusters?.find((c) => c.id === fav.cluster_id)?.status ??
                "unknown"
              }
            />
          ))}
        </div>
      )}
    </div>
  );
}
