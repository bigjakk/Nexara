import type { ComponentProps } from "react";
import { RouterProvider } from "react-router-dom";

import {
  FallbackFocusContext,
  type FallbackFocus,
} from "@/components/ui/return-focus";
import { PendingPBSKeyDialog } from "@/features/storage/components/PendingPBSKey";
import { MAIN_CONTENT_ID } from "@/lib/constants";

// Where a dialog sends focus when the element that opened it has gone — the
// page it navigated from, the row it deleted — and nothing nearer takes it:
// the page's main content, as a skip link would. None outside the app shell
// (the login page), where focus is left alone.
const mainContent: readonly FallbackFocus[] = [
  () => document.getElementById(MAIN_CONTENT_ID),
];

/**
 * The router, and beside it — outside every route — the dialogs that must
 * survive any route change. A generated PBS encryption key is one: it is shown
 * exactly once, and a page that went away with it would take the operator's
 * only chance to save it.
 */
export function AppRoot({
  router,
}: {
  router: ComponentProps<typeof RouterProvider>["router"];
}) {
  return (
    <FallbackFocusContext value={mainContent}>
      <RouterProvider router={router} />
      <PendingPBSKeyDialog />
    </FallbackFocusContext>
  );
}
