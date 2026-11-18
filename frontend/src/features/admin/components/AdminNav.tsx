import { NavLink } from "react-router-dom";
import { usePermissions } from "@/hooks/usePermissions";
import { cn } from "@/lib/utils";

// Each tab takes what its page's route guard takes (App.tsx), install-wide.
const tabs = [
  { label: "Users", to: "/admin/users", action: "manage", resource: "user" },
  { label: "Roles", to: "/admin/roles", action: "manage", resource: "role" },
  { label: "LDAP", to: "/admin/ldap", action: "manage", resource: "user" },
  {
    label: "OIDC / SSO",
    to: "/admin/oidc",
    action: "manage",
    resource: "user",
  },
  {
    label: "Branding",
    to: "/admin/branding",
    action: "manage",
    resource: "settings",
  },
];

export function AdminNav() {
  const { hasGlobalPermission } = usePermissions();
  return (
    <nav className="mb-5 flex gap-5 overflow-x-auto shadow-[inset_0_-1px_0_0_hsl(var(--border))]">
      {tabs
        .filter((tab) => hasGlobalPermission(tab.action, tab.resource))
        .map((tab) => (
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
