import { create } from "zustand";
import type { ReactNode } from "react";
import type {
  HistoryKind,
  HistoryTarget,
} from "@/features/history/types/history";

/** One object's History, as a list row's HistoryButton asks for it. */
export interface HistoryDrawerRequest {
  kind: HistoryKind;
  target: HistoryTarget;
  /** What the History is of, e.g. the object's name. */
  title: string;
  /** Live rows above the first page, as HistoryTimeline's. */
  rowsAbove?: ((colSpan: number) => ReactNode) | undefined;
}

interface HistoryDrawerState {
  /** The last one asked for, kept while the drawer slides shut. */
  request: HistoryDrawerRequest | null;
  isOpen: boolean;
}

interface HistoryDrawerActions {
  open: (request: HistoryDrawerRequest) => void;
  close: () => void;
  /** The signed-in session ended: the History named its cluster and object. */
  resetSession: () => void;
}

/**
 * The app's one History drawer, drawn by HistoryDrawerHost in AppShell rather
 * than inside the row whose button opened it: a portal's events still bubble
 * up the React tree it was rendered in, so a click, a press or a key in the
 * drawer, or on its dismissing overlay, would reach the row and select or open
 * it — and stopping them there would stop the document listener Radix
 * dismisses the drawer on.
 */
export const useHistoryDrawerStore = create<
  HistoryDrawerState & HistoryDrawerActions
>()((set) => ({
  request: null,
  isOpen: false,
  open: (request) => {
    set({ request, isOpen: true });
  },
  close: () => {
    set({ isOpen: false });
  },
  resetSession: () => {
    set({ request: null, isOpen: false });
  },
}));
