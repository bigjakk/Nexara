/**
 * Every measured series draws in the data colour (--data): the stock blue, or
 * the accent the user picked unless that accent is itself a state colour.
 * Amber, red and green are states: a chart wearing one would read as a
 * warning or as "running".
 */
export const DATA_COLOR = "hsl(var(--data))";

/** Something under way, drawn outside a class name: a migration's edge. */
export const INFO_COLOR = "hsl(var(--info))";

/**
 * The first hue of a fixed several-series palette: the theme's blue whatever
 * accent is picked, because the palette's other hues were chosen to be told
 * apart from it and an accent would land on one of them.
 */
export const PALETTE_BLUE = "hsl(var(--theme-blue))";
