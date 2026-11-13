/** A native select sized to sit beside the Hardware and Resources tabs' inputs. */
export const fieldSelectClass =
  "flex h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2.5 py-1 text-sm transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50";

/** An Input at the same height. */
export const fieldInputClass = "h-8";

/**
 * On a tab's root, with a SaveBar at its foot: every control in it keeps this
 * much clear below itself when the browser brings it into view, so one that
 * takes focus near the foot of the pane scrolls out from under the bar.
 */
export const clearSaveBarClass =
  "[&_:is(a,button,input,select,textarea,summary,[tabindex])]:scroll-mb-20";
