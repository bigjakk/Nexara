import { useCallback, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { ClusterResponse } from "@/types/api";

/**
 * What a save of one cluster's access model is called once it can land
 * anywhere, a toast: "Deleting user alice@pve in cluster01". An install can
 * hold several clusters with users of the same names, so the object alone does
 * not say which one changed. The name is the one the cluster page has read
 * (useCluster's entry, never fetched from here); the bare text without it.
 */
export function useInCluster(clusterId: string): (text: string) => string {
  const { t } = useTranslation("access");
  const name = useQuery({
    queryKey: ["clusters", clusterId],
    queryFn: () =>
      apiClient.get<ClusterResponse>(apiPath`/api/v1/clusters/${clusterId}`),
    enabled: false,
  }).data?.name;
  return useCallback(
    (text: string) => (name ? t("inCluster", { text, name }) : text),
    [name, t],
  );
}

/**
 * `send`, but refused (null) while the request it last sent is still out. A
 * save's button is held by its pending flag only once that flag has rendered,
 * a macrotask after the click or the Enter, so a second one in between sent
 * the same save twice.
 */
export function useSingleFlight<V, T>(
  send: (variables: V) => Promise<T>,
): (variables: V) => Promise<T> | null {
  const out = useRef(false);
  return useCallback(
    (variables: V) => {
      if (out.current) return null;
      out.current = true;
      const sent = send(variables);
      // Cleared before the caller hears the answer, so an override it opens
      // on a refusal can send at once.
      sent.then(
        () => {
          out.current = false;
        },
        () => {
          out.current = false;
        },
      );
      return sent;
    },
    [send],
  );
}
