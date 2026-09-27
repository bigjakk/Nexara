import { useLayoutEffect, useRef } from "react";

/**
 * Where focus goes when a modal Radix dialog opened by state — not by a
 * Dialog.Trigger — closes: pass the returned handler as the dialog content's
 * `onCloseAutoFocus`.
 *
 * Radix returns focus on close only to a Trigger (react-dialog
 * DialogContentModal: its onCloseAutoFocus prevents the default and focuses
 * context.triggerRef, which is empty without one), so such a dialog dropped
 * focus to <body> on every close, and a keyboard user started again from the
 * top of the page. This sends it back to whatever had focus when the dialog
 * opened — the button that opened it — or, when that cannot take focus by
 * then (it is disabled, or gone, or nothing had focus), to `fallback()`.
 * ConfirmDeleteDialog does much the same inline for the confirmations,
 * without the fallback.
 *
 * Modal dialogs only: a non-modal one, like a popover, deliberately leaves
 * focus where an outside click put it, and this would pull it back.
 *
 * `open` is the dialog's open state; a dialog mounted only while it is open
 * passes true. The opener is recorded once per opening, in a layout effect:
 * before Radix's FocusScope moves focus into the dialog, and never again while
 * it is open, when focus is inside it.
 */
export function useOpenerFocus(
  open: boolean,
  fallback?: () => HTMLElement | null,
): (event: Event) => void {
  const opener = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (!open) return;
    const active = document.activeElement;
    // <body> is where focus sits when nothing has it — a click on a button
    // does not focus it in every browser — not an opener to return to.
    opener.current =
      active instanceof HTMLElement && active !== document.body ? active : null;
  }, [open]);
  return (event: Event) => {
    event.preventDefault();
    opener.current?.focus();
    // Whether it took focus, not why not: a disabled, removed or hidden
    // element all refuse it the same way. With no opener, activeElement —
    // never null — is not it either.
    if (document.activeElement !== opener.current) fallback?.()?.focus();
  };
}
