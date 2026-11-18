import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

import { useAuthStore } from "@/stores/auth-store";
import type { APIEndpoint } from "@/types/api";
import { APIDocsPage } from "./APIDocsPage";

const declared: APIEndpoint = {
  method: "POST",
  path: "/api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach",
  description: "Allocate a new disk and attach it.",
  permission: "manage:vm",
  group: "Virtual Machines",
  parameters: [
    {
      name: "index",
      type: "integer",
      source: "body",
      optional: true,
      description: "Slot on the bus. Omit to take the lowest free one.",
    },
    {
      name: "storage",
      type: "string",
      source: "body",
      optional: false,
      description: "Storage to allocate on.",
    },
  ],
};

// An entry the payload sends with no parameter list — here one of the few
// routes that predate the parameter schema, though the page cannot tell that
// apart from a declared route that declares none.
const withoutParameters: APIEndpoint = {
  method: "POST",
  path: "/api/v1/alert-rules",
  description: "Create an alert rule",
  permission: "manage:alert",
  group: "Alerts",
};

const endpoints = [declared, withoutParameters];

vi.mock("../api/api-docs-queries", () => ({
  useAPIDocs: () => ({ data: endpoints, isLoading: false }),
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <APIDocsPage />
    </MemoryRouter>,
  );
}

describe("APIDocsPage", () => {
  afterEach(() => {
    useAuthStore.setState({ permissions: [], globalPermissions: [] });
  });

  it("lists every endpoint, grouped", () => {
    renderPage();
    expect(screen.getByText("Virtual Machines")).toBeInTheDocument();
    expect(screen.getByText("Alerts")).toBeInTheDocument();
    expect(screen.getByText(declared.path)).toBeInTheDocument();
    expect(screen.getByText(withoutParameters.path)).toBeInTheDocument();
  });

  it("matches a parameter name in the filter", async () => {
    const user = userEvent.setup();
    renderPage();

    // "index" appears in no path, description or group — only as a
    // parameter. Before parameters were searchable this query emptied the
    // page, hiding the very endpoint the reader was looking for.
    await user.type(screen.getByPlaceholderText(/filter endpoints/i), "index");

    expect(screen.getByText(declared.path)).toBeInTheDocument();
    expect(screen.queryByText(withoutParameters.path)).not.toBeInTheDocument();
    expect(
      screen.queryByText(/no endpoints match your search/i),
    ).not.toBeInTheDocument();
  });

  it("still matches on path, description and group", async () => {
    const user = userEvent.setup();
    renderPage();

    const input = screen.getByPlaceholderText(/filter endpoints/i);
    await user.type(input, "alert");
    expect(screen.getByText(withoutParameters.path)).toBeInTheDocument();
    expect(screen.queryByText(declared.path)).not.toBeInTheDocument();
  });

  it("expands a declared endpoint into its parameter table", async () => {
    const user = userEvent.setup();
    renderPage();

    // The row with no parameter list has no button, so the only one inside
    // the endpoint list belongs to the declared route.
    const row = screen.getByRole("button", { name: /disks\/attach/ });
    await user.click(row);

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("Requirement")).toBeInTheDocument();
  });

  // Creating a key takes manage:api_key install-wide (registry_api_keys.go).
  it.each<[string, string[], boolean]>([
    ["on a cluster only", [], false],
    ["install-wide", ["manage:api_key"], true],
  ])(
    "with manage:api_key %s (install-wide: %j) offers Create an API key: %s",
    (_held, globalPermissions, offered) => {
      useAuthStore.setState({
        permissions: ["manage:api_key"],
        globalPermissions,
      });
      renderPage();
      expect(
        screen.queryByRole("button", { name: "Create an API key" }) !== null,
      ).toBe(offered);
    },
  );
});
