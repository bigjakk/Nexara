import { useTranslation } from "react-i18next";
import { useNavigate, useLocation } from "react-router-dom";
import { ChevronRight, Server, Star, Wrench } from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import { StatusIcon } from "@/components/StatusIcon";
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
      className={cn(
        "flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
        isActive && "bg-primary/10 text-foreground",
      )}
    >
      {fav.resource_type === "cluster" && (
        <>
          <StatusIcon status={clusterStatus} />
          <Server className="h-3.5 w-3.5 shrink-0 text-primary" />
          <span className="truncate font-medium">{fav.name}</span>
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
          <StatusIcon status={fav.status} />
          <VMIcon type={fav.vm_kind} template={fav.template} />
          {(classifyOS(fav.ostype) !== "unknown" ||
            classifyOS(fav.config_ostype) !== "unknown") && (
            <OSIcon ostype={fav.ostype} configOstype={fav.config_ostype} />
          )}
          <span
            className={cn(
              "truncate",
              fav.template && "text-amber-700 dark:text-amber-400",
            )}
          >
            {fav.vmid} {fav.name}
          </span>
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
    <div className="border-b py-1">
      <button
        onClick={toggleCollapsed}
        aria-expanded={!collapsed}
        className="flex w-full items-center gap-1 px-2 pb-1 text-[11px] font-medium uppercase tracking-wider text-muted-foreground/70 hover:text-muted-foreground"
      >
        <ChevronRight
          className={cn(
            "h-3 w-3 transition-transform",
            !collapsed && "rotate-90",
          )}
        />
        <Star className="h-3 w-3" />
        <span>{t("favorites")}</span>
        <span className="ml-auto tabular-nums">{favorites.length}</span>
      </button>

      {!collapsed && (
        <div className="space-y-0.5 px-1">
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
