import { useTranslation } from "react-i18next";
import { Eye, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * What can be done to a registered backup server, ranked: editing it is a
 * quiet button, and removing it, which is rare and cannot be undone, sits
 * behind More. Each is offered only when the caller says the role may run it;
 * a role that may not edit is offered `onView`, where the caller has one.
 */
export function ServerActions({
  name,
  canEdit,
  canDelete,
  onEdit,
  onView,
  onDelete,
}: {
  /** The server, to name the menu for a screen reader. */
  name: string;
  /** The role may change the server (its update route's permission). */
  canEdit: boolean;
  /** The role may remove it (its delete route's permission). */
  canDelete: boolean;
  onEdit: () => void;
  /** Opens the settings read-only, for a role that may not edit them. */
  onView?: (() => void) | undefined;
  onDelete: () => void;
}) {
  const { t } = useTranslation("backup");
  return (
    <>
      {canEdit ? (
        <Button
          variant="outline"
          size="sm"
          className="gap-1.5"
          onClick={onEdit}
        >
          <Pencil className="h-3.5 w-3.5" />
          Edit
        </Button>
      ) : (
        onView !== undefined && (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={onView}
          >
            <Eye className="h-3.5 w-3.5" />
            {t("configuration")}
          </Button>
        )
      )}
      {/* Delete is the menu's one item: without it there is no menu. */}
      {canDelete && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              className="px-2"
              aria-label={`More actions for ${name}`}
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              className="gap-2 text-destructive focus:text-destructive"
              onClick={onDelete}
            >
              <Trash2 className="h-4 w-4" />
              Delete server
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </>
  );
}
