import { createContext } from "react";

/**
 * Whether the rows being rendered sit in a DataTableFrame, which gives them a
 * trailing filler cell: see DataTableFrame. Rows of any other table do not.
 */
export const DataTableFillerContext = createContext(false);
