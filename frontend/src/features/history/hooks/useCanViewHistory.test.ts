import { afterEach, describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { useAuthStore } from "@/stores/auth-store";
import { useCanViewHistory } from "./useCanViewHistory";

afterEach(() => {
  useAuthStore.setState({
    user: null,
    permissions: [],
    globalPermissions: [],
    isAuthenticated: false,
  });
});

// The rule every History control holds. An object of no one cluster is read by
// a route that takes only grants held on the whole install, so a grant on one
// cluster (in `permissions`, not in `globalPermissions`) is not enough for the
// audit log nor for any of the object's own; view:task is a plain grant.
describe("useCanViewHistory", () => {
  const flat = ["view:audit", "view:task", "view:pbs", "view:backup"];
  it.each<[string, string | string[], boolean, string[], boolean]>([
    [
      "a cluster's object, with the grants of any scope",
      "pbs",
      false,
      [],
      true,
    ],
    [
      "a global object, with every grant on the whole install",
      ["pbs", "backup"],
      true,
      ["view:audit", "view:pbs", "view:backup"],
      true,
    ],
    [
      "a global object, the audit log held on a cluster only",
      "pbs",
      true,
      ["view:pbs"],
      false,
    ],
    [
      "a global object, its own grant held on a cluster only",
      "pbs",
      true,
      ["view:audit"],
      false,
    ],
    [
      "a global object of two grants, one held on a cluster only",
      ["pbs", "backup"],
      true,
      ["view:audit", "view:pbs"],
      false,
    ],
  ])("%s", (_, resource, global, globalPermissions, offered) => {
    useAuthStore.setState({
      user: {
        id: "u1",
        email: "u@example.test",
        display_name: "U",
        role: "user",
      },
      permissions: flat,
      globalPermissions,
    });
    const { result } = renderHook(() => useCanViewHistory(resource, global));
    expect(result.current).toBe(offered);
  });
});
