import { beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import type { QueryClient } from "@tanstack/react-query";
import { renderWithProviders } from "@/test/test-utils";
import { deferred } from "@/test/fake-server";
import { fill, setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { usePBSKeyStore } from "@/stores/pbs-key-store";
import { AddStorageDialog } from "./AddStorageDialog";
import { PendingPBSKeyDialog } from "./PendingPBSKey";
import type { StorageWriteResponse } from "../types/storage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// Not under test, and its injected stylesheet makes every getComputedStyle
// call (role queries, Radix Presence, user-event) match against its rules.
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);

// Synthetic key files whose data members are markers.
const GENERATED_KEY =
  '{"kdf":null,"data":"CANARY-generated-key-material","fingerprint":"aa:bb:cc:dd:ee:ff:00:11"}';
const PASTED_KEY =
  '{"kdf":null,"data":"CANARY-pasted-key-material","fingerprint":"11:22:33:44:55:66:77:88"}';

const CREATE_URL = "/api/v1/clusters/c1/storage";

let user: UserEvent;

/** Picks a storage type from the Type select and names the storage. */
async function chooseType(typeLabel: string) {
  // The Type select is the only combobox showing "Directory" on open.
  const typeSelect = screen
    .getAllByRole("combobox")
    .find((el) => el.textContent.includes("Directory"));
  if (!typeSelect) throw new Error("no Type select");
  await user.click(typeSelect);
  await user.click(await screen.findByRole("option", { name: typeLabel }));
  fill(screen.getByLabelText("ID"), "store01");
}

/**
 * Opens the dialog, beside the app-wide must-save dialog as the app has them,
 * and picks a storage type from the Type select. The QueryClient is returned
 * so a test can look inside its caches.
 */
async function openAdd(typeLabel: string) {
  const { queryClient } = renderWithProviders(
    <>
      <AddStorageDialog clusterId="c1" />
      <PendingPBSKeyDialog />
    </>,
  );
  await user.click(screen.getByRole("button", { name: "Add Storage" }));
  await chooseType(typeLabel);
  return queryClient;
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
function fillPBS() {
  fill(screen.getByLabelText(/^Server/), "pbs.example.com");
  fill(screen.getByLabelText(/^Datastore/), "datastore01");
  fill(screen.getByLabelText(/^Username/), "backup@pbs");
  fill(screen.getByLabelText(/^Password/), "not-a-real-password");
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

beforeEach(() => {
  user = setupUser();
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

describe("AddStorageDialog — PBS encryption", () => {
  it("defaults to no encryption and sends no key, and offers no saved login for the password", async () => {
    await openAdd("Proxmox Backup Server");
    // Only the storage's credential: the username is an ordinary field.
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute(
      "autocomplete",
      "new-password",
    );
    expect(screen.getByLabelText(/^Username/)).not.toHaveAttribute(
      "autocomplete",
    );
    fillPBS();

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
    await openAdd("Proxmox Backup Server");
    fillPBS();

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

  it("a pasted key is sent as it is, shows no must-save dialog, and leaves no copy in the mutation cache", async () => {
    // Proxmox echoes a supplied key back; that is not a key to save.
    mockedPost.mockResolvedValue({
      status: "created",
      storage: "store01",
      generated_encryption_key: PASTED_KEY,
    });
    const queryClient = await openAdd("Proxmox Backup Server");
    fillPBS();
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
    expect(
      screen.getByRole("button", { name: "Create Storage" }),
    ).toBeDisabled();
    fill(screen.getByLabelText("Key"), PASTED_KEY);
    await user.click(screen.getByRole("button", { name: "Create Storage" }));

    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Create Storage" }),
      ).toBeNull();
    });
    expect(sentBody()).toEqual({
      storage: "store01",
      type: "pbs",
      params: { ...PBS_PARAMS, "encryption-key": PASTED_KEY },
    });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(usePBSKeyStore.getState().pending).toEqual([]);
    await waitFor(() => {
      expect(
        mutationCacheHolds(queryClient, "CANARY-pasted-key-material"),
      ).toBe(false);
    });
    unsubscribe();
    expect(cacheSawKey).toBe(true);
  });

  // Closing resets the mutation the dialog observes, so a key delivered by the
  // mutate call's own callback would be lost here; the hook's callback runs
  // from the mutation itself.
  it.each([
    ["Escape", () => user.keyboard("{Escape}")],
    [
      "Cancel",
      () => user.click(screen.getByRole("button", { name: "Cancel" })),
    ],
  ])(
    "hands over the key even when the dialog was closed with %s before the answer came",
    async (_, close) => {
      const answer = deferred<StorageWriteResponse>();
      mockedPost.mockReturnValue(answer.promise);
      await openAdd("Proxmox Backup Server");
      fillPBS();
      await user.click(
        screen.getByRole("radio", { name: "Auto-generate a key" }),
      );
      await user.click(screen.getByRole("button", { name: "Create Storage" }));
      await waitFor(() => {
        expect(mockedPost).toHaveBeenCalled();
      });

      await close();
      expect(screen.queryByRole("dialog")).toBeNull();

      answer.resolve({
        status: "created",
        storage: "store01",
        generated_encryption_key: GENERATED_KEY,
      });

      await screen.findByRole("alertdialog");
      expect(screen.getByLabelText("Encryption key")).toHaveValue(
        GENERATED_KEY,
      );
    },
  );

  it("drops a pasted key and a typed password when the dialog closes, even where reopening keeps the form", async () => {
    renderWithProviders(<ControlledAddStorage />);
    await chooseType("Proxmox Backup Server");

    // Once closed with Escape, which goes through the dialog's onOpenChange,
    // and once with Cancel, which does not.
    for (const close of ["Escape", "Cancel"]) {
      fill(screen.getByLabelText(/^Password/), "not-a-real-password");
      await user.click(
        screen.getByRole("radio", { name: "Use an existing key" }),
      );
      fill(screen.getByLabelText("Key"), PASTED_KEY);

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
  it("asks for a keyring only for an external cluster, and sends its contents", async () => {
    await openAdd("RBD (Ceph)");

    expect(screen.queryByLabelText(/^Keyring/)).toBeNull();
    fill(screen.getByLabelText("Monitor Hosts"), "192.0.2.11,192.0.2.12");
    const keyring = screen.getByLabelText(/^Keyring/);
    expect(keyring.tagName).toBe("TEXTAREA");
    // Required once shown, as the Proxmox GUI has it.
    expect(
      screen.getByRole("button", { name: "Create Storage" }),
    ).toBeDisabled();
    fill(keyring, "[client.admin]\n\tkey = CANARY-keyring-contents\n");
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
    await openAdd("CephFS");

    expect(screen.queryByLabelText(/^Secret Key/)).toBeNull();
    fill(screen.getByLabelText("Monitor Hosts"), "192.0.2.11");
    const secret = screen.getByLabelText(/^Secret Key/);
    expect(secret).toHaveAttribute("type", "password");
    // "off" is ignored on a password field; this keeps the saved Nexara
    // login out of it.
    expect(secret).toHaveAttribute("autocomplete", "new-password");
    expect(
      screen.getByRole("button", { name: "Create Storage" }),
    ).toBeDisabled();
    fill(secret, "CANARY-cephfs-secret");
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
    await openAdd("RBD (Ceph)");

    fill(screen.getByLabelText("Monitor Hosts"), "192.0.2.11");
    fill(
      screen.getByLabelText(/^Keyring/),
      "[client.admin]\n\tkey = CANARY-keyring-contents\n",
    );
    fill(screen.getByLabelText("Monitor Hosts"), "");
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
