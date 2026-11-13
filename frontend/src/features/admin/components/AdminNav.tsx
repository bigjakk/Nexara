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
    <nav className="mb-6 flex gap-5 overflow-x-auto shadow-[inset_0_-1px_0_0_hsl(var(--border))]">
      {tabs.map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          className={({ isActive }) =>
            cn(
              "whitespace-nowrap border-b-2 px-0.5 pb-2 pt-1.5 text-sm font-medium transition-colors",
              isActive
                ? "border-foreground text-foreground"
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
