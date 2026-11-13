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
 * - PBS:   a check. Verifying what it stored is what distinguishes it.
 */
const GLYPHS: Record<BackupProvider, LucideIcon> = {
  veeam: StretchHorizontal,
  pbs: Check,
};

interface ProviderBadgeProps {
  provider: BackupProvider;
  /** `sm` sits inside a tab label; `md` leads a server's identity line. */
  size?: "sm" | "md";
  className?: string;
}

/** The accent tile: a filled rounded square carrying the provider's glyph. */
export function ProviderBadge({
  provider,
  size = "sm",
  className,
}: ProviderBadgeProps) {
  const Glyph = GLYPHS[provider];
  const isLarge = size === "md";
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex shrink-0 items-center justify-center",
        PROVIDER_FILL_CLASSES[provider],
        isLarge ? "h-7 w-7 rounded-[7px]" : "h-4 w-4 rounded-[4px]",
        className,
      )}
    >
      <Glyph
        className={isLarge ? "h-4 w-4" : "h-2.5 w-2.5"}
        strokeWidth={isLarge ? 2.5 : 3.25}
      />
    </span>
  );
}

interface ProviderHeaderProps {
  provider: BackupProvider;
  /**
   * The product's full name, spelled the way the vendor spells it. Read out
   * before the server's name; on screen the accent tile stands for it.
   */
  product: string;
  /** The server's own name. */
  name: string;
  /** Where it is reached, in small mono. */
  address?: string | undefined;
  /** Version, edition: whatever else says which server this is. */
  facts?: readonly string[] | undefined;
  /** Connection state, rendered by the caller so it matches its own table. */
  status?: ReactNode;
  /** The choice between servers, when there are several. */
  switcher?: ReactNode;
  /** Edit and the rarer controls. */
  actions?: ReactNode;
}

/**
 * Names the server you are looking at before showing you rows of it: one line
 * on the canvas, not a box. The tab strip above says which product, and it
 * scrolls out of view on a narrow viewport; the accent tile stays.
 */
export function ProviderHeader({
  provider,
  product,
  name,
  address,
  facts,
  status,
  switcher,
  actions,
}: ProviderHeaderProps) {
  // "" is excluded as well as undefined, so an unknown version leaves no gap.
  const details = (facts ?? []).filter((fact) => fact !== "");
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2.5">
      <div className="flex min-w-0 flex-[1_1_280px] flex-wrap items-center gap-x-3 gap-y-1.5">
        <ProviderBadge provider={provider} size="md" />
        <h2 className="min-w-0 text-[15px] font-semibold [overflow-wrap:anywhere]">
          <span className="sr-only">{product}: </span>
          {name}
        </h2>
        {address !== undefined && address !== "" && (
          <span className="min-w-0 font-mono text-[12.5px] text-muted-foreground [overflow-wrap:anywhere]">
            {address}
          </span>
        )}
        {details.length > 0 && (
          <span className="text-[12.5px] text-muted-foreground">
            {details.join(" · ")}
          </span>
        )}
        {status}
      </div>
      {switcher}
      {actions !== undefined && (
        <div className="flex items-center gap-2">{actions}</div>
      )}
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
