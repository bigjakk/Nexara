import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import type { QueryClient } from "@tanstack/react-query";

import { deferred } from "@/test/fake-server";
import { toastsRaised } from "@/test/late-toast-sessions";
import {
  DENIED,
  SESSION_ENDINGS,
  denied,
  flushInAct,
  waitForSuccess,
} from "@/test/save-outcome-kit";

/**
 * What the consumers of hooks/useSaveOutcome.ts share when they test their own
 * wiring: the hook's outcome matrix lives in hooks/useSaveOutcome.test.tsx, so a
 * consumer proves only what it passes the hook (its `shown`, its handlers, its
 * action) with the suites below, over the dialogs it has. A test file using
 * these mocks `sonner` (whose `toast.error` is where a late failure ends up) and
 * `@/lib/api-client` the way save-outcome-kit.tsx says.
 */

export type { UserEvent };

/** A session ending, as save-outcome-kit's SESSION_ENDINGS has them. */
export type SessionEnding = (typeof SESSION_ENDINGS)[number];

/** The row of a [name, ...] table with this name, for a test that wants one of them and not all. */
export function row<T extends readonly [string, ...unknown[]]>(
  table: readonly T[],
  name: string,
): T {
  const found = table.find(([known]) => known === name);
  if (!found) throw new Error(`no row called ${name}`);
  return found;
}

export const SIGN_OUT = row(SESSION_ENDINGS, "a sign-out");
export const TAKEN_OVER = row(SESSION_ENDINGS, "someone else signing in");

/** The mock that carries a save: what a test holds back, and reads what was sent from. */
export interface SaveRequest {
  mockReturnValueOnce(answer: Promise<unknown>): unknown;
  mock: { calls: readonly (readonly unknown[])[] };
}

/** The next request is held until the test settles the promise this returns. */
export function heldOnce(request: SaveRequest) {
  const held = deferred<unknown>();
  request.mockReturnValueOnce(held.promise);
  return held;
}

/** The toast a failed save leaves once what sent it is gone: "<what> failed: <the server's words>". */
export function failedToast(action: string, words = DENIED): string {
  return `${action} failed: ${words}`;
}

/** No toast of any kind has been raised. */
export function expectNoToast(): void {
  expect(toastsRaised()).toEqual([]);
}

/** Waits for exactly this error toast, then for a second one that could be late with it. */
export async function expectOneToast(message: string): Promise<void> {
  const only = [`error: ${message}`];
  await waitFor(() => {
    expect(toastsRaised()).toEqual(only);
  });
  await flushInAct();
  expect(toastsRaised()).toEqual(only);
}

/** Lets what a settled request queued run, twice over, and says nothing was toasted. */
export async function expectSilence(): Promise<void> {
  await flushInAct();
  await flushInAct();
  expectNoToast();
}

/** One way of getting rid of a dialog. */
export type Dismissal = (user: UserEvent, dialog: HTMLElement) => Promise<void>;

export const ESCAPE: Dismissal = async (user) => {
  await user.keyboard("{Escape}");
};

export const CANCEL_BUTTON: Dismissal = async (user, dialog) => {
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
};

/** Gets rid of `dialog` (with Escape unless told otherwise) and waits until it is gone. */
export async function dismiss(
  user: UserEvent,
  dialog: HTMLElement,
  how: Dismissal = ESCAPE,
): Promise<void> {
  await how(user, dialog);
  await waitFor(() => {
    expect(dialog).not.toBeInTheDocument();
  });
}

/**
 * What a section keeps of a dialog's form between dialogs: read by `values`, as
 * the first dialog left it in `left`, and added to by `append`.
 */
export interface Draft {
  values: (dialog: HTMLElement) => string[];
  left: string[];
  append: (user: UserEvent, dialog: HTMLElement) => Promise<void>;
}

/** A dialog that saves through useSaveOutcome, and how to drive it. */
export interface DialogSave {
  name: string;
  /** Renders its section on the app's own client (renderOnAppClient). */
  render: () => { qc: QueryClient };
  request: SaveRequest;
  /** The request, as apiClient is called with it. */
  sent: unknown[];
  /** The dialog's save button while it is idle. */
  button: string;
  /** Opens the dialog, fills it in, presses the button, and waits for the request to be out. */
  send: (user: UserEvent) => Promise<HTMLElement>;
  /** What a toast calls the save once its dialog is gone. */
  action: string;
  /** Opens another dialog of the same component, the first having been dismissed. */
  reopen: (user: UserEvent) => Promise<HTMLElement>;
  draft?: Draft;
  /** If given, a failure after this ending is tested to be silent. */
  silentAfter?: SessionEnding;
  /** How the dialog is dismissed before another is opened (Escape unless given). */
  dismissal?: Dismissal;
}

