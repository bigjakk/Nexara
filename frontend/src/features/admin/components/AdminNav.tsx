import { NavLink } from "react-router-dom";
import { usePermissions } from "@/hooks/usePermissions";
import { cn } from "@/lib/utils";
import { ADMIN_TABS } from "../lib/admin-tabs";

export function AdminNav() {
  const { hasGlobalPermission } = usePermissions();
  return (
    <nav className="mb-5 flex gap-5 overflow-x-auto shadow-[inset_0_-1px_0_0_hsl(var(--border))]">
      {ADMIN_TABS.filter((tab) =>
        hasGlobalPermission(tab.action, tab.resource),
      ).map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          className={({ isActive }) =>
            cn(
              "whitespace-nowrap border-b-2 px-0.5 pb-2 pt-1.5 text-sm font-medium transition-colors",
              isActive
                ? "border-marker text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )
          }
        >
          {tab.label}
        </NavLink>
      ))}
    </nav>
  );
}
