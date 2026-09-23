import { beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";
import type { ReactNode } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { usePBSKeyStore } from "@/stores/pbs-key-store";
import type { User } from "@/types/api";
import { EditStorageDialog } from "./EditStorageDialog";
import { PendingPBSKeyDialog } from "./PendingPBSKey";
import type {
  StorageConfigResponse,
  StorageWriteResponse,
} from "../types/storage";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), list: vi.fn(), put: vi.fn(), post: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPut = vi.mocked(apiClient.put);

// Synthetic fingerprint, and a synthetic key file whose data member is a marker.
const FINGERPRINT =
  "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99";
const GENERATED_KEY =
  '{"kdf":null,"data":"CANARY-generated-key-material","fingerprint":"aa:bb:cc:dd:ee:ff:00:11"}';
const PASTED_KEY =
  '{"kdf":null,"data":"CANARY-pasted-key-material","fingerprint":"11:22:33:44:55:66:77:88"}';

const UPDATE_URL = "/api/v1/clusters/c1/storage/s1";

const OPERATOR: User = {
  id: "user-operator",
  email: "operator@example.com",
  display_name: "Operator",
  role: "user",
};
const SOMEONE_ELSE: User = {
  id: "user-other",
  email: "other@example.com",
  display_name: "Other",
  role: "user",
};

function signIn(user: User | null) {
  act(() => {
    useAuthStore.setState({
      user,
      permissions: [],
      isAuthenticated: user !== null,
    });
  });
}

/** Holds the next PUT until the returned function answers it. */
function holdTheAnswer(): (response: StorageWriteResponse) => void {
  let answer: (response: StorageWriteResponse) => void = () => undefined;
  mockedPut.mockImplementation(
    () =>
      new Promise<StorageWriteResponse>((resolve) => {
        answer = resolve;
      }),
  );
  return (response) => {
    answer(response);
  };
}

const GENERATED_ANSWER: StorageWriteResponse = {
  status: "updated",
  storage: "store01",
  generated_encryption_key: GENERATED_KEY,
};

function pbsConfig(
  over: Partial<StorageConfigResponse> = {},
): StorageConfigResponse {
  return {
    storage: "store01",
    type: "pbs",
    content: "backup",
    server: "pbs.example.com",
    datastore: "datastore01",
    username: "backup@pbs",
    fingerprint: "AA:BB:CC:00:11:22",
    ...over,
  };
}

/**
 * Opens the editor beside the app-wide must-save dialog, as the app has them:
 * a generated key reaches that dialog through usePBSKeyStore, not through the
 * editor. The QueryClient is returned so a test can look inside its caches.
 */
async function openEditor(config: StorageConfigResponse, storageType = "pbs") {
  mockedGet.mockResolvedValue(config);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  );
  const user = userEvent.setup();
  render(
    <>
      <EditStorageDialog
        clusterId="c1"
        storageId="s1"
        storageName="store01"
        storageType={storageType}
      />
      <PendingPBSKeyDialog />
    </>,
    { wrapper },
  );
  await user.click(screen.getByRole("button", { name: "Edit" }));
  await screen.findByRole("button", { name: "Save Changes" });
  return { user, queryClient };
}

/** Whether any mutation in the cache still holds text in its request or answer. */
function mutationCacheHolds(queryClient: QueryClient, text: string): boolean {
  return queryClient
    .getMutationCache()
    .getAll()
    .some((m) =>
      JSON.stringify([m.state.variables, m.state.data]).includes(text),
    );
}

/**
 * Everything written to the console from here to the end of the test, as one
 * string to search. An object is searched as its JSON and an Error by its
 * message and stack, so a key inside either counts.
 */
function watchConsole(): () => string {
  const spies = [
    vi.spyOn(console, "log"),
    vi.spyOn(console, "info"),
    vi.spyOn(console, "warn"),
    vi.spyOn(console, "error"),
    vi.spyOn(console, "debug"),
  ];
  onTestFinished(() => {
    for (const spy of spies) spy.mockRestore();
  });
  const text = (value: unknown): string => {
    if (value instanceof Error) return `${value.message}\n${value.stack ?? ""}`;
    if (typeof value !== "object" || value === null) return String(value);
    try {
      return JSON.stringify(value);
    } catch {
      return ""; // a cycle, which JSON cannot write
    }
  };
  return () =>
    spies.flatMap((spy) => spy.mock.calls.flat().map(text)).join("\n");
}

