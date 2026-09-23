import type { ComponentProps } from "react";
import { RouterProvider } from "react-router-dom";

import { PendingPBSKeyDialog } from "@/features/storage/components/PendingPBSKey";

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
    <>
      <RouterProvider router={router} />
      <PendingPBSKeyDialog />
    </>
  );
}
