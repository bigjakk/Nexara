import { toast } from "sonner";
import { useAuthStore } from "@/stores/auth-store";
import { useConsoleStore } from "@/stores/console-store";
import { usePreferencesStore } from "@/stores/preferences-store";
import type { ConsoleTarget } from "../types/console";
import { leaveLaunch } from "./console-launch";
import {
  consoleLabel,
  consoleSessionHref,
  wantsNewTab,
  type ConsoleClickKeys,
} from "./console-session";

/** A console to open: what it connects to, and the guest's name to label it. */
export type ConsoleRequest = Omit<ConsoleTarget, "label"> & {
  guestName?: string;
};

/**
 * Opens a console where the user asked for it: the floating window, or a
 * browser tab of its own (the preference, or Ctrl/⌘ held on the click).
 * `keys` is the click's, required so that every launcher passes it on.
 */
export function openConsole(request: ConsoleRequest, keys: ConsoleClickKeys) {
  const { guestName, ...connect } = request;
  const target: ConsoleTarget = {
    ...connect,
    label: consoleLabel(connect, guestName),
  };
  const mode = usePreferencesStore.getState().preferences.consoleOpenMode;
  if (wantsNewTab(mode, keys)) {
    openConsoleInNewTab(target);
    return;
  }
  const { addTab, showConsole } = useConsoleStore.getState();
  addTab(target);
  showConsole();
}

/**
 * Opens `target` in a new browser tab, with a launch (console-launch.ts) that
 * lets it dial without asking. Called synchronously from the click, so the
 * browser allows it. noopener gives the tab a browsing context group of its
 * own, so it does not share this page's event loop: a busy dashboard here
 * cannot stall a console there. It also leaves no handle back to it, so a tab
 * a blocker stopped cannot be told from one that opened: false means only that
 * it was refused here.
 *
 * Refused, for every launcher, while the console's node is not known (the
 * nodes still loading where it was asked for): the tab would get a link that
 * names none.
 */
export function openConsoleInNewTab(target: ConsoleTarget): boolean {
  if (target.node === "") {
    toast.error("This console cannot open yet", {
      description: "Its node is still loading. Try again in a moment.",
    });
    return false;
  }
  const user = useAuthStore.getState().user?.id;
  const launch = user === undefined ? null : leaveLaunch(target, user);
  const href = consoleSessionHref(target);
  window.open(
    launch === null ? href : `${href}#launch=${launch}`,
    "_blank",
    "noopener",
  );
  return true;
}

/**
 * Moves a floating console tab into a browser tab of its own. The floating
 * copy closes at once, long before the new tab has signed in and dialled — a
 * serial console takes one client at a time.
 */
export function popOutConsoleTab(tabId: string) {
  const { tabs, removeTab } = useConsoleStore.getState();
  const tab = tabs.find((t) => t.id === tabId);
  if (tab && openConsoleInNewTab(tab)) removeTab(tabId);
}