/** Sends `s`'s save with its request held, dismisses its dialog, and opens another. */
async function sentThenReplaced(
  s: DialogSave,
  user: UserEvent,
  how: Dismissal | undefined = s.dismissal,
): Promise<{
  held: ReturnType<typeof heldOnce>;
  qc: QueryClient;
  second: HTMLElement;
}> {
  const held = heldOnce(s.request);
  const { qc } = s.render();
  await dismiss(user, await s.send(user), how);
  return { held, qc, second: await s.reopen(user) };
}

/**
 * The wiring of each dialog that saves through useSaveOutcome: what it shows of
 * a failure while it is open, and what it does about a save whose dialog was
 * dismissed and another opened in its place.
 */
export function describeDialogSaves(saves: DialogSave[]): void {
  describe.each(saves)("a $name that settles", (s) => {
    it("shows a failure in its dialog, with no toast, and closes it when the second try succeeds", async () => {
      const user = userEvent.setup();
      const held = heldOnce(s.request);
      s.render();

      const dialog = await s.send(user);
      held.reject(denied());

      expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
      await flushInAct();
      expectNoToast();
      await user.click(
        await within(dialog).findByRole("button", { name: s.button }),
      );
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      expect(s.request.mock.calls).toEqual([s.sent, s.sent]);
      expectNoToast();
    });

    it("toasts a failure once, naming it, when its dialog was dismissed and another opened, and shows nothing of it in that one", async () => {
      const user = userEvent.setup();
      const { held, second } = await sentThenReplaced(s, user);
      held.reject(denied());

      await expectOneToast(failedToast(s.action));
      expect(second).toBeInTheDocument();
      expect(second).toHaveAttribute("data-state", "open");
      expect(screen.queryByText(DENIED)).toBeNull();
      // Nothing was put away: the form is as the first dialog left it.
      if (s.draft) expect(s.draft.values(second)).toEqual(s.draft.left);
    });

    it("leaves the dialog opened in its place open and unchanged when it succeeds", async () => {
      const user = userEvent.setup();
      // Escape here whatever the row's dismissal, so a row that has two wires tests both.
      const { held, qc, second } = await sentThenReplaced(s, user, ESCAPE);
      await s.draft?.append(user, second);
      held.resolve({});
      await waitForSuccess(qc);

      expect(second).toBeInTheDocument();
      expect(second).toHaveAttribute("data-state", "open");
      expectNoToast();
      if (s.draft) {
        const [first = "", ...rest] = s.draft.left;
        expect(s.draft.values(second)).toEqual([`${first}x`, ...rest]);
      }
    });

    if (s.silentAfter) {
      const [after, end] = s.silentAfter;
      it(`says nothing of a failure that comes after ${after}`, async () => {
        const user = userEvent.setup();
        const { held } = await sentThenReplaced(s, user);
        end();
        held.reject(denied());

        await expectSilence();
      });
    }
  });
}

/**
 * A dialog taken over by another one from the keyboard. Neither is held while
 * its request is out, and the button that sent it is disabled by the request, so
 * focus is lost and Tab walks out of the modal to the controls behind it, which
 * a pointer cannot reach. Enter on one of them opens another dialog in place of
 * the first, with nothing closed between the two: a dialog told apart only by
 * whether one is open would hand the second the first's outcome.
 */
export interface Replacement {
  name: string;
  render: () => { qc: QueryClient };
  request: SaveRequest;
  /** What a toast calls the first save. */
  action: string;
  /** Sends the first save with its request held, and opens the other dialog over it. */
  replace: (user: UserEvent) => Promise<HTMLElement>;
  /** Says the dialog that replaced it is the other's own. */
  own?: (second: HTMLElement) => void;
}

export function describeReplacements(rows: Replacement[]): void {
  describe.each(rows)(
    "a $name save whose dialog is replaced from the keyboard",
    (r) => {
      it("toasts the failure once, naming it, and shows nothing of it in the dialog that replaced it", async () => {
        const user = userEvent.setup();
        const held = heldOnce(r.request);
        r.render();

        const second = await r.replace(user);
        held.reject(denied());

        await expectOneToast(failedToast(r.action));
        expect(second).toBeInTheDocument();
        expect(second).toHaveAttribute("data-state", "open");
        expect(screen.queryByText(DENIED)).toBeNull();
        r.own?.(second);
      });

      it("leaves the dialog that replaced it open when it succeeds, and toasts nothing", async () => {
        const user = userEvent.setup();
        const held = heldOnce(r.request);
        const { qc } = r.render();

        const second = await r.replace(user);
        held.resolve({});
        await waitForSuccess(qc);

        expect(second).toBeInTheDocument();
        expect(second).toHaveAttribute("data-state", "open");
        r.own?.(second);
        expectNoToast();
      });
    },
  );
}
