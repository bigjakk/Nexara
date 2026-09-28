import type { MouseEvent } from "react";

/**
 * Moves focus from the button just pressed to the dialog around it, as the
 * request it sends goes out.
 *
 * The request disables the dialog's buttons, and a modal's focus trap (Radix's
 * FocusScope) pulls focus back by refocusing the last element that had it
 * inside, which does nothing once that is a disabled button. Focus that gets
 * out then stays out. A click on the backdrop drops it to the page, and Tab or
 * Shift+Tab go on from there into what the modal hides from assistive
 * technology but does not make inert. From an alert dialog, which has nothing
 * else to tab to, Tab walks out at once: Enter on a row's Edit opens the edit
 * dialog, and the refusal of the delete then opens its override over that. From
 * an override, Shift+Tab can reach the form beneath it, whose own Cancel and
 * Close close it and unmount the held override with its request in flight.
 *
 * Focused, the dialog is the last element to have had focus, and one the trap
 * can always return to. On an alert dialog, whose buttons are then all disabled,
 * the trap also blocks Tab and Shift+Tab outright while focus is on the dialog
 * itself, having no tabbable element to move between.
 */
export function holdFocusInDialog(e: MouseEvent) {
  e.currentTarget
    .closest<HTMLElement>('[role="alertdialog"], [role="dialog"]')
    ?.focus();
}
