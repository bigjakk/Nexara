import { Fragment } from "react";
import { Link, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Server } from "lucide-react";
import { BrandingLogo } from "@/components/BrandingLogo";
import { cn } from "@/lib/utils";
import { useBrandingStore } from "@/stores/branding-store";
import {
  isNavItemActive,
  useVisibleNavSections,
  type NavItem,
} from "./nav-items";

function RailLink({ item, pathname }: { item: NavItem; pathname: string }) {
  const { t } = useTranslation("navigation");
  const active = isNavItemActive(item, pathname);
  return (
    <Link
      to={item.to}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative flex w-full shrink-0 flex-col items-center gap-0.5 rounded-lg px-1 py-1.5 text-[10.5px] font-medium leading-tight transition-colors focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
        // The active item wears a rule at the rail's edge and its icon in the
        // accent (white when none is picked).
        active
          ? "bg-rail-accent text-rail-foreground before:absolute before:inset-y-2 before:-left-1.5 before:w-[3px] before:rounded-r-full before:bg-rail-marker"
          : "text-rail-muted hover:bg-rail-accent/60 hover:text-rail-foreground",
      )}
    >
      <item.icon
        className={cn("h-[19px] w-[19px]", active && "text-rail-marker")}
      />
      <span className="max-w-full truncate">{t(item.labelKey)}</span>
    </Link>
  );
}

/**
 * The desktop navigation: every destination as an icon over its name, in a
 * fixed place. The inventory tree lives in its own panel beside this, so
 * opening a cluster can no longer push Settings off the screen.
 */
export function NavRail() {
  const { pathname } = useLocation();
  const appTitle = useBrandingStore((s) => s.appTitle);
  const sections = useVisibleNavSections();
  // Settings and Admin stay pinned; the rest scrolls on a short screen.
  const pinned = sections.filter((s) => s.labelKey === "sectionSystem");
  const scrolling = sections.filter((s) => s.labelKey !== "sectionSystem");

  return (
    <aside className="flex h-full w-19 shrink-0 flex-col items-center bg-rail">
      <Link
        to="/"
        aria-label={appTitle}
        className="my-2.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-rail-accent text-rail-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
      >
        <BrandingLogo
          className="h-5 w-5 object-contain"
          fallback={<Server className="h-5 w-5" />}
        />
      </Link>
      <nav
        aria-label="Main"
        className="flex min-h-0 w-full flex-1 flex-col items-center"
      >
        <div className="flex min-h-0 w-full flex-1 flex-col items-center gap-0.5 overflow-y-auto px-1.5 pb-2">
          {scrolling.map((section, index) => (
            <Fragment key={section.labelKey}>
              {index > 0 && (
                <div
                  aria-hidden="true"
                  className="my-1.5 h-px w-7 shrink-0 bg-rail-accent"
                />
              )}
              {section.items.map((item) => (
                <RailLink key={item.to} item={item} pathname={pathname} />
              ))}
            </Fragment>
          ))}
        </div>
        <div className="flex w-full shrink-0 flex-col items-center gap-0.5 border-t border-rail-accent px-1.5 py-2">
          {pinned
            .flatMap((section) => section.items)
            .map((item) => (
              <RailLink key={item.to} item={item} pathname={pathname} />
            ))}
        </div>
      </nav>
    </aside>
  );
}
