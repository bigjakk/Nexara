import { beforeEach, describe, expect, it, vi } from "vitest";
import { useState, type ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { usePBSKeyStore } from "@/stores/pbs-key-store";
import { AddStorageDialog } from "./AddStorageDialog";
import { PendingPBSKeyDialog } from "./PendingPBSKey";
import type { StorageWriteResponse } from "../types/storage";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), list: vi.fn(), put: vi.fn(), post: vi.fn() },
}));

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);

// Synthetic key files whose data members are markers.
const GENERATED_KEY =
  '{"kdf":null,"data":"CANARY-generated-key-material","fingerprint":"aa:bb:cc:dd:ee:ff:00:11"}';
const PASTED_KEY =
  '{"kdf":null,"data":"CANARY-pasted-key-material","fingerprint":"11:22:33:44:55:66:77:88"}';

const CREATE_URL = "/api/v1/clusters/c1/storage";

/** Picks a storage type from the Type select and names the storage. */
async function chooseType(
  user: ReturnType<typeof userEvent.setup>,
  typeLabel: string,
) {
  // The Type select is the only combobox showing "Directory" on open.
  const typeSelect = screen
    .getAllByRole("combobox")
    .find((el) => el.textContent.includes("Directory"));
  if (!typeSelect) throw new Error("no Type select");
  await user.click(typeSelect);
  await user.click(await screen.findByRole("option", { name: typeLabel }));
  await user.type(screen.getByLabelText("ID"), "store01");
}

/**
 * Opens the dialog, beside the app-wide must-save dialog as the app has them,
 * and picks a storage type from the Type select. The QueryClient is returned
 * so a test can look inside its caches.
 */
async function openAdd(typeLabel: string) {
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
      <AddStorageDialog clusterId="c1" />
      <PendingPBSKeyDialog />
    </>,
    { wrapper },
  );
  await user.click(screen.getByRole("button", { name: "Add Storage" }));
  await chooseType(user, typeLabel);
  return { user, queryClient };
}

async function openWithType(typeLabel: string) {
  return (await openAdd(typeLabel)).user;
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
 * The dialog as the storage tree's context menu drives it: controlled, and
 * reopened by setting open again — which, unlike its own trigger, does not
 * reset the form.
 */
function ControlledAddStorage() {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button
        type="button"
        onClick={() => {
          setOpen(true);
        }}
      >
        Reopen
      </button>
      <AddStorageDialog clusterId="c1" open={open} onOpenChange={setOpen} />
    </>
  );
}

/** Fills the fields a PBS storage requires. */
async function fillPBS(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/^Server/), "pbs.example.com");
  await user.type(screen.getByLabelText(/^Datastore/), "datastore01");
  await user.type(screen.getByLabelText(/^Username/), "backup@pbs");
  await user.type(screen.getByLabelText(/^Password/), "not-a-real-password");
}

const PBS_PARAMS = {
  server: "pbs.example.com",
  datastore: "datastore01",
  username: "backup@pbs",
  password: "not-a-real-password",
  content: "backup",
};

/** The body of the one POST the dialog sent. */
function sentBody(): unknown {
  expect(mockedPost).toHaveBeenCalledTimes(1);
  const call = mockedPost.mock.calls[0];
  if (!call) throw new Error("no POST was sent");
  expect(call[0]).toBe(CREATE_URL);
  return call[1];
}

