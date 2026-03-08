import { NavLink } from "react-router-dom";
import { cn } from "@/lib/utils";

const tabs = [
  { label: "Users", to: "/admin/users" },
  { label: "Roles", to: "/admin/roles" },
  { label: "LDAP", to: "/admin/ldap" },
  { label: "OIDC / SSO", to: "/admin/oidc" },
  { label: "Branding", to: "/admin/branding" },
];

export function AdminNav() {
  return (
    <nav className="flex gap-1 border-b px-6 pt-4">
      {tabs.map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          className={({ isActive }) =>
            cn(
              "px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors",
              isActive
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground hover:border-muted-foreground/50",
            )
          }
        >
          {tab.label}
        </NavLink>
      ))}
    </nav>
  );
}
