import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { useAuthStore } from "@/stores/auth-store";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { grantPermissions } from "@/test/permissions";
import { setupUser } from "@/test/user";
import { FirewallTemplatesTable } from "./FirewallTemplatesTable";
import type { FirewallTemplate } from "../types/network";

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const TEMPLATES_PATH = "/api/v1/firewall-templates";
const WEB_ID = "11111111-0000-0000-0000-000000000001";
const DB_ID = "11111111-0000-0000-0000-000000000002";

const TEMPLATES: FirewallTemplate[] = [
  {
    id: WEB_ID,
    name: "web",
    description: "",
    rules: [{ type: "in", action: "ACCEPT", dport: "443", enable: 1 }],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: DB_ID,
    name: "db",
    description: "",
    rules: [
      { type: "in", action: "ACCEPT", dport: "5432", enable: 1 },
      { type: "in", action: "DROP", enable: 1 },
    ],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  grantPermissions("manage:network", "delete:network");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// A template belongs to no cluster: creating and deleting one take
// manage:network and delete:network install-wide. Applying one takes
// manage:network on the cluster, where a grant on that cluster is enough.
// Without it the same dialog opens as View rules: the rules show nowhere else.
describe("FirewallTemplatesTable — what a role is offered", () => {
  const BOTH = ["manage:network", "delete:network"];

  it.each([
    [[], [], { apply: false, create: false, remove: false }],
    [BOTH, [], { apply: true, create: false, remove: false }],
    [BOTH, ["manage:network"], { apply: true, create: true, remove: false }],
    [BOTH, ["delete:network"], { apply: true, create: false, remove: true }],
  ])(
    "with %j, install-wide %j, offers %j",
    async (permissions, globalPermissions, expected) => {
      useAuthStore.setState({ user: null, permissions, globalPermissions });
      stubApi({ [TEMPLATES_PATH]: listOf(TEMPLATES) });
      const user = setupUser();
      renderWithProviders(<FirewallTemplatesTable clusterId="c1" />);
      await screen.findByText("db");

      const offered = (name: string) =>
        screen.queryByRole("button", { name }) !== null;
      const opener = expected.apply
        ? "Apply template db"
        : "View rules of template db";
      expect({
        apply: offered("Apply template db"),
        create: offered("Create template"),
        remove: offered("Delete template db"),
        view: offered("View rules of template db"),
      }).toEqual({ ...expected, view: !expected.apply });

      await user.click(screen.getByRole("button", { name: opener }));
      const dialog = await screen.findByRole("dialog");
      expect(dialog).toHaveTextContent(":5432");
      // What applying does, and its lock-out warning, only beside Apply.
      expect({
        applies:
          within(dialog).queryByRole("button", { name: "Apply to cluster" }) !==
          null,
        explains: within(dialog).queryByText(/^Adds 2 rules/) !== null,
        warns: within(dialog).queryByText(/lock you out/) !== null,
      }).toEqual({
        applies: expected.apply,
        explains: expected.apply,
        warns: expected.apply,
      });
    },
  );

  it.each([
    [[], "No firewall templates."],
    [
      ["manage:network"],
      "No firewall templates. Create one to define reusable rule sets.",
    ],
  ])("with %j install-wide, an empty list says %j", async (granted, says) => {
    grantPermissions(...granted);
    stubApi({ [TEMPLATES_PATH]: listOf([]) });
    renderWithProviders(<FirewallTemplatesTable clusterId="c1" />);

    const empty = await screen.findByText(/^No firewall templates\./);
    expect(empty.textContent).toBe(says);
  });
});

async function openDeleteFor(name: string) {
  api = stubApi({ [TEMPLATES_PATH]: listOf(TEMPLATES) });
  const user = userEvent.setup();
  renderWithProviders(<FirewallTemplatesTable clusterId="c1" />);
  await user.click(
    await screen.findByRole("button", { name: `Delete template ${name}` }),
  );
  return { user, dialog: await screen.findByRole("alertdialog") };
}

describe("FirewallTemplatesTable — deleting a template", () => {
  it("asks first, naming the template, and sends nothing", async () => {
    const { dialog } = await openDeleteFor("db");

    expect(
      within(dialog).getByRole("heading", {
        name: "Delete firewall template db?",
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      "Nexara deletes the template db and its 2 rules for every cluster. Rules already applied from it are ordinary Proxmox firewall rules and stay in place.",
    );
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const { user, dialog } = await openDeleteFor("db");

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming sends one DELETE for that template", async () => {
    const { user, dialog } = await openDeleteFor("db");

    await user.click(
      within(dialog).getByRole("button", { name: "Delete template" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${TEMPLATES_PATH}/${DB_ID}`]);
    });
  });
});

describe("FirewallTemplatesTable — applying a template", () => {
  it("says where the rules land, warns of the DROP, and reports how many Proxmox took", async () => {
    const APPLY = `/api/v1/clusters/c1/firewall-templates/${DB_ID}/apply`;
    api = stubApi(
      { [TEMPLATES_PATH]: listOf(TEMPLATES) },
      { [`POST ${APPLY}`]: { status: "ok", applied: 2, total: 2 } },
    );
    const user = setupUser();
    renderWithProviders(<FirewallTemplatesTable clusterId="c1" />);
    await user.click(
      await screen.findByRole("button", { name: "Apply template db" }),
    );

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent(
      "Adds 2 rules at the top of the cluster's firewall rules, above every rule already there, in the order below.",
    );
    expect(dialog).toHaveTextContent("This can lock you out of the cluster");
    expect(api.writes()).toEqual([]);

    await user.click(
      within(dialog).getByRole("button", { name: "Apply to cluster" }),
    );
    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith(
        "Added 2 rules from db at the top of the cluster's firewall rules.",
      );
    });
    expect(api.writes()).toEqual([`POST ${APPLY}`]);
  });
});
