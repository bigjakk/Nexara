import { MoreHorizontal, Pencil, Trash2 } from "lucide-react";
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
 * behind More.
 */
export function ServerActions({
  name,
  onEdit,
  onDelete,
}: {
  /** The server, to name the menu for a screen reader. */
  name: string;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <>
      <Button variant="outline" size="sm" className="gap-1.5" onClick={onEdit}>
        <Pencil className="h-3.5 w-3.5" />
        Edit
      </Button>
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
    </>
  );
}
