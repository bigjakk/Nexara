import { useId, useRef, useState, type ReactNode } from "react";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface TypeToConfirmDialogProps<T> {
  /** What is waiting to be confirmed. The dialog is open while this is non-null. */
  target: T | null;
  /** Clears `target`: on Cancel, Escape, an outside click and after a confirm. */
  onClose: () => void;
  /** Goes ahead, with the target the dialog opened for and the name as typed. */
  onConfirm: (target: T, typed: string) => void;
  title: (target: T) => ReactNode;
  /** What happens, and to what. */
  description: (target: T) => ReactNode;
  /** The name to type, exactly. */
  name: (target: T) => string;
  confirmLabel: string;
  /** What else to read first, such as what the action affects. */
  details?: (target: T) => ReactNode;
}

/**
 * An action drastic enough to take a name typed: the confirm button stays shut
 * until the field holds the name exactly. Wherever the server can, it checks
 * the name again, so an API caller meets the same bar.
 */
export function TypeToConfirmDialog<T>({
  target,
  onClose,
  onConfirm,
  title,
  description,
  name,
  confirmLabel,
  details,
}: TypeToConfirmDialogProps<T>) {
  const id = useId();
  const [typed, setTyped] = useState("");
  // Radix keeps the content mounted while the close animation plays; the last
  // target keeps its text from blanking out meanwhile.
  const shown = useRef<T | null>(null);
  if (target !== null) shown.current = target;
  const current = shown.current;
  const expected = current === null ? "" : name(current);

  return (
    <AlertDialog
      open={target !== null}
      onOpenChange={(open) => {
        if (!open) {
          setTyped("");
          onClose();
        }
      }}
    >
      <AlertDialogContent>
        {current !== null && (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>{title(current)}</AlertDialogTitle>
              <AlertDialogDescription>{description(current)}</AlertDialogDescription>
            </AlertDialogHeader>
            {details?.(current)}
            <div className="space-y-1.5">
              <Label htmlFor={id}>
                Type <span className="font-mono font-semibold">{expected}</span> to confirm
              </Label>
              <Input
                id={id}
                autoComplete="off"
                spellCheck={false}
                value={typed}
                onChange={(e) => {
                  setTyped(e.target.value);
                }}
              />
            </div>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={typed !== expected}
                onClick={() => {
                  // Only an open dialog confirms: the closing content is still
                  // clickable for the length of its animation.
                  if (target !== null) onConfirm(target, typed);
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
