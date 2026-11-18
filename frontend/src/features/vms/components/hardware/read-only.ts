import { createContext } from "react";

/**
 * Whether a guest's Hardware or Resources tab is drawn for a role that may not
 * change the guest's config (its config route answers 403). The row kit in
 * HardwareRows then draws itself read-only: fields and checkboxes disabled, a
 * device's fields behind Show, and no row actions, resize editors or save bar.
 */
export const RowsReadOnlyContext = createContext(false);
