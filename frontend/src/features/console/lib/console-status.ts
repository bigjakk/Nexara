import type { ConsoleStatus, ConsoleType } from "../types/console";

/** A console's state in words: beside its dot, and read out with it. */
export const CONSOLE_STATUS_TEXT: Record<ConsoleStatus, string> = {
  idle: "Not connected",
  connecting: "Connecting",
  connected: "Connected",
  reconnecting: "Reconnecting",
  disconnected: "Disconnected",
  error: "Connection failed",
  "guest-stopped": "Powered off",
};

/** What a console type is, for a list that names it apart from its label. */
export const CONSOLE_TYPE_TEXT: Record<ConsoleType, string> = {
  node_shell: "Node shell",
  vm_serial: "Serial terminal",
  ct_attach: "Container terminal",
  vm_vnc: "VNC display",
  ct_vnc: "Container console",
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
