import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-hidden focus:ring-2 focus:ring-ring focus:ring-offset-2",
  {
    variants: {
      variant: {
        default:
          "border-transparent bg-primary/15 text-primary hover:bg-primary/25",
        secondary:
          "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80",
        destructive:
          "border-transparent bg-destructive text-destructive-foreground shadow-sm hover:bg-destructive/80",
        outline: "text-foreground",
        // A filled pill whose colour the call site supplies (bg-emerald-600).
        // Not for a healthy state: that is `healthy`, which stays quiet.
        solid: "border-transparent text-white shadow-sm",
        // "This is fine": a plain chip with a small green dot, so a screen of
        // healthy things does not shout.
        healthy:
          "gap-1.5 border-border text-foreground before:h-1.5 before:w-1.5 before:shrink-0 before:rounded-full before:bg-emerald-500 before:content-['']",
        warning:
          "border-amber-500/40 bg-amber-500/15 text-amber-700 dark:text-amber-400",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

/**
 * The variants `badgeVariants` actually defines, derived rather than restated:
 * adding one to the cva above widens every consumer instead of leaving a
 * hand-copied union behind to drift. `NonNullable` drops the `null` and
 * `undefined` that VariantProps admits for "variant not set".
 */
export type BadgeVariant = NonNullable<
  VariantProps<typeof badgeVariants>["variant"]
>;

export interface BadgeProps
  extends
    React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

// eslint-disable-next-line react-refresh/only-export-components
export { Badge, badgeVariants };
