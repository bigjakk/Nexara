import { useCallback, useEffect, useRef, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  LayoutDashboard,
  Network,
  Package,
  Shield,
  ShieldAlert,
  Bell,
  FileText,
  TerminalSquare,
  ScrollText,
  PanelLeftClose,
  PanelLeftOpen,
  ChevronDown,
  ChevronRight,
  Server,
  Settings,
  Users,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/hooks/useAuth";
import { useSidebarStore } from "@/stores/sidebar-store";
import { useBrandingStore } from "@/stores/branding-store";
import { TreeView } from "./TreeView";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface NavItem {
  labelKey: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  requiredPermission?: string;
}

interface NavSection {
  labelKey: string;
  items: NavItem[];
}

const navSections: NavSection[] = [
  {
    labelKey: "sectionOverview",
    items: [
      { labelKey: "dashboard", to: "/", icon: LayoutDashboard },
      { labelKey: "inventory", to: "/inventory", icon: Package },
      { labelKey: "topology", to: "/topology", icon: Network },
      { labelKey: "console", to: "/console", icon: TerminalSquare },
    ],
  },
  {
    labelKey: "sectionOperations",
    items: [
      { labelKey: "backup", to: "/backup", icon: Shield },
      { labelKey: "alerts", to: "/alerts", icon: Bell, requiredPermission: "view:alert" },
      { labelKey: "reports", to: "/reports", icon: FileText, requiredPermission: "view:report" },
      { labelKey: "security", to: "/security", icon: ShieldAlert, requiredPermission: "view:cve_scan" },
      { labelKey: "events", to: "/events", icon: ScrollText, requiredPermission: "view:audit" },
    ],
  },
  {
    labelKey: "sectionSystem",
    items: [
      { labelKey: "settings", to: "/settings/appearance", icon: Settings },
      { labelKey: "admin", to: "/admin/users", icon: Users, requiredPermission: "manage:user" },
    ],
  },
];

function isInventoryRoute(pathname: string): boolean {
  return (
    pathname.startsWith("/inventory") ||
    pathname.startsWith("/clusters") ||
    pathname.startsWith("/storage")
  );
}

interface SidebarProps {
  /** Drawer mode (mobile nav sheet): always expanded, fills its container,
   * no collapse toggle or resize handle. */
  drawer?: boolean;
}

export function Sidebar({ drawer = false }: SidebarProps) {
  const { t } = useTranslation("navigation");
  const { hasPermission } = useAuth();
  const {
    collapsed: collapsedPref,
    toggleCollapsed,
    treeVisible,
    setTreeVisible,
    width,
    setWidth,
    setPerspective,
  } = useSidebarStore();
  const collapsed = drawer ? false : collapsedPref;
  const { appTitle, logoUrl } = useBrandingStore();
  const location = useLocation();
  const prevPathRef = useRef(location.pathname);
  const [appVersion, setAppVersion] = useState("");

  useEffect(() => {
    fetch("/api/v1/version")
      .then((r) => r.json() as Promise<{ version: string }>)
      .then((data) => { setAppVersion(data.version); })
      .catch(() => {});
  }, []);

  // Auto-expand tree when navigating to inventory/clusters/storage routes
  useEffect(() => {
    const prev = prevPathRef.current;
    prevPathRef.current = location.pathname;
    if (isInventoryRoute(location.pathname) && !isInventoryRoute(prev)) {
      setTreeVisible(true);
    }
    // Navigating into a storage-detail route is the one case where the tree
    // must be in a specific perspective — the route itself is only reachable
    // by clicking a pool inside the Storage tree, so flip there if needed.
    // Cluster/inventory routes are reachable from all three perspectives, so
    // we leave the user's choice alone.
    if (location.pathname.startsWith("/storage/")) {
      setPerspective("storage");
    }
  }, [location.pathname, setTreeVisible, setPerspective]);

  const visibleSections = navSections
    .map((section) => ({
      ...section,
      items: section.items.filter((item) => {
        if (!item.requiredPermission) return true;
        const parts = item.requiredPermission.split(":");
        return hasPermission(parts[0] ?? "", parts[1] ?? "");
      }),
    }))
    .filter((section) => section.items.length > 0);

  const showTree = !collapsed && treeVisible;
  const isResizing = useRef(false);

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (collapsed) return;
      e.preventDefault();
      isResizing.current = true;
      const startX = e.clientX;
      const startWidth = width;

      const onMouseMove = (ev: MouseEvent) => {
        if (!isResizing.current) return;
        const newWidth = startWidth + (ev.clientX - startX);
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        isResizing.current = false;
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [collapsed, width, setWidth],
  );

  return (
    <TooltipProvider delayDuration={0}>
      <aside
        className={cn(
          "relative flex h-full shrink-0 flex-col bg-card",
          drawer ? "w-full" : "border-r",
          collapsed ? "w-12" : "",
          collapsed ? "transition-all duration-200" : "",
        )}
        style={drawer || collapsed ? undefined : { width: `${String(width)}px` }}
      >
        {/* Header */}
        <div className="flex h-14 shrink-0 items-center border-b px-2">
          {!collapsed && (
            <>
              {logoUrl ? (
                <img src={logoUrl} alt={appTitle} className="ml-2 h-6 w-6 shrink-0 object-contain" />
              ) : (
                <Server className="ml-2 h-6 w-6 shrink-0 text-primary" />
              )}
              <span className="ml-2 text-lg font-semibold tracking-tight">{appTitle}</span>
              {appVersion && (
                <span className="ml-2 rounded-md bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium leading-none text-primary">{appVersion}</span>
              )}
            </>
          )}
          {!drawer && (
            <button
              onClick={toggleCollapsed}
              className={cn(
                "rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors",
                collapsed ? "mx-auto" : "ml-auto",
              )}
            >
              {collapsed ? (
                <PanelLeftOpen className="h-4 w-4" />
              ) : (
                <PanelLeftClose className="h-4 w-4" />
              )}
            </button>
          )}
        </div>

        {/* Nav items */}
        <nav className="flex-1 overflow-y-auto p-2">
          {visibleSections.map((section, sectionIndex) => (
            <div key={section.labelKey}>
              {collapsed ? (
                sectionIndex > 0 && <div className="mx-1 my-2 border-t" />
              ) : (
                <div
                  className={cn(
                    "px-3 pb-1 text-[11px] font-medium uppercase tracking-wider text-muted-foreground/70",
                    sectionIndex === 0 ? "pt-1" : "pt-4",
                  )}
                >
                  {t(section.labelKey)}
                </div>
              )}
              <div className="space-y-0.5">
                {section.items.map((item) => {
                  const label = t(item.labelKey);
                  // "Inventory" should be active for both /inventory/* and /clusters/*
                  const isInventoryItem = item.to === "/inventory";
                  const inventoryActive = isInventoryItem && isInventoryRoute(location.pathname);

                  if (collapsed) {
                    return (
                      <Tooltip key={item.to}>
                        <TooltipTrigger asChild>
                          <NavLink
                            to={item.to}
                            end={item.to === "/"}
                            className={({ isActive }) =>
                              cn(
                                "flex items-center justify-center rounded-md p-2 transition-colors",
                                isActive || inventoryActive
                                  ? "bg-primary/10 text-primary"
                                  : "text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground",
                              )
                            }
                          >
                            <item.icon className="h-4 w-4" />
                          </NavLink>
                        </TooltipTrigger>
                        <TooltipContent side="right">
                          {label}
                        </TooltipContent>
                      </Tooltip>
                    );
                  }

                  return (
                    <div key={item.to}>
                      <div className="flex items-center">
                        <NavLink
                          to={item.to}
                          end={item.to === "/" || isInventoryItem}
                          className={({ isActive }) =>
                            cn(
                              "flex flex-1 items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                              isActive || inventoryActive
                                ? "bg-primary/10 text-foreground shadow-[inset_2px_0_0_0_hsl(var(--primary))]"
                                : "text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground",
                            )
                          }
                        >
                          {({ isActive }) => (
                            <>
                              <item.icon
                                className={cn(
                                  "h-4 w-4",
                                  (isActive || inventoryActive) && "text-primary",
                                )}
                              />
                              {label}
                            </>
                          )}
                        </NavLink>
                        {isInventoryItem && (
                          <button
                            onClick={() => { setTreeVisible(!treeVisible); }}
                            className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
                          >
                            {showTree ? (
                              <ChevronDown className="h-3 w-3" />
                            ) : (
                              <ChevronRight className="h-3 w-3" />
                            )}
                          </button>
                        )}
                      </div>

                      {/* Render tree inline below Inventory — persists across routes */}
                      {isInventoryItem && showTree && (
                        <div
                          data-tree-scroller
                          className="mt-1 max-h-[calc(100vh-20rem)] overflow-y-auto"
                        >
                          <TreeView />
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>
        {/* Resize handle */}
        {!drawer && !collapsed && (
          <div
            onMouseDown={handleMouseDown}
            className="absolute inset-y-0 -right-1 w-2 cursor-col-resize hover:bg-primary/20 active:bg-primary/30 transition-colors"
          />
        )}
      </aside>
    </TooltipProvider>
  );
}
