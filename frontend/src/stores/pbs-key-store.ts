import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";

/** A PBS client encryption key Proxmox generated, waiting to be saved. */
export interface GeneratedPBSKey {
  /** Tells two queued keys apart, even for the same storage. */
  id: number;
  /**
   * The id of the Nexara user whose save generated the key, the one user it
   * is ever shown to. Taken when the save was sent, not when the answer came:
   * the session can end, or pass to someone else, in between.
   */
  owner: string;
  /**
   * The Nexara cluster the storage is on. A storage id is unique only within
   * one cluster: store01 on one cluster and store01 on another are two
   * storages, each with a key of its own.
   */
  cluster: string;
  /**
   * The cluster's name, from the cluster list already loaded when the save
   * was sent — or null when it was not there.
   */
  clusterName: string | null;
  /** The Proxmox storage id the key belongs to. */
  storage: string;
  /**
   * The key file as the response carried it — or "" when the request asked
   * for one and the response did not carry it.
   */
  keyText: string;
}

interface PBSKeyState {
  /** Keys waiting for the operator to confirm they saved them, oldest first. */
  pending: GeneratedPBSKey[];
  /** Queues a key for the must-save dialog. */
  deliver: (key: Omit<GeneratedPBSKey, "id">) => void;
  /** Drops a key, once the operator has confirmed saving it. */
  saved: (id: number) => void;
}

type AuthSnapshot = Pick<
  ReturnType<typeof useAuthStore.getState>,
  "isAuthenticated" | "user"
>;

/** The id of the user signed in, or undefined while no one is. */
export function signedInUserID(
  auth: AuthSnapshot = useAuthStore.getState(),
): string | undefined {
  return auth.isAuthenticated ? auth.user?.id : undefined;
}

let nextID = 1;

/**
 * Generated PBS encryption keys, from the response that carried one until the
 * operator presses Done in the must-save dialog (PendingPBSKeyDialog).
 *
 * The key lived in the storage dialog's own state once, and so died with the
 * page: Back, a search result, or a session-expiry redirect removed it before
 * the operator had saved it — and Nexara keeps no other copy. Here it outlives
 * any route change. It is filled from the storage mutations' own onSuccess
 * (storage-queries.ts), which TanStack runs even once the dialog that started
 * the save has closed.
 *
 * A key is shown only to the user whose save generated it. While no one is
 * signed in — on the login page once a session has expired — it is kept but
 * not shown, and it is shown again when its owner signs back in. When anyone
 * else signs in it is dropped, not merely hidden: it is not theirs to see,
 * even in this page's memory, and Proxmox still has the current key at
 * /etc/pve/priv/storage/<storage>.enc. So while someone is signed in, every
 * key here is theirs: deliver refuses a key whose answer came into someone
 * else's session, and the subscription below drops the rest the moment
 * someone else signs in, synchronously, before anything renders.
 *
 * Memory only, and deliberately: no persist middleware, nothing written to
 * localStorage or sessionStorage. A reload does lose a key that is still
 * waiting, which is why PendingPBSKeyDialog asks the browser to confirm one.
 * A queue rather than a single slot, so a second key generated before the
 * first is saved cannot overwrite it.
 */
export const usePBSKeyStore = create<PBSKeyState>()((set) => ({
  pending: [],
  deliver: (key) => {
    const user = signedInUserID();
    if (user !== undefined && user !== key.owner) return;
    set((s) => ({ pending: [...s.pending, { ...key, id: nextID++ }] }));
  },
  saved: (id) => {
    set((s) => ({ pending: s.pending.filter((k) => k.id !== id) }));
  },
}));

// Someone signed in: only the keys their own saves generated are kept (see
// above). Zustand calls this inside the auth store's own update, so the rest
// are gone before anything renders with the new user signed in.
useAuthStore.subscribe((auth) => {
  const user = signedInUserID(auth);
  if (user === undefined) return;
  const { pending } = usePBSKeyStore.getState();
  if (pending.every((k) => k.owner === user)) return;
  usePBSKeyStore.setState({
    pending: pending.filter((k) => k.owner === user),
  });
});
