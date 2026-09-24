import { useRef, type ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

interface ConfirmDeleteDialogProps<T> {
  /**
   * What is waiting to be deleted. The dialog is open while this is non-null;
   * a row's delete button sets it instead of deleting.
   */
  target: T | null;
  /** Clears `target`. Called on Cancel, Escape, an outside click and after a confirm. */
  onClose: () => void;
  /**
   * Performs the delete. It receives the target the dialog was opened for, so a
   * row that re-renders or refetches while the dialog is open cannot swap in
   * another item.
   */
  onConfirm: (target: T) => void;
  title: (target: T) => ReactNode;
  /** Say what is lost and whether it can be undone — the dialog adds nothing. */
  description: (target: T) => ReactNode;
  confirmLabel?: string;
}

/**
 * The confirmation every destructive one-row action goes through: the button
 * opens this, and only its confirm button sends the request.
 *
 * Usage:
 *
 *     const [pendingDelete, setPendingDelete] = useState<Alias | null>(null);
 *     <Button onClick={() => { setPendingDelete(alias); }}>…</Button>
 *     <ConfirmDeleteDialog
 *       target={pendingDelete}
 *       onClose={() => { setPendingDelete(null); }}
 *       onConfirm={(a) => { deleteAlias.mutate(a.name); }}
 *       title={(a) => `Delete alias ${a.name}?`}
 *       description={() => "…"}
 *     />
 */
export function ConfirmDeleteDialog<T>({
  target,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel = "Delete",
}: ConfirmDeleteDialogProps<T>) {
  // Radix keeps the content mounted while the close animation plays, after
  // target has gone back to null; the last target keeps the text from
  // blanking out during it.
  const shown = useRef<T | null>(null);
  if (target !== null) shown.current = target;
  const current = shown.current;

  return (
    <AlertDialog
      open={target !== null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <AlertDialogContent>
        {current !== null && (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>{title(current)}</AlertDialogTitle>
              <AlertDialogDescription>
                {description(current)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                onClick={() => {
                  // Only an open dialog confirms: the closing content is
                  // still clickable for the length of its animation. The
                  // Action closes the dialog itself, through onOpenChange,
                  // so onClose runs once, there.
                  if (target !== null) onConfirm(target);
                }}
              >
                {confirmLabel}
              </AlertDialogAction>
            </AlertDialogFooter>
          </>
        )}
      </AlertDialogContent>
    </AlertDialog>
  );
}
