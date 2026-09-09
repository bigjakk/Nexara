import { Star, StarOff } from "lucide-react";
import { ContextMenuItem } from "@/components/ui/context-menu";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import {
  useIsFavorite,
  useToggleFavorite,
  type FavoriteTarget,
} from "../api/favorites-queries";

/**
 * The star/unstar action, resolved against the caller's current favorites.
 *
 * Split out from the two menu wrappers below so both read the same state and
 * write it the same way — the cluster row in the sidebar carries a context menu
 * AND a hover dropdown listing the same actions, and those two must never
 * disagree about whether something is starred.
 */
function useFavoriteAction(target: FavoriteTarget) {
  const isFavorite = useIsFavorite(target);
  const toggle = useToggleFavorite();

  return {
    isFavorite,
    label: isFavorite ? "Remove from Favorites" : "Add to Favorites",
    Icon: isFavorite ? StarOff : Star,
    disabled: toggle.isPending,
    run: () => {
      toggle.mutate({ target, favorited: !isFavorite });
    },
  };
}

/** Star/unstar, for a right-click context menu. */
export function FavoriteContextItem({
  target,
  onAction,
}: {
  target: FavoriteTarget;
  /** Lets a host that owns the menu (e.g. the command palette) close itself,
   * the same way every other item in that menu does. */
  onAction?: (() => void) | undefined;
}) {
  const { label, Icon, disabled, run } = useFavoriteAction(target);
  return (
    <ContextMenuItem
      onClick={() => {
        onAction?.();
        run();
      }}
      disabled={disabled}
    >
      <Icon className="mr-2 h-3.5 w-3.5" />
      {label}
    </ContextMenuItem>
  );
}

/**
 * Star/unstar, for a hand-rolled menu built from plain buttons.
 *
 * The inventory table renders its row menu into a portal with its own markup
 * rather than through Radix, so it cannot host either item above. The classes
 * are copied from that menu's own buttons; the state and the write still come
 * from useFavoriteAction, so all three variants agree.
 */
export function FavoritePlainItem({
  target,
  onAction,
}: {
  target: FavoriteTarget;
  onAction?: (() => void) | undefined;
}) {
  const { label, Icon, disabled, run } = useFavoriteAction(target);
  return (
    <button
      disabled={disabled}
      onClick={() => {
        run();
        onAction?.();
      }}
      className="relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-hidden hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50"
    >
      <span className="mr-2">
        <Icon className="h-4 w-4" />
      </span>
      {label}
    </button>
  );
}

/** Star/unstar, for a hover dropdown menu. */
export function FavoriteDropdownItem({ target }: { target: FavoriteTarget }) {
  const { label, Icon, disabled, run } = useFavoriteAction(target);
  return (
    <DropdownMenuItem onClick={run} disabled={disabled}>
      <Icon className="mr-2 h-3.5 w-3.5" />
      {label}
    </DropdownMenuItem>
  );
}
