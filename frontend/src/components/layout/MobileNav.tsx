import { Link, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronRight, Server } from "lucide-react";
import { cn } from "@/lib/utils";
import { useBrandingStore } from "@/stores/branding-store";
import { useSidebarStore } from "@/stores/sidebar-store";
import { TreeView } from "./TreeView";
import { isNavItemActive, useVisibleNavSections } from "./nav-items";

/**
 * The phone's navigation drawer: the same destinations as the desktop rail,
 * spelled out, with the inventory tree folded under Inventory.
 */
export function MobileNav() {
  const { t } = useTranslation("navigation");
  const { pathname } = useLocation();
  const { appTitle, logoUrl } = useBrandingStore();
  const sections = useVisibleNavSections();
  const treeVisible = useSidebarStore((s) => s.treeVisible);
  const setTreeVisible = useSidebarStore((s) => s.setTreeVisible);

  return (
    <div className="flex h-full flex-col bg-card">
      {/* Room on the right for the sheet's own close button. */}
      <div className="flex h-14 shrink-0 items-center gap-2 border-b pl-4 pr-12">
        {logoUrl ? (
          <img
            src={logoUrl}
            alt=""
            className="h-6 w-6 shrink-0 object-contain"
          />
        ) : (
          <Server className="h-5 w-5 shrink-0 text-muted-foreground" />
        )}
        <span className="truncate text-lg font-semibold tracking-tight">
          {appTitle}
        </span>
      </div>
      <nav aria-label="Main" className="flex-1 overflow-y-auto p-2">
        {sections.map((section, sectionIndex) => (
          <div key={section.labelKey}>
            <div
              className={cn(
                "px-3 pb-1 text-xs font-medium text-muted-foreground",
                sectionIndex === 0 ? "pt-1" : "pt-4",
              )}
            >
              {t(section.labelKey)}
            </div>
            <div className="space-y-0.5">
              {section.items.map((item) => {
                const active = isNavItemActive(item, pathname);
                const isInventory = item.to === "/inventory";
                return (
                  <div key={item.to}>
                    <div className="flex items-center">
                      <Link
                        to={item.to}
                        aria-current={active ? "page" : undefined}
                        className={cn(
                          "flex flex-1 items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                          active
                            ? "bg-accent text-foreground"
                            : "text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground",
                        )}
                      >
                        <item.icon
                          className={cn("h-4 w-4", active && "text-marker")}
                        />
                        {t(item.labelKey)}
                      </Link>
                      {isInventory && (
                        <button
                          type="button"
                          aria-label={
                            treeVisible
                              ? "Hide inventory tree"
                              : "Show inventory tree"
                          }
                          aria-expanded={treeVisible}
                          onClick={() => {
                            setTreeVisible(!treeVisible);
                          }}
                          className="rounded-md p-2 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                        >
                          {treeVisible ? (
                            <ChevronDown className="h-4 w-4" />
                          ) : (
                            <ChevronRight className="h-4 w-4" />
                          )}
                        </button>
                      )}
                    </div>
                    {isInventory && treeVisible && (
                      <TreeView className="mt-1 max-h-[50dvh]" />
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </nav>
    </div>
  );
}
