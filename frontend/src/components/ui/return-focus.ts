import {
  createContext,
  useCallback,
  useContext,
  useLayoutEffect,
  useMemo,
  useRef,
  type ForwardedRef,
  type RefObject,
} from "react";

/**
 * Where focus goes when a modal dialog closes: the default every Dialog,
 * AlertDialog and Sheet in ui/ gets.
 *
 * Radix returns focus on close only to a Trigger (react-dialog
 * DialogContentModal: its onCloseAutoFocus prevents the default and focuses
 * context.triggerRef, which only a Dialog.Trigger fills). Most dialogs here
 * are opened by state, so Save, Cancel, Escape and an outside click all left
 * focus on <body>, and a keyboard user started again from the top of the page.
 *
 * So the root records the opener each time the dialog opens, and the content
 * sends focus back to it on close — or, when it cannot take focus by then
 * (disabled, removed, or nothing had focus), to a fallback: the content's
 * fallbackFocus, else the regions around it (FallbackFocusContext) — the
 * dialog it was opened from, the app's main content. A row's Delete button is
 * usually still there when its dialog closes and is removed a moment later,
 * when the list is read again, so after the restore a watch sends focus to
 * the fallback if what it went to is taken away before the user moves focus
 * on.
 *
 * Modal dialogs only: a non-modal one, like a popover, deliberately leaves
 * focus where an outside click put it, and keeps Radix's own handling.
 */

/** An element to fall back to. A container needs tabIndex={-1}. */
export type FallbackFocus = () => HTMLElement | null;

interface ReturnFocus {
  /** What the dialog goes back to — see resolveOpener. */
  opener: RefObject<HTMLElement | null>;
  modal: boolean;
}

/** Provided by the Dialog, AlertDialog and Sheet roots in ui/. */
export const ReturnFocusContext = createContext<ReturnFocus | null>(null);

/**
 * Where a dialog inside this subtree sends focus when its opener cannot take
 * it back, after its own fallbackFocus: the regions around it, innermost
 * first. Focus goes to the first that takes it, so one that has gone — the
 * page a dialog navigated away from — hands on to the next. Provide it with
 * useFallbackFocus.
 */
export const FallbackFocusContext = createContext<readonly FallbackFocus[]>([]);

/**
 * A FallbackFocusContext value: `find` ahead of the regions around it. Pass a
 * stable `find`.
 */
export function useFallbackFocus(
  find: FallbackFocus,
): readonly FallbackFocus[] {
  const around = useContext(FallbackFocusContext);
  return useMemo(() => [find, ...around], [find, around]);
}

// The element that had focus inside one of these is about to go with it.
const MENU = '[role="menu"]';
const CLOSING_DIALOG =
  '[role="dialog"][data-state="closed"], [role="alertdialog"][data-state="closed"]';
const DIALOG = '[role="dialog"], [role="alertdialog"]';
const OPEN_DIALOG =
  '[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"]';

/** Each dialog content's opener, for a dialog opened as that one closes. */
const dialogOpeners = new WeakMap<Element, () => HTMLElement | null>();

/** What the last context menu was opened on — see rememberContextMenuOpener. */
let contextMenuOpener: HTMLElement | null = null;

// What a keyboard or a click can put focus on.
const FOCUSABLE = [
  "a[href]",
  "button",
  "input",
  "select",
  "textarea",
  "[tabindex]",
]
  .map((selector) => `${selector}:not(:disabled)`)
  .join(", ");

/**
 * The element a dialog opened now should go back to: what has focus, unless
 * that will be gone by the time the dialog closes.
 *
 * - Nothing, <body>: a click does not focus the button it lands on in every
 *   browser. No opener, so the fallback.
 * - An item of a menu: Radix runs a menu item's handler inside a flushSync,
 *   before the menu starts to close, so the dialog opens with the item still
 *   focused — and the item goes with the menu. A DropdownMenu's content (and
 *   a submenu's) is labelled by its trigger; a ContextMenu's is not, so its
 *   trigger remembers what it was opened on.
 * - Anything in a dialog that is closing — a dialog opened as another closes,
 *   like a secret shown after the form that created it: that one's opener.
 */
