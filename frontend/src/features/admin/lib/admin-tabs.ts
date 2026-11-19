/**
 * The admin pages in tab order. Each takes what its route guard takes
 * (App.tsx), install-wide; the admin tabs and the rail's Admin item read this.
 */
export const ADMIN_TABS = [
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
] as const;
