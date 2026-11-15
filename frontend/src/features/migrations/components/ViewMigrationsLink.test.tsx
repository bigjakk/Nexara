import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { usePermissions } from "@/hooks/usePermissions";
import { ViewMigrationsLink } from "./ViewMigrationsLink";

vi.mock("@/hooks/usePermissions", () => ({ usePermissions: vi.fn() }));

describe("ViewMigrationsLink", () => {
  it.each([
    { held: true, shown: true },
    { held: false, shown: false },
  ])(
    "links to the Migrations page when view:migration is held: $held",
    ({ held, shown }) => {
      vi.mocked(usePermissions).mockReturnValue({
        canView: (resource: string) => held && resource === "migration",
      } as unknown as ReturnType<typeof usePermissions>);
      renderWithProviders(<ViewMigrationsLink />);

      const link = screen.queryByRole("link", { name: "View migrations" });
      if (shown) expect(link).toHaveAttribute("href", "/migrations");
      else expect(link).toBeNull();
    },
  );
});
