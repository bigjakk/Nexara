import { describe, it, expect } from "vitest";
import {
  filterNavSections,
  isNavItemActive,
  type NavItem,
  type NavSection,
} from "./nav-items";

const Icon = () => null;
const item = (to: string, over: Partial<NavItem> = {}): NavItem => ({
  labelKey: to,
  to,
  icon: Icon,
  ...over,
});

describe("filterNavSections", () => {
  const sections: NavSection[] = [
    {
      labelKey: "open",
      items: [
        item("/"),
        item("/alerts", { requiredPermission: "view:alert" }),
        item("/power", { requiredPermission: "view:ups", global: true }),
      ],
    },
    {
      labelKey: "gated",
      items: [item("/admin/users", { requiredPermission: "manage:user" })],
    },
  ];
  const holds =
    (...held: string[]) =>
    (action: string, resource: string) =>
      held.includes(`${action}:${resource}`);

  it.each([
    {
      name: "an ungated item needs nothing, and a section left empty is dropped",
      scoped: holds(),
      global: holds(),
      want: [["/"]],
    },
    {
      name: "a gated item shows once its permission is held",
      scoped: holds("view:alert", "manage:user"),
      global: holds(),
      want: [["/", "/alerts"], ["/admin/users"]],
    },
    {
      name: "a global item asks the install-wide check, not the scoped one",
      scoped: holds("view:ups"),
      global: holds(),
      want: [["/"]],
    },
    {
      name: "a global item shows when held install-wide",
      scoped: holds(),
      global: holds("view:ups"),
      want: [["/", "/power"]],
    },
  ])("$name", ({ scoped, global, want }) => {
    expect(
      filterNavSections(sections, scoped, global).map((s) =>
        s.items.map((i) => i.to),
      ),
    ).toEqual(want);
  });
});

describe("isNavItemActive", () => {
  it.each([
    { to: "/", pathname: "/", want: true },
    { to: "/", pathname: "/alerts", want: false },
    // Inventory stands for the cluster and storage pages too.
    { to: "/inventory", pathname: "/clusters/c1/vms/100", want: true },
    { to: "/inventory", pathname: "/storage/c1/store01", want: true },
    // An item links to one page of its area and is lit on all of them.
    { to: "/settings/appearance", pathname: "/settings/profile", want: true },
    { to: "/admin/users", pathname: "/admin/roles", want: true },
    // A prefix is not a segment.
    { to: "/backup", pathname: "/backups-old", want: false },
  ])("$to on $pathname is $want", ({ to, pathname, want }) => {
    expect(isNavItemActive(item(to), pathname)).toBe(want);
  });
});
