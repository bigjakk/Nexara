import { useEffect } from "react";
import { useBlocker } from "react-router-dom";

import { useAuthStore } from "@/stores/auth-store";
import { signedInUserID, usePBSKeyStore } from "@/stores/pbs-key-store";
import { pbsKeyFileFingerprint } from "../lib/pbs-encryption";
import { PBSKeySaveDialog } from "./PBSKeySaveDialog";

/**
 * The one must-save dialog for PBS keys Proxmox generated (usePBSKeyStore),
 * shown one key at a time, oldest first.
 *
 * It is rendered beside the router rather than inside a page (AppRoot, in
 * components/AppRoot.tsx), so no route change can take it away: not Back,
 * not a search result, not the redirect to the login page when a session
 * expires, which unmounts the whole application shell.
 *
 * It is shown only while someone is signed in, and the store holds only that
 * user's keys then. On the login page, once a session has expired, it would
 * show the key to whoever sat down at the screen, and make them confirm
 * saving it before they could sign in; there the key waits, unseen, for its
 * owner to sign back in.
 *
 * While a key is waiting, shown or not, leaving the page asks the browser to
 * confirm: a reload or a closed tab would lose the key, and Nexara keeps no
 * other copy. The prompt is registered only while a key is waiting.
 */
export function PendingPBSKeyDialog() {
  const pending = usePBSKeyStore((s) => s.pending);
  const saved = usePBSKeyStore((s) => s.saved);
  const signedIn = useAuthStore((s) => signedInUserID(s) !== undefined);
  const waiting = pending.length > 0;

  useEffect(() => {
    if (!waiting) return;
    const confirmLeaving = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    window.addEventListener("beforeunload", confirmLeaving);
    return () => {
      window.removeEventListener("beforeunload", confirmLeaving);
    };
  }, [waiting]);

  const next = pending[0];
  if (!next || !signedIn) return null;

  // Generating a key for a storage sets the one before aside, so of the keys
  // queued for one storage only the last is the one Proxmox uses. One storage
  // is a storage id on one cluster: the same id on another cluster is another
  // storage, whose key replaces nothing here.
  const last = pending
    .filter((k) => k.cluster === next.cluster && k.storage === next.storage)
    .at(-1);
  const replacedBy =
    last && last.id !== next.id
      ? {
          fingerprint:
            pbsKeyFileFingerprint(last.keyText)?.shortFingerprint ?? null,
        }
      : undefined;

  // Keyed by the queued key, so the next one starts with its box unticked.
  return (
    <PBSKeySaveDialog
      key={next.id}
      storage={next.storage}
      cluster={{ id: next.cluster, name: next.clusterName }}
      keyText={next.keyText}
      replacedBy={replacedBy}
      onDone={() => {
        saved(next.id);
      }}
    />
  );
}

/**
 * Holds in-app navigation while a generated key is waiting to be saved. It
 * lives in AppShell because useBlocker needs the data router's context, which
 * PendingPBSKeyDialog deliberately sits outside of.
 *
 * The blocker exists only while a key is waiting. A router consults just one
 * blocker, the one registered last (shouldBlockNavigation in react-router's
 * router), so one held for the whole session would silently stand in for a
 * page's own — an unsaved-changes prompt, say — whenever it registered after
 * it, as it does when that page is the first one loaded.
 *
 * A blocked navigation simply does not happen: nothing ever proceeds it, and
 * react-router has already put Back's history entry back by the time it
 * reports the block. The operator stays on the page the dialog opened over,
 * and navigates again once the key is saved, when the blocker is gone.
 */
export function PendingPBSKeyNavigationGuard() {
  const waiting = usePBSKeyStore((s) => s.pending.length > 0);
  return waiting ? <HoldNavigation /> : null;
}

function HoldNavigation() {
  useBlocker(true);
  return null;
}
