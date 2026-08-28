import type { ReactNode } from "react";
import { Check, StretchHorizontal, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  PROVIDER_FILL_CLASSES,
  PROVIDER_TRADEMARK_OWNERS,
  type BackupProvider,
} from "./provider-accents";

/**
 * Visual identity for the backup providers Nexara integrates with.
 *
 * Every mark here is a Lucide glyph on one of our own accent colours (see
 * --provider-* in index.css). Nothing is derived from a vendor's logo or brand
 * palette, and that is a hard constraint rather than a stylistic one: Nexara
 * ships AGPL-3.0, and vendor brand assets cannot be sublicensed under it. A
 * logo committed to this tree would make the repository something we have no
 * right to redistribute.
 *
 * Naming the products is a separate question and is fine — that is what
 * trademarks are for. The panels spell them out in full and carry an
 * attribution line.
 */

/**
 * Chosen for shape, not for the name Lucide files them under:
 *
 * - Veeam: two stacked bars — one copy, then another. Replication.
 * - PBS:   a check. Verifying what it stored is what distinguishes it, and
 *          the page's own header already spends the shield.
 */
const GLYPHS: Record<BackupProvider, LucideIcon> = {
  veeam: StretchHorizontal,
  pbs: Check,
};

interface ProviderBadgeProps {
  provider: BackupProvider;
  /** `sm` sits inside a tab label; `lg` anchors a panel header. */
  size?: "sm" | "lg";
  className?: string;
}

/** The accent tile: a filled rounded square carrying the provider's glyph. */
export function ProviderBadge({
  provider,
  size = "sm",
  className,
}: ProviderBadgeProps) {
  const Glyph = GLYPHS[provider];
  const isLarge = size === "lg";
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex shrink-0 items-center justify-center",
        PROVIDER_FILL_CLASSES[provider],
        isLarge ? "h-10 w-10 rounded-lg" : "h-4 w-4 rounded-[4px]",
        className,
      )}
    >
      <Glyph
        className={isLarge ? "h-5 w-5" : "h-2.5 w-2.5"}
        strokeWidth={isLarge ? 2.25 : 3.25}
      />
    </span>
  );
}

interface ProviderHeaderProps {
  provider: BackupProvider;
  /** The product's full name, spelled the way the vendor spells it. */
  title: string;
  /**
   * Host, version, scope — whatever identifies *which* server this is.
   * Truncated to one line and mirrored into a `title`, so a clipped hostname
   * stays recoverable on hover.
   */
  subtitle?: string;
  /** Connection state, rendered by the caller so it matches its own table. */
  status?: ReactNode;
  /** Add / edit / delete controls. */
  actions?: ReactNode;
}

/**
 * Names what you are looking at before showing you rows of it.
 *
 * Both provider panels previously opened straight onto a bare table, leaving
 * the tab label as the only thing that said which product's state you were
 * reading — and on a narrow viewport that strip scrolls out of view.
 */
export function ProviderHeader({
  provider,
  title,
  subtitle,
  status,
  actions,
}: ProviderHeaderProps) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-md border bg-card p-4">
      <ProviderBadge provider={provider} size="lg" />
      <div className="min-w-0 flex-1">
        <h2 className="truncate text-sm font-medium">{title}</h2>
        {/* "" is excluded as well as undefined: joining an all-empty detail
            list yields "", which would render a blank row rather than none. */}
        {subtitle !== undefined && subtitle !== "" && (
          <div
            className="truncate text-xs text-muted-foreground"
            title={subtitle}
          >
            {subtitle}
          </div>
        )}
      </div>
      {status}
      {actions}
    </div>
  );
}

/** A tab label with its provider's mark, so the strip is scannable by colour. */
export function ProviderTabLabel({
  provider,
  children,
}: {
  provider: BackupProvider;
  children: ReactNode;
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <ProviderBadge provider={provider} />
      {children}
    </span>
  );
}

/**
 * The attribution that lets us go on calling the products by their names.
 *
 * Takes every provider the page names rather than one, and belongs below the
 * whole tab set rather than inside a tab. Per-tab notes missed the panels'
 * loading and error returns, and the Coverage report names Veeam in its own
 * right while sitting under neither provider.
 */
export function ProviderTrademarkNote({
  providers,
}: {
  providers: BackupProvider[];
}) {
  return (
    <p className="text-xs text-muted-foreground">
      {providers.map((p) => PROVIDER_TRADEMARK_OWNERS[p]).join(" ")} Nexara is
      an independent project, not affiliated with or endorsed by them.
    </p>
  );
}
