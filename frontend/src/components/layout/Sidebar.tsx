import { useCallback, useEffect, useRef } from "react";
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
  HardDrive,
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
import { InventoryTree } from "./InventoryTree";
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

const navItems: NavItem[] = [
  { labelKey: "dashboard", to: "/", icon: LayoutDashboard },
  { labelKey: "inventory", to: "/inventory", icon: Package },
  { labelKey: "topology", to: "/topology", icon: Network },
  { labelKey: "console", to: "/console", icon: TerminalSquare },
  { labelKey: "storage", to: "/storage", icon: HardDrive },
  { labelKey: "backup", to: "/backup", icon: Shield },
  { labelKey: "alerts", to: "/alerts", icon: Bell, requiredPermission: "view:alert" },
  { labelKey: "reports", to: "/reports", icon: FileText, requiredPermission: "view:report" },
  { labelKey: "security", to: "/security", icon: ShieldAlert, requiredPermission: "view:cve_scan" },
  { labelKey: "events", to: "/events", icon: ScrollText, requiredPermission: "view:audit" },
  { labelKey: "settings", to: "/settings/appearance", icon: Settings },
  { labelKey: "admin", to: "/admin/users", icon: Users, requiredPermission: "manage:user" },
];

function isInventoryRoute(pathname: string): boolean {
  return pathname.startsWith("/inventory") || pathname.startsWith("/clusters");
}

export function Sidebar() {
  const { t } = useTranslation("navigation");
  const { hasPermission } = useAuth();
  const { collapsed, toggleCollapsed, treeVisible, setTreeVisible, width, setWidth } = useSidebarStore();
  const { appTitle, logoUrl } = useBrandingStore();
  const location = useLocation();
  const prevPathRef = useRef(location.pathname);

  // Auto-expand tree when navigating to inventory/clusters routes
  useEffect(() => {
    const prev = prevPathRef.current;
    prevPathRef.current = location.pathname;
    if (isInventoryRoute(location.pathname) && !isInventoryRoute(prev)) {
      setTreeVisible(true);
    }
  }, [location.pathname, setTreeVisible]);

  const visibleItems = navItems.filter((item) => {
    if (!item.requiredPermission) return true;
    const parts = item.requiredPermission.split(":");
    return hasPermission(parts[0] ?? "", parts[1] ?? "");
  });

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
          "relative flex h-full shrink-0 flex-col border-r bg-card",
          collapsed ? "w-12" : "",
          collapsed ? "transition-all duration-200" : "",
        )}
        style={collapsed ? undefined : { width: `${width}px` }}
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
              <span className="ml-2 text-lg font-semibold">{appTitle}</span>
            </>
          )}
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
        </div>

        {/* Nav items */}
        <nav className="flex-1 space-y-1 overflow-y-auto p-2">
          {visibleItems.map((item) => {
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
                            ? "bg-accent text-accent-foreground"
                            : "text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
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
                          ? "bg-accent text-accent-foreground"
                          : "text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
                      )
                    }
                  >
                    <item.icon className="h-4 w-4" />
                    {label}
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
                  <div className="mt-1 max-h-[calc(100vh-20rem)] overflow-y-auto">
                    <InventoryTree />
                  </div>
                )}
              </div>
            );
          })}
        </nav>
        {/* Resize handle */}
        {!collapsed && (
          <div
            onMouseDown={handleMouseDown}
            className="absolute inset-y-0 -right-1 w-2 cursor-col-resize hover:bg-primary/20 active:bg-primary/30 transition-colors"
          />
        )}
      </aside>
    </TooltipProvider>
  );
}