describe("AddStorageDialog — PBS encryption", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    usePBSKeyStore.setState({ pending: [] });
    // A generated key is shown only to the user whose save generated it.
    useAuthStore.setState({
      user: {
        id: "user-operator",
        email: "operator@example.com",
        display_name: "Operator",
        role: "user",
      },
      permissions: [],
      isAuthenticated: true,
    });
    mockedList.mockResolvedValue([]);
    mockedPost.mockResolvedValue({ status: "created", storage: "store01" });
  });

  it("defaults to no encryption and sends no key", async () => {
    const user = await openWithType("Proxmox Backup Server");
    await fillPBS(user);

    expect(screen.getByRole("radio", { name: "Do not encrypt" })).toBeChecked();
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "pbs",
      params: PBS_PARAMS,
    });
  });

  it("auto-generate sends autogen and shows the must-save dialog with the key the response carried", async () => {
    mockedPost.mockResolvedValue({
      status: "created",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });
    const user = await openWithType("Proxmox Backup Server");
    await fillPBS(user);

    await user.click(
      screen.getByRole("radio", { name: "Auto-generate a key" }),
    );
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "pbs",
      params: { ...PBS_PARAMS, "encryption-key": "autogen" },
    });
    expect(dialog).toHaveTextContent("store01");
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
    expect(screen.getByRole("button", { name: "Done" })).toBeDisabled();
  });

  it("a pasted key is sent as it is and shows no must-save dialog", async () => {
    // Proxmox echoes a supplied key back; that is not a key to save.
    mockedPost.mockResolvedValue({
      status: "created",
      storage: "store01",
      generated_encryption_key: PASTED_KEY,
    });
    const user = await openWithType("Proxmox Backup Server");
    await fillPBS(user);

    await user.click(
      screen.getByRole("radio", { name: "Use an existing key" }),
    );
    expect(
      screen.getByRole("button", { name: "Create Storage" }),
    ).toBeDisabled();
    await user.click(screen.getByLabelText("Key"));
    await user.paste(PASTED_KEY);
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "pbs",
      params: { ...PBS_PARAMS, "encryption-key": PASTED_KEY },
    });
    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Create Storage" }),
      ).toBeNull();
    });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(usePBSKeyStore.getState().pending).toEqual([]);
  });

  it("leaves no pasted key in the mutation cache once the create is done", async () => {
    const { user, queryClient } = await openAdd("Proxmox Backup Server");
    await fillPBS(user);
    // The positive control: the key really did pass through the cache, so
    // its absence afterwards is not merely a cache that never saw it.
    let cacheSawKey = false;
    const unsubscribe = queryClient.getMutationCache().subscribe(() => {
      if (mutationCacheHolds(queryClient, "CANARY-pasted-key-material")) {
        cacheSawKey = true;
      }
    });

    await user.click(
      screen.getByRole("radio", { name: "Use an existing key" }),
    );
    await user.click(screen.getByLabelText("Key"));
    await user.paste(PASTED_KEY);
    await user.click(screen.getByRole("button", { name: "Create Storage" }));
    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Create Storage" }),
      ).toBeNull();
    });

    await waitFor(() => {
      expect(
        mutationCacheHolds(queryClient, "CANARY-pasted-key-material"),
      ).toBe(false);
    });
    unsubscribe();
    expect(cacheSawKey).toBe(true);
  });

  it("offers no saved login for the storage's password", async () => {
    await openWithType("Proxmox Backup Server");

    expect(screen.getByLabelText(/^Password/)).toHaveAttribute(
      "autocomplete",
      "new-password",
    );
    // Only the credential: the username is an ordinary field.
    expect(screen.getByLabelText(/^Username/)).not.toHaveAttribute(
      "autocomplete",
    );
  });

  it("hands over the key even when the dialog was closed before the answer came", async () => {
    let answer: (response: StorageWriteResponse) => void = () => undefined;
    mockedPost.mockImplementation(
      () =>
        new Promise<StorageWriteResponse>((resolve) => {
          answer = resolve;
        }),
    );
    const user = await openWithType("Proxmox Backup Server");
    await fillPBS(user);
    await user.click(
      screen.getByRole("radio", { name: "Auto-generate a key" }),
    );
    await user.click(screen.getByRole("button", { name: "Create Storage" }));
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });

    // Closing resets the mutation the dialog observes, so a key delivered by
    // the mutate call's own callback would be lost here; the hook's callback
    // runs from the mutation itself.
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();

    answer({
      status: "created",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });

    await screen.findByRole("alertdialog");
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
  });

  it("Cancel closes the dialog while the create runs, and the key still arrives", async () => {
    let answer: (response: StorageWriteResponse) => void = () => undefined;
    mockedPost.mockImplementation(
      () =>
        new Promise<StorageWriteResponse>((resolve) => {
          answer = resolve;
        }),
    );
    const user = await openWithType("Proxmox Backup Server");
    await fillPBS(user);
    await user.click(
      screen.getByRole("radio", { name: "Auto-generate a key" }),
    );
    await user.click(screen.getByRole("button", { name: "Create Storage" }));
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });

    // As Escape and the close button do.
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();

    answer({
      status: "created",
      storage: "store01",
      generated_encryption_key: GENERATED_KEY,
    });
    await screen.findByRole("alertdialog");
    expect(screen.getByLabelText("Encryption key")).toHaveValue(GENERATED_KEY);
  });

  it("drops a pasted key and a typed password when the dialog closes, even where reopening keeps the form", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ControlledAddStorage />);
    await chooseType(user, "Proxmox Backup Server");

    // Once closed with Escape, which goes through the dialog's onOpenChange,
    // and once with Cancel, which does not.
    for (const close of ["Escape", "Cancel"]) {
      await user.type(
        screen.getByLabelText(/^Password/),
        "not-a-real-password",
      );
      await user.click(
        screen.getByRole("radio", { name: "Use an existing key" }),
      );
      await user.click(screen.getByLabelText("Key"));
      await user.paste(PASTED_KEY);

      if (close === "Escape") await user.keyboard("{Escape}");
      else await user.click(screen.getByRole("button", { name: "Cancel" }));
      await user.click(screen.getByRole("button", { name: "Reopen" }));

      // The form itself came back — the storage id is still there — but the
      // key did not.
      expect(await screen.findByLabelText("ID")).toHaveValue("store01");
      expect(
        screen.getByRole("radio", { name: "Do not encrypt" }),
      ).toBeChecked();
      expect(screen.queryByLabelText("Key")).toBeNull();
      expect(screen.queryByDisplayValue(PASTED_KEY)).toBeNull();
      expect(screen.getByLabelText(/^Password/)).toHaveValue("");
    }
  });
});

