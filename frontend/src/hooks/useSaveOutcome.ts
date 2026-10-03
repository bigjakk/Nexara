import { useCallback, useLayoutEffect, useRef } from "react";
import { toast } from "sonner";

import { sessionScope } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";

/** What a request that never got an answer reads as: see describeError. */
const CONNECTION_FAILED =
  "The request failed — check your connection and try again.";

/** What a save does when it settles; see useSaveOutcome. */
export interface SaveOutcome<T> {
  /**
   * What a failure that nobody is looking at is reported as, in a toast that
   * names the object: "Saving alice@pve" reads "Saving alice@pve failed: <the
   * server's own words>". The toast can land on another page, or over another
   * object's open dialog, so the name is not optional.
   */
  action: string;
  /** The save went through, and what sent it is still on screen. */
  onSuccess?: (result: T) => void;
  /** The save failed, and what sent it is still on screen. */
  onError: (error: unknown) => void;
  /**
   * The save went through, but what sent it is gone and the session goes on.
   * Runs INSTEAD of onSuccess, for what nothing else will do once it is gone:
   * show the one-time secret of a new API token (it cannot be shown, so the
   * operator is told), or put away the draft that a dialog left behind in a
   * component that hosts its successors. `now.open` says whether a dialog of
   * that component is open at this moment — which is then not the one that sent
   * the save, and whose text is not this save's to touch. Left out, a success
   * that nobody is looking at needs nothing: the hook that sent it has already
   * refreshed the lists it changed.
   */
  onLateSuccess?: (result: T, now: { open: boolean }) => void;
  /**
   * The words of the toast for a failure that nobody is looking at, for a
   * failure the caller can say better than "<action> failed: <the server's own
   * words>": one the server worded for an API caller, or a refusal that the
   * dialog would have answered with a prompt. Returns the whole text, or
   * undefined to leave the default. Not asked about a failure that what sent the
   * save is there to show.
   */
  lateFailure?: (error: unknown) => string | undefined;
}

/**
 * Settles the saves of a dialog — or the form of a page — that shows its own
 * failures, so that none is reported nowhere.
 *
 * The mutations behind such a dialog opt out of the app's global error toast
 * (`errorsHandledLocally`, lib/query-client.ts), because the open dialog
 * renders the failure itself. But TanStack runs the callbacks given to
 * `mutate(vars, { onError })` only while the component that called it is
 * mounted and still attached to that very call. A save that settles after the
 * dialog was dismissed (Escape, Cancel and a click outside all work while the
 * request is out), after the page was left, or after the same component hosted
 * another dialog in its place would therefore be reported nowhere — and the
 * list behind it would go on showing the old values, since nothing refreshes it
 * after a failure. Callers hand `mutateAsync(vars)` to the function this
 * returns instead: the promise settles whatever has happened to the component,
 * and this decides who hears of it.
 *
 *  - What sent the save is still on screen: `onSuccess` or `onError`, which
 *    show it there, as `mutate`'s own callbacks did.
 *  - It is not, and the session goes on: a failure is toasted, naming the
 *    object (`action`); a success does nothing, or `onLateSuccess` when the
 *    result cannot be shown any other way. Never `onSuccess`: it would close, or
 *    clear, whichever dialog has been opened in the first one's place.
 *  - The session ended or changed hands since the save went out (a sign-out, an
 *    expiry, someone else signing in): nothing at all, wherever the component
 *    is. The toast would carry the previous user's object names into the next
 *    user's Toaster, and a result would reach a screen it was not made for.
 *    lib/api-client.ts has sessionScope.
 *
 * What counts as "still on screen" is the component being mounted AND `shown`
 * being what it was when the save went out. A component that IS the dialog
 * (mounted only while it is open) leaves `shown` alone. One that hosts
 * successive dialogs — a section with a Create button, a page that opens one
 * form after another — passes what tells them apart: its open flag, or the id
 * of the object being edited (null for none). Closing is a change too, so a
 * save sent from a dialog that has since been closed is gone even if nothing
 * has opened in its place. `false` and `null` are what "closed" is.
 *
 * An open flag tells apart only dialogs that cannot take each other's place. A
 * modal that is not held while its request is out does not hold focus either (the
 * button that had it is disabled), Tab leaves it for the controls behind, which a
 * pointer cannot reach, and Enter on one of them can open another dialog in place
 * of the one that is up, with nothing closed between the two: the flag is true
 * before and after. Where that can happen — several buttons open the same dialog
 * — pass the identity of what is open, and key a dialog that is its own component
 * on it.
 *
 * Both are tracked from LAYOUT effects, so that they change in the commit that
 * removes the dialog, not in the passive flush after it. A navigation runs in a
 * transition (React Router sets its state in startTransition), React can run
 * that flush a task after such a commit, and a request settling in between would
 * find the dialog still live: it would show its failure in a form nobody can
 * see, or close the dialog opened in its place. (Radix's unmount schedules a
 * sync update, which today makes React run the passive effects inside the
 * commit; nothing here should lean on that. AccessEditLeavePage.test.tsx
 * reaches the gap with a stand-in for the dialog.)
 *
 * The session is taken when the function is called, which is as the operator
 * presses Save: call it in the same breath as `mutateAsync`.
 */
export function useSaveOutcome(
  shown: string | number | boolean | null = true,
): <T>(save: Promise<T>, outcome: SaveOutcome<T>) => void {
  const mounted = useRef(false);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  // Which dialog is on screen. It moves whenever `shown` does — on the render
  // that opens a dialog, and on the one that closes it — and once on mount,
  // which no save can have been sent before. `latest` is what `shown` is now,
  // for a late success to ask whether a dialog is open.
  const generation = useRef(0);
  const latest = useRef(shown);
  useLayoutEffect(() => {
    generation.current += 1;
    latest.current = shown;
  }, [shown]);

  return useCallback(function settle<T>(
    save: Promise<T>,
    outcome: SaveOutcome<T>,
  ): void {
    const ended = sessionScope();
    const sentFrom = generation.current;
    const onScreen = () => mounted.current && generation.current === sentFrom;

    save.then(
      (result) => {
        if (ended()) return;
        if (onScreen()) {
          outcome.onSuccess?.(result);
          return;
        }
        outcome.onLateSuccess?.(result, {
          open:
            mounted.current &&
            latest.current !== false &&
            latest.current !== null,
        });
      },
      (error: unknown) => {
        if (ended()) return;
        if (onScreen()) {
          outcome.onError(error);
          return;
        }
        // The server's own words, unless the caller has better: with the dialog
        // gone, "reload and try again" is just what to do.
        toast.error(
          outcome.lateFailure?.(error) ??
            `${outcome.action} failed: ${describeError(error) || CONNECTION_FAILED}`,
        );
      },
    );
  }, []);
}
