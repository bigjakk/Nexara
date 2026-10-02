import {
  describe,
  it,
  expect,
  beforeEach,
  afterEach,
  vi,
  type MockInstance,
} from "vitest";
import { useConsoleStore } from "./console-store";
import { apiClient } from "@/lib/api-client";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), list: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
// The node re-resolution reads a collection, so it goes through apiClient.list
// (which unwraps the {items,total} envelope), not apiClient.get. Stubbing it as
// `get` returning a bare array is how this suite kept passing against a shape
// the server had stopped sending.
const mockedList = vi.mocked(apiClient.list);

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
      mockedList.mockReset();
    });

    it("parks the tab as guest-stopped when the guest is powered off", async () => {
      const id = addVmTab();
      mockedGet.mockResolvedValueOnce({
        node_id: "node-uuid-1",
        status: "stopped",
      });

      await useConsoleStore.getState().resolveAndReconnect(id);

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.status).toBe("guest-stopped");
      expect(tab?.reconnectKey).toBe(0); // no reconnect attempt scheduled
      expect(mockedGet).toHaveBeenCalledTimes(1);
      expect(mockedList).not.toHaveBeenCalled(); // node lookup skipped
    });

    it("reconnects to the new node after a migration (guest running)", async () => {
      const id = addVmTab();
      mockedGet.mockResolvedValueOnce({
        node_id: "node-uuid-2",
        status: "running",
      });
      mockedList.mockResolvedValueOnce([
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
      mockedGet.mockResolvedValueOnce({
        node_id: "node-uuid-1",
        status: "running",
      });
      mockedList.mockResolvedValueOnce([{ id: "node-uuid-1", name: "n1" }]);

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

  describe("updateTabNode", () => {
    function addVmTabAt(node: string) {
      return useConsoleStore.getState().addTab({
        clusterID: "c1",
        node,
        vmid: 101,
        type: "vm_serial",
        label: "Serial: vm101",
        resourceId: "vm-uuid-1",
        kind: "vm",
      });
    }

    it("retargets a live tab and triggers a reconnect", () => {
      const id = addVmTabAt("n1");
      useConsoleStore.getState().updateTabStatus(id, "connected");

      useConsoleStore.getState().updateTabNode("c1", 101, "n2");

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.node).toBe("n2");
      expect(tab?.status).toBe("connecting");
      expect(tab?.reconnectKey).toBe(1);
    });

    it("retargets an idle tab without waking it", () => {
      // Restored-but-never-opened tabs must not dial a console just because
      // the guest migrated — that is what the lazy-activation gate prevents.
      const id = addVmTabAt("n1");
      useConsoleStore.getState().updateTabStatus(id, "idle");

      useConsoleStore.getState().updateTabNode("c1", 101, "n2");

      const tab = useConsoleStore.getState().tabs[0];
      expect(tab?.node).toBe("n2");
      expect(tab?.status).toBe("idle");
      expect(tab?.reconnectKey).toBe(0);
    });
  });

  describe("resetSession", () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    // zustand's persist writes the store to localStorage on EVERY set, changed or
    // not, with a window position derived from the viewport. A signed-out boot
    // has nothing to forget, so it must not write — least of all something that
    // can throw (a full quota) or that was never the user's choice.
    function consoleWrites(setItem: MockInstance<Storage["setItem"]>) {
      return setItem.mock.calls.filter(
        ([key]) => key === "nexara-console-tabs",
      );
    }

    it("writes nothing when the store is already at its defaults", () => {
      useConsoleStore.setState({
        tabs: [],
        activeTabId: null,
        windowMode: "hidden",
      });
      const setItem = vi.spyOn(Storage.prototype, "setItem");

      useConsoleStore.getState().resetSession();

      expect(consoleWrites(setItem)).toEqual([]);
    });

    it("control: writes the emptied store when a tab is open", () => {
      useConsoleStore.getState().addTab({
        clusterID: "cluster01",
        node: "pve-01",
        type: "node_shell",
        label: "pve-01 shell",
      });
      const setItem = vi.spyOn(Storage.prototype, "setItem");

      useConsoleStore.getState().resetSession();

      expect(consoleWrites(setItem)).toHaveLength(1);
      expect(useConsoleStore.getState().tabs).toEqual([]);
    });

    // Another tab persisted its own tabs after this one loaded: this tab's memory
    // never held them, so the in-memory check alone calls a reset a no-op, and
    // the next user's reload would bring them back, the active one dialling at
    // once with their token.
    const KEY = "nexara-console-tabs";

    interface Persisted {
      state: {
        tabs: unknown[];
        activeTabId: string | null;
        windowMode: string;
      };
    }

    function persistedByAnotherTab(left: Partial<Persisted["state"]> = {}) {
      localStorage.setItem(
        KEY,
        JSON.stringify({
          state: {
            tabs: [
              {
                id: "node_shell-pve-01-1",
                clusterID: "cluster01",
                node: "pve-01",
                type: "node_shell",
                label: "pve-01 shell",
                status: "idle",
                reconnectKey: 0,
              },
            ],
            activeTabId: "node_shell-pve-01-1",
            windowMode: "floating",
            windowPosition: { x: 0, y: 0 },
            windowSize: { width: 800, height: 500 },
            ...left,
          },
          version: 1,
        }),
      );
    }

    function persisted(): Persisted["state"] {
      return (JSON.parse(localStorage.getItem(KEY) ?? "null") as Persisted)
        .state;
    }

    function memoryAtItsDefaults() {
      useConsoleStore.setState({
        tabs: [],
        activeTabId: null,
        windowMode: "hidden",
      });
    }

    it("forgets what another tab persisted, though its own memory is at its defaults", () => {
      memoryAtItsDefaults();
      persistedByAnotherTab(); // after the setState above, which persists too

      useConsoleStore.getState().resetSession();

      expect(persisted()).toMatchObject({
        tabs: [],
        activeTabId: null,
        windowMode: "hidden",
      });
    });

    it.each([
      ["tabs", { activeTabId: null, windowMode: "hidden" }],
      ["an active tab", { tabs: [], windowMode: "hidden" }],
      ["the window shown", { tabs: [], activeTabId: null }],
    ])("forgets %s alone in what another tab persisted", (_what, left) => {
      memoryAtItsDefaults();
      persistedByAnotherTab(left);

      useConsoleStore.getState().resetSession();

      expect(persisted()).toMatchObject({
        tabs: [],
        activeTabId: null,
        windowMode: "hidden",
      });
    });

    it("forgets a persisted copy it cannot read, rather than leave it for the next user", () => {
      memoryAtItsDefaults();
      localStorage.setItem(KEY, "{ not json");

      useConsoleStore.getState().resetSession();

      expect(persisted()).toMatchObject({ tabs: [], activeTabId: null });
    });

    it("forgets what a storage that answers late may hold, as it cannot look", () => {
      memoryAtItsDefaults();
      const original = useConsoleStore.persist.getOptions().storage;
      const setItem = vi.fn();
      useConsoleStore.persist.setOptions({
        storage: {
          getItem: () => Promise.resolve(null),
          setItem,
          removeItem: vi.fn(),
        },
      });

      try {
        useConsoleStore.getState().resetSession();
      } finally {
        useConsoleStore.persist.setOptions({ storage: original });
      }

      expect(setItem).toHaveBeenCalledTimes(1);
    });

    it("writes nothing when nothing was ever persisted either", () => {
      memoryAtItsDefaults();
      localStorage.removeItem(KEY);
      const setItem = vi.spyOn(Storage.prototype, "setItem");

      useConsoleStore.getState().resetSession();

      expect(consoleWrites(setItem)).toEqual([]);
    });

    it("writes nothing when the persisted copy is at its defaults, as at a signed-out boot", () => {
      memoryAtItsDefaults();
      const setItem = vi.spyOn(Storage.prototype, "setItem");

      useConsoleStore.getState().resetSession();

      expect(persisted()).toMatchObject({ tabs: [], windowMode: "hidden" });
      expect(consoleWrites(setItem)).toEqual([]);
    });

    it.each([
      ["a tab", () => useConsoleStore.setState({ activeTabId: "t1" })],
      [
        "the window shown",
        () => useConsoleStore.setState({ windowMode: "floating" }),
      ],
    ])("is not a no-op when only %s is left", (_what, leave) => {
      useConsoleStore.setState({
        tabs: [],
        activeTabId: null,
        windowMode: "hidden",
      });
      leave();

      useConsoleStore.getState().resetSession();

      expect(useConsoleStore.getState().activeTabId).toBeNull();
      expect(useConsoleStore.getState().windowMode).toBe("hidden");
    });
  });
});