describe("AddStorageDialog — Ceph keyring", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    usePBSKeyStore.setState({ pending: [] });
    // A generated key is shown only to the user whose save generated it.
    useAuthStore.setState({
      user: {
        id: "user-operator",
        email: "operator@example.com",
        display_name: "Operator",
        role: "user",
      },
      permissions: [],
      isAuthenticated: true,
    });
    mockedList.mockResolvedValue([]);
    mockedPost.mockResolvedValue({ status: "created", storage: "store01" });
  });

  it("asks for a keyring only for an external cluster, and sends its contents", async () => {
    const user = await openWithType("RBD (Ceph)");

    expect(screen.queryByLabelText(/^Keyring/)).toBeNull();
    await user.type(
      screen.getByLabelText("Monitor Hosts"),
      "192.0.2.11,192.0.2.12",
    );
    const keyring = screen.getByLabelText(/^Keyring/);
    expect(keyring.tagName).toBe("TEXTAREA");
    // Required once shown, as the Proxmox GUI has it.
    expect(
      screen.getByRole("button", { name: "Create Storage" }),
    ).toBeDisabled();
    await user.click(keyring);
    await user.paste("[client.admin]\n\tkey = CANARY-keyring-contents\n");
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "rbd",
      params: {
        monhost: "192.0.2.11,192.0.2.12",
        keyring: "[client.admin]\n\tkey = CANARY-keyring-contents\n",
        content: "images,rootdir",
      },
    });
  });

  it("asks CephFS for the bare secret key, and sends it as keyring", async () => {
    const user = await openWithType("CephFS");

    expect(screen.queryByLabelText(/^Secret Key/)).toBeNull();
    await user.type(screen.getByLabelText("Monitor Hosts"), "192.0.2.11");
    const secret = screen.getByLabelText(/^Secret Key/);
    expect(secret).toHaveAttribute("type", "password");
    // "off" is ignored on a password field; this keeps the saved Nexara
    // login out of it.
    expect(secret).toHaveAttribute("autocomplete", "new-password");
    expect(
      screen.getByRole("button", { name: "Create Storage" }),
    ).toBeDisabled();
    await user.type(secret, "CANARY-cephfs-secret");
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "cephfs",
      params: {
        monhost: "192.0.2.11",
        keyring: "CANARY-cephfs-secret",
        content: "images,rootdir,iso,vztmpl,backup,snippets",
      },
    });
  });

  it("does not send a keyring typed before Monitor Hosts was cleared", async () => {
    const user = await openWithType("RBD (Ceph)");

    await user.type(screen.getByLabelText("Monitor Hosts"), "192.0.2.11");
    await user.click(screen.getByLabelText(/^Keyring/));
    await user.paste("[client.admin]\n\tkey = CANARY-keyring-contents\n");
    await user.clear(screen.getByLabelText("Monitor Hosts"));
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalled();
    });
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "rbd",
      params: { monhost: "", content: "images,rootdir" },
    });
  });
});
