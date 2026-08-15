import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { AlertRuleForm } from "./AlertRuleForm";
import { apiClient } from "@/lib/api-client";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), post: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedPost = vi.mocked(apiClient.post);

// The body every case shares, so each test asserts only what it varies.
const commonFields = {
  name: "My rule",
  description: undefined,
  severity: "warning",
  metric: "cpu_usage",
  operator: ">",
  threshold: 90,
  duration_seconds: 300,
  cluster_id: "cluster-1",
  cooldown_seconds: 3600,
  escalation_chain: undefined,
  message_template: undefined,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockedGet.mockImplementation((path: string) =>
    path.endsWith("/nodes")
      ? Promise.resolve([{ id: "node-1", name: "pve1" }] as never)
      : Promise.resolve([
          { id: "cluster-1", name: "Lab" },
          { id: "cluster-2", name: "Prod" },
        ] as never),
  );
  mockedPost.mockResolvedValue({});
});

async function openForm() {
  const user = userEvent.setup();
  renderWithProviders(<AlertRuleForm />);
  await user.click(screen.getByRole("button", { name: /Create Rule/i }));
  await user.type(screen.getByLabelText("Name"), "My rule");
  return user;
}

async function pickOption(
  user: ReturnType<typeof userEvent.setup>,
  selectName: string,
  optionName: string | RegExp,
) {
  await user.click(screen.getByRole("combobox", { name: selectName }));
  await user.click(await screen.findByRole("option", { name: optionName }));
}

function submitButton() {
  // The dialog trigger shares this name; the submit button is the one inside.
  return within(screen.getByRole("dialog")).getByRole("button", {
    name: "Create Rule",
  });
}

describe("AlertRuleForm", () => {
  it("creates a cluster-scoped rule once a cluster is chosen", async () => {
    const user = await openForm();
    expect(submitButton()).toBeDisabled();

    await pickOption(user, "Cluster", "Lab");
    expect(submitButton()).toBeEnabled();
    await user.click(submitButton());

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    // Exact body: the backend rejects a scope whose binding is missing, so a
    // silently dropped cluster_id would 400 in production, not here.
    expect(mockedPost).toHaveBeenCalledWith("/api/v1/alert-rules", {
      ...commonFields,
      scope_type: "cluster",
      node_id: undefined,
      vm_vmid: undefined,
    });
  });

  it("blocks submit until a VM scope has a valid VMID", async () => {
    const user = await openForm();
    await pickOption(user, "Cluster", "Lab");
    await pickOption(user, "Scope", "VM");

    // A VM-scoped rule with no VMID is one the engine can never evaluate.
    expect(submitButton()).toBeDisabled();

    // 0 is not a Proxmox VMID; the backend rejects it with a 400.
    await user.type(screen.getByLabelText("VMID"), "0");
    expect(submitButton()).toBeDisabled();

    await user.clear(screen.getByLabelText("VMID"));
    await user.type(screen.getByLabelText("VMID"), "101");
    expect(submitButton()).toBeEnabled();
    await user.click(submitButton());

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost).toHaveBeenCalledWith("/api/v1/alert-rules", {
      ...commonFields,
      scope_type: "vm",
      node_id: undefined,
      vm_vmid: 101,
    });
  });

  it("requires a node for node scope and sends node_id", async () => {
    const user = await openForm();
    await pickOption(user, "Cluster", "Lab");
    await pickOption(user, "Scope", "Node");
    expect(submitButton()).toBeDisabled();

    await pickOption(user, "Node", "pve1");
    expect(submitButton()).toBeEnabled();
    await user.click(submitButton());

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost).toHaveBeenCalledWith("/api/v1/alert-rules", {
      ...commonFields,
      scope_type: "node",
      node_id: "node-1",
      vm_vmid: undefined,
    });
  });

  it("drops the chosen node when the cluster changes", async () => {
    const user = await openForm();
    await pickOption(user, "Cluster", "Lab");
    await pickOption(user, "Scope", "Node");
    await pickOption(user, "Node", "pve1");
    expect(submitButton()).toBeEnabled();

    // Nodes belong to one cluster; carrying the old pick over would send a
    // pair the backend rejects as belonging to different clusters.
    await pickOption(user, "Cluster", "Prod");
    expect(submitButton()).toBeDisabled();
  });
});