export function resolveOpener(start: Element | null): HTMLElement | null {
  let el = start;
  // Each hop leaves a menu or a closing dialog for what opened it; the bound
  // only stops a cycle.
  for (let hop = 0; hop < 4; hop++) {
    if (!(el instanceof HTMLElement) || el === document.body) return null;
    const menu = el.closest(MENU);
    if (menu !== null) {
      const trigger = menu.getAttribute("aria-labelledby")?.split(/\s+/)[0];
      el = trigger ? document.getElementById(trigger) : contextMenuOpener;
      continue;
    }
    const dialog = el.closest(CLOSING_DIALOG);
    const origin = dialog === null ? undefined : dialogOpeners.get(dialog);
    if (origin === undefined) return el;
    el = origin();
  }
  return null;
}

/**
 * Called by the ContextMenu trigger on contextmenu (a right click, Shift+F10,
 * the Menu key) and on a touch or pen press: the element to go back to after
 * a dialog opened from that menu — the focusable element the event landed on,
 * or else the trigger's first. Not document.activeElement: a right click does
 * not focus what it lands on in every browser. Within the trigger: the page's
 * main content is focusable too, and around every row.
 */
export function rememberContextMenuOpener(event: {
  target: EventTarget;
  currentTarget: Element;
}): void {
  const trigger = event.currentTarget;
  const hit =
    event.target instanceof Element
      ? event.target.closest<HTMLElement>(FOCUSABLE)
      : null;
  if (hit !== null && trigger.contains(hit)) {
    contextMenuOpener = hit;
  } else if (trigger instanceof HTMLElement && trigger.matches(FOCUSABLE)) {
    contextMenuOpener = trigger;
  } else {
    contextMenuOpener = trigger.querySelector<HTMLElement>(FOCUSABLE);
  }
}

/**
 * The root's half: records the opener each time `open` turns true. `open`
 * undefined — an uncontrolled dialog, which only its Trigger can open —
 * records nothing, and Radix goes back to the Trigger itself.
 */
export function useRecordOpener(
  open: boolean | undefined,
  modal: boolean,
): ReturnFocus {
  const opener = useRef<HTMLElement | null>(null);
  // A layout effect keyed on `open`: it runs before the content mounts —
  // react-portal renders nothing until its own layout effect sets `mounted`,
  // so the content, and an autoFocus input in it, come a commit later — and
  // not again while the dialog is open, when focus is inside it. Not
  // onOpenAutoFocus: FocusScope does not dispatch it when focus is already
  // inside the content, which is where an autoFocus input has put it.
  useLayoutEffect(() => {
    if (open !== true) return;
    opener.current = resolveOpener(document.activeElement);
  }, [open]);
  return useMemo(() => ({ opener, modal }), [modal]);
}

/**
 * The content's half: a ref for the content, the onCloseAutoFocus to hand
 * Radix, and the fallbacks for a dialog opened from inside this one — this
 * content, which FocusScope makes focusable, so that focus stays within the
 * dialog still open rather than going behind it. The caller's
 * onCloseAutoFocus runs first; one that calls preventDefault() has taken over.
 */
export function useReturnFocus<T extends HTMLElement>(
  forwardedRef: ForwardedRef<T>,
  onCloseAutoFocus: ((event: Event) => void) | undefined,
  fallbackFocus: FallbackFocus | undefined,
) {
  const returnFocus = useContext(ReturnFocusContext);
  const around = useContext(FallbackFocusContext);
  const fallbacks =
    fallbackFocus === undefined ? around : [fallbackFocus, ...around];

  const content = useRef<T | null>(null);
  const own = useCallback(() => content.current, []);
  const childFallbacks = useFallbackFocus(own);

  const ref = useCallback(
    (node: T | null) => {
      content.current = node;
      if (node !== null) {
        // A dialog opening ends the last one's watch (see watch).
        disarmWatch();
        if (returnFocus !== null) {
          dialogOpeners.set(node, () => returnFocus.opener.current);
        }
      }
      const cleanup = setRef(forwardedRef, node);
      if (cleanup === undefined) return undefined;
      // With a cleanup to run, React does not call this ref again with null.
      return () => {
        content.current = null;
        cleanup();
      };
    },
    [forwardedRef, returnFocus],
  );

  const handleCloseAutoFocus = (event: Event) => {
    onCloseAutoFocus?.(event);
    if (event.defaultPrevented || returnFocus === null || !returnFocus.modal) {
      return;
    }
    // Already somewhere on purpose: in a dialog opened as this one closed,
    // say, or wherever the app put it.
    if (!focusLost()) return;
    const opener = returnFocus.opener.current;
    if (opener !== null) {
      opener.focus();
      // Whether it took focus, not why not: disabled, removed and hidden all
      // refuse it the same way. If it did not, Radix goes on to its Trigger —
      // for a dialog without one, nowhere.
      if (document.activeElement === opener) event.preventDefault();
    }
    // After Radix has had its turn.
    queueMicrotask(() => {
      settle(fallbacks);
    });
  };

  return { ref, onCloseAutoFocus: handleCloseAutoFocus, childFallbacks };
}

