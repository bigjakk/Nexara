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
import { CopyableName } from "@/components/CopyableName";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface ConfirmNameFieldProps {
  id: string;
  /** The name to type, exactly. */
  name: string;
  value: string;
  onChange: (value: string) => void;
  /**
   * Help with the typing: the name copies itself when pressed, and shows in
   * the empty box. For a guest's own dialogs, which have always offered it.
   * Left off, the name has to be read and typed, which is the point of asking
   * for it before something as drastic as cutting a UPS's power.
   */
  assist?: boolean;
}

/**
 * The field a drastic action is held behind: the name to type, over the box
 * to type it in. Shared by every dialog that asks for a name, so they all ask
 * the same way.
 */
export function ConfirmNameField({
  id,
  name,
  value,
  onChange,
  assist = false,
}: ConfirmNameFieldProps) {
  return (
    <div className="space-y-2">
      <Label
        htmlFor={id}
        className="block font-normal leading-snug text-muted-foreground"
      >
        Type{" "}
        {assist ? (
          <CopyableName name={name} />
        ) : (
          <span className="font-mono font-semibold text-foreground">
            {name}
          </span>
        )}{" "}
        to confirm
      </Label>
      <Input
        id={id}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        {...(assist ? { placeholder: name } : {})}
        autoComplete="off"
        spellCheck={false}
      />
    </div>
  );
}

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
              <AlertDialogDescription>
                {description(current)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            {details?.(current)}
            <ConfirmNameField
              id={id}
              name={expected}
              value={typed}
              onChange={setTyped}
            />
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              {/* The one filled red control: everything else here is quiet. */}
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
