/**
 * MMKV-backed TanStack Query persister. Cached query results survive cold
 * launches, so the app shows stale-but-instant data while a fresh fetch
 * runs in the background.
 *
 * Only non-sensitive server data lives here (dashboards, VM lists).
 * Auth tokens NEVER touch MMKV — they stay in SecureStore.
 */

import { MMKV } from "react-native-mmkv";
import type { PersistedClient, Persister } from "@tanstack/react-query-persist-client";

const queryStorage = new MMKV({ id: "nx.query-cache" });

const KEY = "nx.query-state";

export const mmkvQueryPersister: Persister = {
  persistClient: async (client: PersistedClient) => {
    queryStorage.set(KEY, JSON.stringify(client));
  },
  restoreClient: async () => {
    const raw = queryStorage.getString(KEY);
    if (!raw) return undefined;
    try {
      return JSON.parse(raw) as PersistedClient;
    } catch {
      queryStorage.delete(KEY);
      return undefined;
    }
  },
  removeClient: async () => {
    queryStorage.delete(KEY);
  },
};
