/**
 * Tiny Zustand store for the global search modal's open/close state.
 *
 * Lives at the (app) layout level so any header button — Tabs header,
 * StackHeader, etc. — can open the modal without prop drilling. The
 * modal itself is mounted once at `app/(app)/_layout.tsx` and reads
 * `isOpen` here.
 */

import { create } from "zustand";

interface SearchState {
  isOpen: boolean;
  open: () => void;
  close: () => void;
}

export const useSearchStore = create<SearchState>((set) => ({
  isOpen: false,
  open: () => set({ isOpen: true }),
  close: () => set({ isOpen: false }),
}));
