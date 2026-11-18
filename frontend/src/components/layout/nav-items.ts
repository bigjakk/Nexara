import {
  BatteryCharging,
  Bell,
  Camera,
  FileText,
  LayoutDashboard,
  Network,
  Package,
  ScrollText,
  Settings,
  Shield,
  ShieldAlert,
  TerminalSquare,
  Upload,
  Users,
} from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { usePermissions } from "@/hooks/usePermissions";

export interface NavItem {
  labelKey: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  requiredPermission?: string;
  /** requiredPermission must be held install-wide (see RequirePermission). */
  global?: boolean;
}

export interface NavSection {
  labelKey: string;
  items: NavItem[];
}

/** The app's destinations, shared by the desktop rail and the mobile drawer. */
export const NAV_SECTIONS: NavSection[] = [
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
      { labelKey: "snapshots", to: "/snapshots", icon: Camera },
      {
        labelKey: "power",
        to: "/power",
        icon: BatteryCharging,
        requiredPermission: "view:ups",
        global: true,
      },
      {
        labelKey: "alerts",
        to: "/alerts",
        icon: Bell,
        requiredPermission: "view:alert",
      },
      {
        labelKey: "reports",
        to: "/reports",
        icon: FileText,
        requiredPermission: "view:report",
      },
      {
        labelKey: "security",
        to: "/security",
        icon: ShieldAlert,
        requiredPermission: "view:cve_scan",
      },
      {
        labelKey: "imports",
        to: "/imports",
        icon: Upload,
        requiredPermission: "view:vm_import",
      },
      {
        labelKey: "events",
        to: "/events",
        icon: ScrollText,
        requiredPermission: "view:audit",
      },
    ],
  },
  {
    labelKey: "sectionSystem",
    items: [
      { labelKey: "settings", to: "/settings/appearance", icon: Settings },
      {
        labelKey: "admin",
        to: "/admin/users",
        icon: Users,
        // As the admin pages' guards take it (App.tsx).
        requiredPermission: "manage:user",
        global: true,
      },
    ],
  },
];

/** The routes the Inventory destination stands for, cluster and storage pages included. */
export function isInventoryRoute(pathname: string): boolean {
  return (
    pathname.startsWith("/inventory") ||
    pathname.startsWith("/clusters") ||
    pathname.startsWith("/storage")
  );
}

/**
 * Whether a destination is the one the current page belongs to. An item links
 * to one page of its area (Settings opens Appearance), so it is matched on the
 * area's first path segment rather than on the link itself.
 */
export function isNavItemActive(item: NavItem, pathname: string): boolean {
  if (item.to === "/") return pathname === "/";
  if (item.to === "/inventory") return isInventoryRoute(pathname);
  const area = `/${item.to.split("/")[1] ?? ""}`;
  return pathname === area || pathname.startsWith(`${area}/`);
}

type PermissionCheck = (action: string, resource: string) => boolean;

/**
 * The sections a caller may see: an item without its permission is dropped,
 * and a section left empty goes with it.
 */
export function filterNavSections(
  sections: NavSection[],
  hasPermission: PermissionCheck,
  hasGlobalPermission: PermissionCheck,
): NavSection[] {
  return sections
    .map((section) => ({
      ...section,
      items: section.items.filter((item) => {
        if (!item.requiredPermission) return true;
        const parts = item.requiredPermission.split(":");
        const held = item.global === true ? hasGlobalPermission : hasPermission;
        return held(parts[0] ?? "", parts[1] ?? "");
      }),
    }))
    .filter((section) => section.items.length > 0);
}

export function useVisibleNavSections(): NavSection[] {
  const { hasPermission } = useAuth();
  const { hasGlobalPermission } = usePermissions();
  return filterNavSections(NAV_SECTIONS, hasPermission, hasGlobalPermission);
}
