import i18n from "@/lib/i18n";
import type { ConsoleStatus, ConsoleType } from "../types/console";

/** A console's state in words: beside its dot, and read out with it. */
export const CONSOLE_STATUS_TEXT: Record<ConsoleStatus, string> = {
  get idle() {
    return i18n.t("console:consoleStatus.statusIdle");
  },
  get connecting() {
    return i18n.t("console:consoleStatus.statusConnecting");
  },
  get connected() {
    return i18n.t("console:consoleStatus.statusConnected");
  },
  get reconnecting() {
    return i18n.t("console:consoleStatus.statusReconnecting");
  },
  get disconnected() {
    return i18n.t("console:consoleStatus.statusDisconnected");
  },
  get error() {
    return i18n.t("console:consoleStatus.statusError");
  },
  get "guest-stopped"() {
    return i18n.t("console:consoleStatus.statusGuestStopped");
  },
};

/** What a console type is, for a list that names it apart from its label. */
export const CONSOLE_TYPE_TEXT: Record<ConsoleType, string> = {
  get node_shell() {
    return i18n.t("console:consoleStatus.typeNodeShell");
  },
  get vm_serial() {
    return i18n.t("console:consoleStatus.typeVmSerial");
  },
  get ct_attach() {
    return i18n.t("console:consoleStatus.typeCtAttach");
  },
  get vm_vnc() {
    return i18n.t("console:consoleStatus.typeVmVnc");
  },
  get ct_vnc() {
    return i18n.t("console:consoleStatus.typeCtVnc");
  },
};

/**
 * The dot beside a console's state. Green is a live session and red a failed
 * one; in between is amber while it is being dialled, and a hollow ring for
 * one that is simply not up (never opened, closed, or its guest is off).
 */
export const CONSOLE_STATUS_DOT: Record<ConsoleStatus, string> = {
  idle: "ring-[1.5px] ring-inset ring-muted-foreground/70",
  connecting: "animate-pulse bg-amber-500",
  connected: "bg-emerald-500",
  reconnecting: "animate-pulse bg-amber-500",
  disconnected: "ring-[1.5px] ring-inset ring-muted-foreground/70",
  error: "bg-red-500",
  "guest-stopped": "ring-[1.5px] ring-inset ring-muted-foreground/70",
};
