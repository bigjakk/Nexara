import { useCallback, useState } from "react";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import {
  privateAddressWarningFromError,
  type PrivateAddressWarning as PrivateAddressDetails,
} from "@/lib/private-address";

export interface FingerprintResponse {
  fingerprint: string;
  self_signed: boolean;
}

/**
 * Fetches and holds the TLS certificate a host presents, so the operator can
 * confirm it before a credential is stored against that host.
 *
 * Shared by the add and edit dialogs. Edit needs it because a pinned
 * fingerprint belongs to one host: carrying it to a new address makes every
 * subsequent connection fail on a mismatch, with no way back out of the UI.
 * Changing the address therefore has to re-pin, exactly as adding does.
 */
export function useVeeamFingerprint() {
  const [fingerprint, setFingerprint] = useState<FingerprintResponse | null>(
    null,
  );
  // The URL this fingerprint was fetched for. Without it, editing the address
  // while a fetch is in flight leaves the confirm step showing host A's
  // fingerprint under host B's name — asking the operator to attest to a
  // certificate they are not looking at.
  const [fetchedFor, setFetchedFor] = useState<string | null>(null);
  const [accepted, setAccepted] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [privateWarning, setPrivateWarning] =
    useState<PrivateAddressDetails | null>(null);

  const fetchFor = useCallback(async (url: string, allowPrivate: boolean) => {
    setError(null);
    setPrivateWarning(null);
    setPending(true);
    // A pin from a previous attempt must not outlive a new one, whether this
    // fetch succeeds or fails.
    setFingerprint(null);
    setFetchedFor(null);
    try {
      const resp = await apiClient.post<FingerprintResponse>(
        apiPath`/api/v1/clusters/fetch-fingerprint`,
        { api_url: url, allow_private_address: allowPrivate },
      );
      setFingerprint(resp);
      setFetchedFor(url);
      // A CA-signed chain needs no human confirmation — there is nothing for
      // the operator to compare it against that the CA has not already
      // asserted.
      setAccepted(!resp.self_signed);
    } catch (err) {
      const warn = privateAddressWarningFromError(err);
      if (warn != null) {
        setPrivateWarning(warn);
      } else {
        setError(
          err instanceof Error
            ? err.message
            : "Failed to fetch the TLS certificate",
        );
      }
    } finally {
      setPending(false);
    }
  }, []);

  const reset = useCallback(() => {
    setFingerprint(null);
    setFetchedFor(null);
    setAccepted(false);
    setPending(false);
    setError(null);
    setPrivateWarning(null);
  }, []);

  const clearPrivateWarning = useCallback(() => {
    setPrivateWarning(null);
  }, []);

  /**
   * What to store for the confirmed certificate: the pin for a self-signed
   * cert, or an empty string for a CA-signed one. Empty is meaningful on an
   * update — it clears a stale pin left over from the previous address.
   */
  const pinToStore = useCallback(
    () => (fingerprint?.self_signed === true ? fingerprint.fingerprint : ""),
    [fingerprint],
  );

  /**
   * The confirmed certificate for `url`, or null when none has been fetched
   * for exactly that address. Callers pass the address currently in the form,
   * so a fingerprint can never be displayed against a URL it does not belong
   * to.
   */
  const fingerprintFor = useCallback(
    (url: string) => (fetchedFor === url ? fingerprint : null),
    [fetchedFor, fingerprint],
  );

  return {
    fingerprint,
    fingerprintFor,
    accepted,
    setAccepted,
    pending,
    error,
    privateWarning,
    clearPrivateWarning,
    fetchFor,
    reset,
    pinToStore,
  };
}
