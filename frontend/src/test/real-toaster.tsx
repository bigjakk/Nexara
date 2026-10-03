import { act, render, screen } from "@testing-library/react";
import { Toaster, toast } from "sonner";

/**
 * The real sonner, for the tests of what a session leaves on the toast screen
 * (stores/session-reset.toasts.test.tsx, stores/auth-store.toasts.test.tsx).
 * sonner keeps its toasts in module state, outside any Toaster, and hands every
 * one that was never dismissed to each Toaster that mounts, so those tests mount
 * a Toaster, unmount it and mount another, as AppShell's is when a session ends
 * or changes hands. A test that mocks sonner cannot see any of that.
 */

/** What a toast that the session being tested raises itself reads as. */
export const PROBE = "A toast the next session raises itself";

/** Lets sonner's timers run: it draws a toast a timer after it is raised. */
export async function tick(ms = 60) {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, ms);
    });
  });
}

/**
 * Waits for a dismissal, if one was made, to have cleared its toast from the DOM.
 * It gets there in two steps: sonner tells its Toaster, which marks the toast
 * removed when React applies that update — at the close of the act scope the wait
 * is in — and the Toaster takes it out of the DOM about 200 ms after that. So one
 * wait inside act shows nothing of a dismissal; two do. The first closes the
 * scope that applies the update, and the second, set after the removal's timer,
 * is longer than it. A test that says a toast is still on screen after this has
 * not been dismissed, and one that says it is gone has been; both also look at
 * toast.getToasts(), which a dismissal changes at once.
 */
export async function afterADismissal() {
  await tick(150);
  await tick(400);
}

/**
 * Mounts a Toaster, as AppShell does for each session, and waits until it has
 * drawn everything it was handed — and is therefore done with whatever it was
 * going to replay. It raises a toast of its own and waits for that to be drawn:
 * sonner draws what it replays from timers set when the Toaster mounts, and this
 * toast's timer is set after them, so by the time it is on screen so is anything
 * that was going to be. An absence looked for after this is an absence, not a
 * Toaster that has yet to get to it.
 */
export async function mountedToasterAndDrawn() {
  const view = render(<Toaster />);
  toast.message(PROBE);
  await screen.findByText(PROBE);
  return view;
}

/** A Toaster that is mounted, with `text` raised on it and drawn. */
export async function toasterShowing(text: string) {
  const view = render(<Toaster />);
  toast.error(text);
  await screen.findByText(text);
  return view;
}
