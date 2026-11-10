import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
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

afterEach(() => {
  vi.unstubAllGlobals();
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
      within(dialog).getByRole("button", { name: "Delete Template" }),
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
      within(dialog).getByRole("button", { name: "Apply to Cluster" }),
    );
    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith(
        "Added 2 rules from db at the top of the cluster's firewall rules.",
      );
    });
    expect(api.writes()).toEqual([`POST ${APPLY}`]);
  });
});
