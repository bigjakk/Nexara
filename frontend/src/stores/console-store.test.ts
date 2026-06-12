import { describe, it, expect, beforeEach, vi } from "vitest";
import { useConsoleStore } from "./console-store";
import { apiClient } from "@/lib/api-client";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);

describe("console-store", () => {
  beforeEach(() => {
    // Reset store state between tests.
    useConsoleStore.setState({ tabs: [], activeTabId: null });
  });

  it("adds a tab and sets it as active", () => {
    const id = useConsoleStore.getState().addTab({
      clusterID: "cluster-1",
      node: "node1",
      type: "node_shell",
      label: "node1 shell",
    });

    const state = useConsoleStore.getState();
    expect(state.tabs).toHaveLength(1);
    expect(state.tabs[0]?.id).toBe(id);
    expect(state.tabs[0]?.status).toBe("connecting");
    expect(state.tabs[0]?.label).toBe("node1 shell");
    expect(state.activeTabId).toBe(id);
  });

  it("adds multiple tabs and activates the last one", () => {
    const { addTab } = useConsoleStore.getState();
    addTab({
      clusterID: "c1",
      node: "n1",
      type: "node_shell",
      label: "shell 1",
    });
    const id2 = addTab({
      clusterID: "c1",
      node: "n2",
      type: "node_shell",
      label: "shell 2",
    });

    const state = useConsoleStore.getState();
    expect(state.tabs).toHaveLength(2);
    expect(state.activeTabId).toBe(id2);
  });

  it("removes a tab and updates active tab", () => {
    const { addTab } = useConsoleStore.getState();
    const id1 = addTab({
      clusterID: "c1",
      node: "n1",
      type: "node_shell",
      label: "shell 1",
    });
    const id2 = addTab({
      clusterID: "c1",
      node: "n2",
      type: "node_shell",
      label: "shell 2",
    });

    // Active is id2. Remove id2 → active should become id1.
    useConsoleStore.getState().removeTab(id2);
    const state = useConsoleStore.getState();
    expect(state.tabs).toHaveLength(1);
    expect(state.activeTabId).toBe(id1);
  });

  it("removes last tab and sets active to null", () => {
    const { addTab } = useConsoleStore.getState();
    const id = addTab({
      clusterID: "c1",
      node: "n1",
      type: "node_shell",
      label: "shell 1",
    });

    useConsoleStore.getState().removeTab(id);
    const state = useConsoleStore.getState();
    expect(state.tabs).toHaveLength(0);
    expect(state.activeTabId).toBeNull();
  });

  it("sets active tab", () => {
    const { addTab } = useConsoleStore.getState();
    const id1 = addTab({
      clusterID: "c1",
      node: "n1",
      type: "node_shell",
      label: "shell 1",
    });
    addTab({
      clusterID: "c1",
      node: "n2",
      type: "node_shell",
      label: "shell 2",
    });

    useConsoleStore.getState().setActiveTab(id1);
    expect(useConsoleStore.getState().activeTabId).toBe(id1);
  });

  it("updates tab status", () => {
    const { addTab } = useConsoleStore.getState();
    const id = addTab({
      clusterID: "c1",
      node: "n1",
      type: "node_shell",
      label: "shell 1",
    });

    useConsoleStore.getState().updateTabStatus(id, "connected");
    expect(useConsoleStore.getState().tabs[0]?.status).toBe("connected");

    useConsoleStore.getState().updateTabStatus(id, "error");
    expect(useConsoleStore.getState().tabs[0]?.status).toBe("error");
  });

  it("handles VM tab with vmid", () => {
    const id = useConsoleStore.getState().addTab({
      clusterID: "c1",
      node: "n1",
      vmid: 100,
      type: "vm_serial",
      label: "VM 100",
    });

    const tab = useConsoleStore.getState().tabs[0];
    expect(tab?.vmid).toBe(100);
    expect(tab?.type).toBe("vm_serial");
    expect(tab?.id).toBe(id);
  });

  describe("resolveAndReconnect", () => {
    function addVmTab() {
      return useConsoleStore.getState().addTab({
        clusterID: "c1",
        node: "n1",
        vmid: 100,
        type: "vm_vnc",
        label: "VNC: test-vm",
        resourceId: "vm-uuid-1",
        kind: "vm",
      });
    }

    beforeEach(() => {
      mockedGet.mockReset();
    });

    it("parks the tab as guest-stopped when the guest is powered off", async () => {
      const id = addVmTab();
      mockedGet.mockResolvedValueOnce({ node_id: "node-uuid-1", status: "stopped" });

      await useConsoleStore.getState().resolveAndReconnect(id);

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.status).toBe("guest-stopped");
      expect(tab?.reconnectKey).toBe(0); // no reconnect attempt scheduled
      expect(mockedGet).toHaveBeenCalledTimes(1); // node lookup skipped
    });

    it("reconnects to the new node after a migration (guest running)", async () => {
      const id = addVmTab();
      mockedGet
        .mockResolvedValueOnce({ node_id: "node-uuid-2", status: "running" })
        .mockResolvedValueOnce([
          { id: "node-uuid-1", name: "n1" },
          { id: "node-uuid-2", name: "n2" },
        ]);

      await useConsoleStore.getState().resolveAndReconnect(id);

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.node).toBe("n2");
      expect(tab?.status).toBe("connecting");
      expect(tab?.reconnectKey).toBe(1);
    });

    it("reconnects on the same node when the guest is running and unmoved", async () => {
      const id = addVmTab();
      mockedGet
        .mockResolvedValueOnce({ node_id: "node-uuid-1", status: "running" })
        .mockResolvedValueOnce([{ id: "node-uuid-1", name: "n1" }]);

      await useConsoleStore.getState().resolveAndReconnect(id);

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.node).toBe("n1");
      expect(tab?.status).toBe("connecting");
      expect(tab?.reconnectKey).toBe(1);
    });

    it("falls back to a plain reconnect when the status lookup fails", async () => {
      const id = addVmTab();
      mockedGet.mockRejectedValueOnce(new Error("network down"));

      await useConsoleStore.getState().resolveAndReconnect(id);

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.status).toBe("connecting");
      expect(tab?.reconnectKey).toBe(1);
    });
  });
});