/** The body of the one PUT the dialog sent. */
function sentBody(): unknown {
  expect(mockedPut).toHaveBeenCalledTimes(1);
  const call = mockedPut.mock.calls[0];
  if (!call) throw new Error("no PUT was sent");
  expect(call[0]).toBe(UPDATE_URL);
  return call[1];
}

describe("EditStorageDialog — PBS encryption", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    usePBSKeyStore.setState({ pending: [] });
    signIn(OPERATOR);
    mockedList.mockResolvedValue([]);
    mockedPut.mockResolvedValue({ status: "updated", storage: "store01" });
  });

  it("shows an existing key's fingerprint read-only, with no editable key field", async () => {
    await openEditor(pbsConfig({ "encryption-key": FINGERPRINT }));

    expect(
      screen.getByText(/Encryption enabled — fingerprint/),
    ).toBeInTheDocument();
    const shown = screen.getByText("AA:BB:CC:DD:EE:FF:00:11");
    expect(shown).toHaveAttribute("title", FINGERPRINT);
    // Nothing editable holds it, and no key field exists until one is asked for.
    expect(screen.queryByDisplayValue(FINGERPRINT)).toBeNull();
    expect(screen.queryByLabelText("Key")).toBeNull();
    expect(
      screen.getByRole("radio", { name: "Keep the current key" }),
    ).toBeChecked();
  });

  it("shows a key recorded without a fingerprint as enabled", async () => {
    await openEditor(pbsConfig({ "encryption-key": "1" }));

    expect(screen.getByText("Encryption enabled")).toBeInTheDocument();
    expect(screen.queryByText(/fingerprint/)).toBeNull();
  });

  it("Keep sends nothing about the key", async () => {
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByLabelText("Enable"));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({ params: { disable: "1" } });
  });

  it("Remove sends the delete, after warning that old backups need the current key", async () => {
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByRole("radio", { name: "Remove the key" }));
    expect(
      screen.getByText(/can only be restored with\s+the current key/),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({ params: {}, delete: "encryption-key" });
    // Wait for onSuccess to have run — the form closes there — before
    // asserting it opened no save dialog.
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Save Changes" })).toBeNull();
    });
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("Cancel forgets an abandoned choice, so reopening starts from Keep", async () => {
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByRole("radio", { name: "Remove the key" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await screen.findByRole("button", { name: "Save Changes" });

    expect(
      screen.getByRole("radio", { name: "Keep the current key" }),
    ).toBeChecked();
    expect(
      screen.queryByText(/can only be restored with\s+the current key/),
    ).toBeNull();
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("hands over the key even when the editor was closed before the answer came", async () => {
    let answer: (response: StorageWriteResponse) => void = () => undefined;
    mockedPut.mockImplementation(
      () =>
        new Promise<StorageWriteResponse>((resolve) => {
          answer = resolve;
        }),
    );
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });

    // Closing resets the mutation the editor observes, so a key delivered by
    // the mutate call's own callback would be lost here; the hook's callback
    // runs from the mutation itself.
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();

    answer({
      status: "updated",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });

    await screen.findByRole("alertdialog");
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
  });

  it("Cancel closes the editor while the save runs, and the key still arrives", async () => {
    const answer = holdTheAnswer();
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );
    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });

    // As Escape and the close button do.
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();

    answer(GENERATED_ANSWER);
    await screen.findByRole("alertdialog");
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
  });

  it("keeps a key answered after the session ended for the user who saved, and shows it when they sign back in", async () => {
    const answer = holdTheAnswer();
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );
    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });

    signIn(null);
    answer(GENERATED_ANSWER);
    await waitFor(() => {
      expect(usePBSKeyStore.getState().pending).toHaveLength(1);
    });
    expect(screen.queryByRole("alertdialog")).toBeNull();

    signIn(OPERATOR);
    expect(await screen.findByLabelText("Encryption key")).toHaveValue(
      GENERATED_KEY,
    );
  });

  it("shows no one a key answered after someone else signed in", async () => {
    const answer = holdTheAnswer();
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );
    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });

    signIn(SOMEONE_ELSE);
    answer(GENERATED_ANSWER);
    // The editor closes once the answer has been handled, key and all.
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Save Changes" })).toBeNull();
    });

    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(usePBSKeyStore.getState().pending).toEqual([]);
  });

  it("shows no one a key from a save sent with no one signed in", async () => {
    signIn(null);
    mockedPut.mockResolvedValue(GENERATED_ANSWER);
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );
    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Save Changes" })).toBeNull();
    });
    expect(usePBSKeyStore.getState().pending).toEqual([]);

    signIn(OPERATOR);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("leaves no key in the mutation cache once the save is done", async () => {
    mockedPut.mockResolvedValue({
      status: "updated",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });
    const { user, queryClient } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );
    // The positive control: the key really did pass through the cache, so
    // its absence afterwards is not merely a cache that never saw it.
    let cacheSawKey = false;
    const unsubscribe = queryClient.getMutationCache().subscribe(() => {
      if (mutationCacheHolds(queryClient, "CANARY-generated-key-material")) {
        cacheSawKey = true;
      }
    });

    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await screen.findByRole("alertdialog");

    await waitFor(() => {
      expect(
        mutationCacheHolds(queryClient, "CANARY-generated-key-material"),
      ).toBe(false);
    });
    unsubscribe();
    expect(cacheSawKey).toBe(true);
    // The must-save dialog still holds it, from usePBSKeyStore.
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
  });

  it("never writes a generated key to the console, from the answer to Done", async () => {
    const consoleText = watchConsole();
    mockedPut.mockResolvedValue({
      status: "updated",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await screen.findByRole("alertdialog");
    await user.click(screen.getByLabelText("I have saved this key"));
    await user.click(screen.getByRole("button", { name: "Done" }));

    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(consoleText()).not.toContain("CANARY-generated-key-material");
  });

  it("Replace with an auto-generated key sends autogen and shows the key the response carried", async () => {
    mockedPut.mockResolvedValue({
      status: "updated",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    expect(
      screen.getByRole("radio", { name: "Auto-generate a new key" }),
    ).toBeChecked();
    expect(
      screen.getByText(/can only be restored with\s+the current key/),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(sentBody()).toEqual({ params: { "encryption-key": "autogen" } });
    // No cluster list loaded here, so the cluster is named by its ID.
    expect(dialog).toHaveTextContent("for store01 on the cluster with ID c1.");
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
  });

  it("files the key under the storage's cluster, named from the cluster list", async () => {
    mockedPut.mockResolvedValue(GENERATED_ANSWER);
    const { user, queryClient } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );
    // As AppShell keeps it loaded — its useClusters() holds the query, which
    // gcTime 0 would otherwise drop at once: another cluster first, then this
    // one.
    queryClient.setQueryDefaults(["clusters"], { gcTime: Infinity });
    queryClient.setQueryData(
      ["clusters"],
      [
        { id: "c2", name: "cluster02" },
        { id: "c1", name: "cluster01" },
      ],
    );

    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    expect(await screen.findByRole("alertdialog")).toHaveTextContent(
      "for store01 on cluster cluster01.",
    );
    expect(usePBSKeyStore.getState().pending).toEqual([
      expect.objectContaining({
        cluster: "c1",
        clusterName: "cluster01",
        storage: "store01",
      }),
    ]);
  });

  it("says where the key is when an auto-generate response carries none", async () => {
    const { user } = await openEditor(pbsConfig());

    await user.click(
      screen.getByRole("radio", { name: "Auto-generate a key" }),
    );
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await screen.findByRole("alertdialog");
    expect(
      screen.getByText("/etc/pve/priv/storage/store01.enc"),
    ).toBeInTheDocument();
  });

  it("Replace with a pasted key sends that JSON and shows no save dialog", async () => {
    // Proxmox echoes a supplied key back; the dialog must not mistake the
    // echo for a generated key.
    mockedPut.mockResolvedValue({
      status: "updated",
      storage: "store01",
      generated_encryption_key: PASTED_KEY,
    });
    const { user } = await openEditor(
      pbsConfig({ "encryption-key": FINGERPRINT }),
    );

    await user.click(screen.getByRole("radio", { name: "Replace the key" }));
    await user.click(
      screen.getByRole("radio", { name: "Use an existing key" }),
    );
    await user.click(screen.getByLabelText("Key"));
    await user.paste(PASTED_KEY);
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({ params: { "encryption-key": PASTED_KEY } });
    // Give a save dialog every chance to appear before asserting it did not.
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Save Changes" })).toBeNull();
    });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(usePBSKeyStore.getState().pending).toEqual([]);
  });

  it("offers no saved login for the storage's password", async () => {
    await openEditor(pbsConfig());

    expect(screen.getByLabelText(/^Password/)).toHaveAttribute(
      "autocomplete",
      "new-password",
    );
    // Only the credential: the username is an ordinary field.
    expect(screen.getByLabelText(/^Username/)).not.toHaveAttribute(
      "autocomplete",
    );
  });

  it("loads a key from a file", async () => {
    const { user } = await openEditor(pbsConfig());

    await user.click(
      screen.getByRole("radio", { name: "Use an existing key" }),
    );
    await user.upload(
      screen.getByLabelText("Key file"),
      new File([PASTED_KEY], "store01.enc", { type: "application/json" }),
    );

    await waitFor(() => {
      expect(screen.getByLabelText("Key")).toHaveValue(PASTED_KEY);
    });
  });

  it("refuses a pasted text that is not a key", async () => {
    const { user } = await openEditor(pbsConfig());

    await user.click(
      screen.getByRole("radio", { name: "Use an existing key" }),
    );
    await user.click(screen.getByLabelText("Key"));
    await user.paste("autogen");

    expect(screen.getByText(/This is not JSON/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save Changes" })).toBeDisabled();
  });
});

describe("EditStorageDialog — Ceph keyring", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    usePBSKeyStore.setState({ pending: [] });
    signIn(OPERATOR);
    mockedList.mockResolvedValue([]);
    mockedPut.mockResolvedValue({ status: "updated", storage: "store01" });
  });

  const rbdConfig: StorageConfigResponse = {
    storage: "store01",
    type: "rbd",
    content: "images",
    monhost: "192.0.2.11,192.0.2.12",
    pool: "rbd",
    username: "admin",
    // The read never carries it; if it ever did, the dialog must not show it.
    keyring: "STORED-KEYRING-MUST-NOT-BE-SHOWN",
  };

  it("sends the keyring's contents, and starts empty", async () => {
    const { user } = await openEditor(rbdConfig, "rbd");

    const keyring = screen.getByLabelText(/^Keyring/);
    expect(keyring).toHaveValue("");
    // Proxmox already holds this external cluster's keyring, so leaving the
    // field empty is a valid edit.
    expect(screen.getByRole("button", { name: "Save Changes" })).toBeEnabled();
    await user.click(keyring);
    await user.paste("[client.admin]\n\tkey = CANARY-keyring-contents\n");
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      params: { keyring: "[client.admin]\n\tkey = CANARY-keyring-contents\n" },
    });
  });

  it("needs a keyring to point the cluster's own Ceph at another cluster", async () => {
    const ownCeph: StorageConfigResponse = { ...rbdConfig };
    delete ownCeph.monhost;
    const { user } = await openEditor(ownCeph, "rbd");

    await user.type(screen.getByLabelText("Monitor Hosts"), "192.0.2.11");
    // The keyring Proxmox has is this cluster's own admin keyring, which is no
    // credential for another cluster.
    const keyring = screen.getByLabelText(/^Keyring/);
    expect(screen.getByRole("button", { name: "Save Changes" })).toBeDisabled();
    await user.click(keyring);
    await user.paste("[client.admin]\n\tkey = CANARY-keyring-contents\n");
    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      params: {
        monhost: "192.0.2.11",
        keyring: "[client.admin]\n\tkey = CANARY-keyring-contents\n",
      },
    });
  });

  it("offers no keyring for the cluster's own Ceph", async () => {
    const ownCeph: StorageConfigResponse = { ...rbdConfig };
    delete ownCeph.monhost;
    await openEditor(ownCeph, "rbd");

    expect(screen.queryByLabelText(/^Keyring/)).toBeNull();
  });
});