// Sets a forwarded ref, and hands back the cleanup a React 19 callback ref
// may return, so that composing it does not drop it.
function setRef<T>(ref: ForwardedRef<T>, node: T | null) {
  if (typeof ref === "function") {
    const cleanup = (ref as (node: T | null) => (() => void) | undefined)(node);
    return typeof cleanup === "function" ? cleanup : undefined;
  }
  if (ref !== null) ref.current = node;
  return undefined;
}

function focusLost(): boolean {
  const active = document.activeElement;
  return active === null || active === document.body;
}

/**
 * Focuses the first fallback that takes focus — but none that a modal still
 * open hides: Radix's hideOthers marks everything outside it aria-hidden. A
 * dialog opened from inside another that stays open, but rendered beside it
 * (VMContextDialogs, opened from the mobile nav Sheet), would otherwise send
 * focus behind it. When none can, that open dialog: the topmost.
 */
function focusFallback(
  fallbacks: readonly FallbackFocus[],
  options?: FocusOptions,
): void {
  for (const fallback of fallbacks) {
    const el = fallback();
    if (el === null || el.closest('[aria-hidden="true"]') !== null) continue;
    el.focus(options);
    if (document.activeElement === el) return;
  }
  [...document.querySelectorAll<HTMLElement>(OPEN_DIALOG)]
    .at(-1)
    ?.focus(options);
}

function settle(fallbacks: readonly FallbackFocus[]): void {
  if (fallbacks.length === 0) return;
  if (focusLost()) {
    focusFallback(fallbacks);
    return;
  }
  const active = document.activeElement;
  if (active instanceof HTMLElement) watch(active, fallbacks);
}

// Long enough for a delete to be read back — some go through a Proxmox task
// first — and short enough that the watch does not outlive the moment.
const WATCH_MS = 30_000;

let disarmWatch: () => void = () => undefined;

/**
 * Keeps focus from falling to <body> when the element it went back to is
 * taken away before the user moves focus on — the row a confirmed delete
 * removes once the list is read again, a button a save disables. Ends at the
 * first focusin or pointerdown anywhere (the user, or the app, has moved
 * focus on), once it has fired, after WATCH_MS, and when the next dialog
 * opens: in a window that does not have focus, focus() fires no focusin, and
 * a watch left armed would take the focus that next dialog's content drops
 * to <body> as it unmounts. Stands down, too, when the element goes with a
 * dialog that closed around it — the one a confirmation was opened from,
 * closed by Escape: that dialog's own close, still to come, puts focus back,
 * and the element (detached with it) still finds it with closest().
 * Without scrolling: by the time it fires, the user may have scrolled away
 * from the fallback.
 */
function watch(element: HTMLElement, fallbacks: readonly FallbackFocus[]) {
  disarmWatch();
  const expires = Date.now() + WATCH_MS;
  const observer = new MutationObserver(() => {
    if (Date.now() > expires) {
      disarm();
      return;
    }
    const disabled =
      document.activeElement === element && element.matches(":disabled");
    if (!focusLost() && !disabled) return;
    disarm();
    if (element.closest(DIALOG)?.isConnected === false) return;
    focusFallback(fallbacks, { preventScroll: true });
  });
  function disarm() {
    observer.disconnect();
    document.removeEventListener("focusin", disarm, true);
    document.removeEventListener("pointerdown", disarm, true);
    if (disarmWatch === disarm) disarmWatch = () => undefined;
  }
  observer.observe(document.body, {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: ["disabled"],
  });
  document.addEventListener("focusin", disarm, true);
  document.addEventListener("pointerdown", disarm, true);
  disarmWatch = disarm;
}
