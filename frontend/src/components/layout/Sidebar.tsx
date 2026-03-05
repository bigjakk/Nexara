import { NavLink } from "react-router-dom";
import {
  LayoutDashboard,
  Server,
  Package,
  Settings,
  Users,
  Shield,
  HardDrive,
  TerminalSquare,
  Database,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/hooks/useAuth";

interface NavItem {
  label: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  adminOnly?: boolean;
}

const navItems: NavItem[] = [
  { label: "Dashboard", to: "/", icon: LayoutDashboard },
  { label: "Clusters", to: "/clusters", icon: Server },
  { label: "Inventory", to: "/inventory", icon: Package },
  { label: "Console", to: "/console", icon: TerminalSquare },
  { label: "Storage", to: "/storage", icon: HardDrive },
  { label: "Ceph", to: "/ceph", icon: Database },
  { label: "Backup", to: "/backup", icon: Shield },
  { label: "Users", to: "/users", icon: Users, adminOnly: true },
  { label: "Settings", to: "/settings", icon: Settings, adminOnly: true },
];

export function Sidebar() {
  const { isAdmin } = useAuth();

  const visibleItems = navItems.filter(
    (item) => !item.adminOnly || isAdmin,
  );

  return (
    <aside className="flex h-full w-60 flex-col border-r bg-card">
      <div className="flex h-14 items-center gap-2 border-b px-4">
        <Server className="h-6 w-6 text-primary" />
        <span className="text-lg font-semibold">ProxDash</span>
      </div>
      <nav className="flex-1 space-y-1 p-2">
        {visibleItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === "/"}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
              )
            }
          >
            <item.icon className="h-4 w-4" />
            {item.label}
          </NavLink>
        ))}
      </nav>
    </aside>
  );
}
