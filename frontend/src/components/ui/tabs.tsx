import * as React from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";

import { cn } from "@/lib/utils";
import {
  FallbackFocusContext,
  setRef,
  useFallbackFocus,
} from "@/components/ui/return-focus";

/**
 * Radix's Root, which also gives a dialog inside it somewhere to send focus
 * when the element that opened it has gone by the time it closes — the row
 * it deleted: the panel on show, the section that row was in (see
 * return-focus.ts). Radix makes a panel focusable. Tabs inside tabs offer
 * their own panel first.
 */
const Tabs = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Root>
>((props, ref) => {
  const root = React.useRef<HTMLDivElement | null>(null);
  const setRoot = React.useCallback(
    (node: HTMLDivElement | null) => {
      root.current = node;
      const cleanup = setRef(ref, node);
      if (cleanup === undefined) return undefined;
      // With a cleanup to run, React does not call this ref again with null.
      return () => {
        root.current = null;
        cleanup();
      };
    },
    [ref],
  );
  const panel = React.useCallback(() => activePanel(root.current), []);
  const fallbacks = useFallbackFocus(panel);
  return (
    <FallbackFocusContext value={fallbacks}>
      <TabsPrimitive.Root ref={setRoot} {...props} />
    </FallbackFocusContext>
  );
});
Tabs.displayName = TabsPrimitive.Root.displayName;

// These tabs' own panel on show, not that of tabs inside it: Radix renders
// every panel but fills only the active one, and that one comes before
// anything inside it. (forceMount fills an inactive panel too, left to CSS to
// hide: tabs in it could come first, but what CSS hides cannot take focus,
// so the next fallback would be used, not a wrong one.)
function activePanel(root: HTMLElement | null): HTMLElement | null {
  return (
    root?.querySelector<HTMLElement>(
      '[role="tabpanel"][data-state="active"]',
    ) ?? null
  );
}

const TabsList = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    className={cn(
      // justify-start + overflow-x-auto (not center): when the strip is wider
      // than a phone viewport it scrolls within itself instead of widening
      // the page; centered content inside a scroll container clips its start.
      "inline-flex h-9 max-w-full items-center justify-start overflow-x-auto rounded-lg bg-muted p-1 text-muted-foreground",
      className,
    )}
    {...props}
  />
));
TabsList.displayName = TabsPrimitive.List.displayName;

const TabsTrigger = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Trigger
    ref={ref}
    className={cn(
      "inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm",
      className,
    )}
    {...props}
  />
));
TabsTrigger.displayName = TabsPrimitive.Trigger.displayName;

const TabsContent = React.forwardRef<
  React.ComponentRef<typeof TabsPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Content>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Content
    ref={ref}
    className={cn(
      "mt-2 ring-offset-background focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
      className,
    )}
    {...props}
  />
));
TabsContent.displayName = TabsPrimitive.Content.displayName;

export { Tabs, TabsList, TabsTrigger, TabsContent };
