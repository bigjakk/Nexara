import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"
import { usePreferencesStore } from "@/stores/preferences-store"

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default:
          "bg-primary text-primary-foreground shadow-sm not-disabled:hover:bg-primary/90",
        destructive:
          "bg-destructive text-destructive-foreground shadow-xs not-disabled:hover:bg-destructive/90",
        outline:
          "border border-input bg-background shadow-xs not-disabled:hover:bg-accent not-disabled:hover:text-accent-foreground",
        secondary:
          "bg-secondary text-secondary-foreground shadow-xs not-disabled:hover:bg-secondary/80",
        ghost: "not-disabled:hover:bg-accent not-disabled:hover:text-accent-foreground",
        link: "text-primary underline-offset-4 not-disabled:hover:underline",
      },
      size: {
        default: "h-9 px-4 py-2",
        sm: "h-8 rounded-md px-3 text-xs",
        lg: "h-10 rounded-md px-8",
        icon: "h-9 w-9",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

/** Expand fragments so `<>{icon}{label}</>` reshapes like `{icon}{label}`. */
function flattenChildren(children: React.ReactNode): React.ReactNode[] {
  return React.Children.toArray(children).flatMap((child) => {
    if (React.isValidElement(child) && child.type === React.Fragment) {
      const { children: inner } = child.props as { children?: React.ReactNode }
      return flattenChildren(inner)
    }
    return [child]
  })
}

/**
 * A child counts as an icon when it is a childless element — either a Lucide
 * component (`<Power className="h-4 w-4" />`) or a bare `<svg />`. Anything
 * that wraps content (`<span>Label</span>`, `<Badge>New</Badge>`) is treated
 * as label content instead, so only genuine glyphs are ever stripped.
 */
function isIconNode(node: React.ReactNode): boolean {
  if (!React.isValidElement(node)) return false
  if (typeof node.type === "string") return node.type === "svg"
  const { children } = node.props as { children?: React.ReactNode }
  return children === undefined || children === null
}

/** An in-flight spinner. Never strip one — it is the only progress signal. */
function isAnimatedNode(node: React.ReactNode): boolean {
  if (!React.isValidElement(node)) return false
  const { className } = node.props as { className?: string }
  return typeof className === "string" && className.includes("animate-")
}

/**
 * Flatten a node tree down to the text a sighted user actually reads.
 *
 * `sr-only` spans never count: they are the Shadcn way to name an icon button
 * (`<Moon /><span className="sr-only">Toggle theme</span>`), so treating them
 * as a label would make text-only mode drop the glyph and render a blank
 * button. With `skipResponsiveHidden`, breakpoint-hidden labels
 * (`<span className="hidden sm:inline">Create</span>`) don't count either —
 * dropping the icon next to one would leave nothing visible on a small screen.
 */
function visibleText(node: React.ReactNode, skipResponsiveHidden = false): string {
  if (typeof node === "string" || typeof node === "number") return String(node)
  if (Array.isArray(node)) {
    return (node as React.ReactNode[])
      .map((n) => visibleText(n, skipResponsiveHidden))
      .join("")
  }
  if (React.isValidElement(node)) {
    const { className, children } = node.props as {
      className?: string
      children?: React.ReactNode
    }
    if (typeof className === "string") {
      const classes = className.split(/\s+/)
      if (classes.includes("sr-only")) return ""
      if (skipResponsiveHidden && classes.includes("hidden")) return ""
    }
    return visibleText(children, skipResponsiveHidden)
  }
  return ""
}

/**
 * Tighten horizontal padding once a button collapses to a lone glyph, and let
 * a disabled one still show its `title` — the base style sets
 * `disabled:pointer-events-none`, which suppresses native tooltips, and a
 * disabled glyph with no tooltip is unidentifiable. The `disabled` attribute
 * still blocks activation on its own (reshaping never applies to `asChild`).
 *
 * The override works because tailwind-merge drops the conflicting
 * `disabled:pointer-events-none` from the class list — not because of CSS
 * order, which would go the other way. Restoring hit-testing also restores
 * `:hover`, which is why every variant above paints its hover state under
 * `not-disabled:` — a disabled button must never light up as clickable.
 */
const iconOnlyClass: Record<string, string> = {
  sm: "px-2 disabled:pointer-events-auto",
  default: "px-3 disabled:pointer-events-auto",
  lg: "px-3 disabled:pointer-events-auto",
}

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, children, ...props }, ref) => {
    const Comp = asChild ? Slot : "button"
    const buttonDisplay = usePreferencesStore((s) => s.preferences.buttonDisplay)

    // Allow-list the modes: an unrecognised stored value (the server
    // preferences blob is merged unvalidated) must mean "leave buttons alone",
    // not fall through to stripping every icon in the app.
    const mode =
      buttonDisplay === "icon" || buttonDisplay === "text" ? buttonDisplay : null

    // `asChild` hands a single element straight to Slot — rewriting children
    // there would break that contract, so those buttons keep their own markup.
    // A combobox trigger's text is the selected value, not a caption.
    const canReshape = !asChild && mode !== null && props.role !== "combobox"

    let content = children
    let extraClass: string | undefined
    let a11yLabel: string | undefined

    if (canReshape) {
      const items = flattenChildren(children)
      const rest = items.filter((item) => !isIconNode(item))
      const label = visibleText(rest).trim()

      // Only a glyph *before* the label is the button's identity. One after it
      // is an affordance — a sort arrow, a dropdown chevron — annotating text
      // that has to stay, so it neither licenses a reshape nor gets stripped.
      const firstTextIndex = items.findIndex(
        (item) => !isIconNode(item) && visibleText(item).trim() !== "",
      )
      const leadingIcons = items
        .slice(0, firstTextIndex === -1 ? items.length : firstTextIndex)
        .filter(isIconNode)

      // An icon-only button stays clickable and a text-only button (Cancel,
      // Save, and every confirm dialog) keeps reading as words in every mode.
      if (leadingIcons.length > 0 && label !== "") {
        if (mode === "icon") {
          content = leadingIcons
          extraClass = iconOnlyClass[size ?? "default"]
          a11yLabel = label
        } else if (
          visibleText(rest, true).trim() !== "" &&
          !items.filter(isIconNode).some(isAnimatedNode)
        ) {
          // Dropping the glyph is only safe when the label survives at every
          // breakpoint — otherwise the button would render empty on mobile —
          // and never while a spinner is showing, since in text mode that
          // glyph is the only progress signal the button has left.
          content = items.filter((item) => !leadingIcons.includes(item))
        }
      }
    }

    return (
      <Comp
        className={cn(buttonVariants({ variant, size }), className, extraClass)}
        ref={ref}
        {...props}
        title={props.title ?? a11yLabel}
        aria-label={props["aria-label"] ?? a11yLabel}
      >
        {content}
      </Comp>
    )
  }
)
Button.displayName = "Button"

// eslint-disable-next-line react-refresh/only-export-components -- buttonVariants is intentionally co-exported (Shadcn pattern)
export { Button, buttonVariants }
