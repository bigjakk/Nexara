import { useEffect, useRef } from "react";
import { NavLink, useLocation } from "react-router-dom";
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
  Users,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/hooks/useAuth";
import { useSidebarStore } from "@/stores/sidebar-store";
import { InventoryTree } from "./InventoryTree";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface NavItem {
  label: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  requiredPermission?: string;
}

const navItems: NavItem[] = [
  { label: "Dashboard", to: "/", icon: LayoutDashboard },
  { label: "Inventory", to: "/inventory", icon: Package },
  { label: "Topology", to: "/topology", icon: Network },
  { label: "Console", to: "/console", icon: TerminalSquare },
  { label: "Storage", to: "/storage", icon: HardDrive },
  { label: "Backup", to: "/backup", icon: Shield },
  { label: "Alerts", to: "/alerts", icon: Bell, requiredPermission: "view:alert" },
  { label: "Reports", to: "/reports", icon: FileText, requiredPermission: "view:report" },
  { label: "Security", to: "/security", icon: ShieldAlert, requiredPermission: "view:cve_scan" },
  { label: "Audit Log", to: "/audit-log", icon: ScrollText, requiredPermission: "view:audit" },
  { label: "Admin", to: "/admin/users", icon: Users, requiredPermission: "manage:user" },
];

function isInventoryRoute(pathname: string): boolean {
  return pathname.startsWith("/inventory") || pathname.startsWith("/clusters");
}

export function Sidebar() {
  const { hasPermission } = useAuth();
  const { collapsed, toggleCollapsed, treeVisible, setTreeVisible } = useSidebarStore();
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

  return (
    <TooltipProvider delayDuration={0}>
      <aside
        className={cn(
          "flex h-full flex-col border-r bg-card transition-all duration-200",
          collapsed ? "w-12" : "w-60",
        )}
      >
        {/* Header */}
        <div className="flex h-14 shrink-0 items-center border-b px-2">
          {!collapsed && (
            <>
              <Server className="ml-2 h-6 w-6 shrink-0 text-primary" />
              <span className="ml-2 text-lg font-semibold">ProxDash</span>
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
                    {item.label}
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
                    {item.label}
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
      </aside>
    </TooltipProvider>
  );
}
