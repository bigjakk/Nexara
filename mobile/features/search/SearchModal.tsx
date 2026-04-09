/**
 * Global search modal.
 *
 * Mounted once at the (app) layout level. Visible when the global
 * `useSearchStore` flag is true. Any header button anywhere in the app
 * can open it via `useSearchStore.getState().open()`.
 *
 * Mirrors the desktop Cmd+K command palette at
 * `frontend/src/components/layout/SearchBar.tsx`. Same backend endpoint
 * (`GET /api/v1/search?q=...`), same RBAC requirement (`view:cluster`),
 * same entity types: clusters, nodes, VMs, containers, storage pools.
 *
 * Layout: full-screen Modal with a top bar (close button + title), a
 * search TextInput (autofocused, debounced 250ms), and a scrollable
 * result list grouped by entity type. Each result row is tappable and
 * navigates to the corresponding detail screen, dismissing the modal.
 *
 * Navigation targets:
 *   - cluster        → /(app)/clusters/[id]
 *   - vm / ct        → /(app)/clusters/[id]/vms/[vmId]
 *   - node           → /(app)/clusters/[id]/nodes/[nodeId]
 *   - storage        → /(app)/clusters/[id]/storage/[storageId]
 */

import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Modal,
  ScrollView,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import {
  Boxes,
  HardDrive,
  Layers,
  Search as SearchIcon,
  Server,
  X as XIcon,
} from "lucide-react-native";

import { useGlobalSearch } from "@/features/api/search-queries";
import type { SearchResult, SearchResultType } from "@/features/api/types";
import { usePermissions } from "@/hooks/usePermissions";
import { useSearchStore } from "@/stores/search-store";

const DEBOUNCE_MS = 250;

interface Group {
  type: SearchResultType;
  label: string;
  results: SearchResult[];
}

const GROUP_ORDER: SearchResultType[] = [
  "cluster",
  "node",
  "vm",
  "ct",
  "storage",
];

const GROUP_LABELS: Record<SearchResultType, string> = {
  cluster: "Clusters",
  node: "Nodes",
  vm: "Virtual machines",
  ct: "Containers",
  storage: "Storage",
};

export function SearchModal() {
  const isOpen = useSearchStore((s) => s.isOpen);
  const close = useSearchStore((s) => s.close);
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { canView } = usePermissions();

  const [input, setInput] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const inputRef = useRef<TextInput | null>(null);

  // Debounce the input value at 250ms before passing it to the query hook.
  useEffect(() => {
    if (!isOpen) return;
    const t = setTimeout(() => setDebouncedQuery(input), DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
    };
  }, [input, isOpen]);

  // Reset state when the modal closes so the next open is fresh.
  useEffect(() => {
    if (!isOpen) {
      setInput("");
      setDebouncedQuery("");
    }
  }, [isOpen]);

  // RN Modal + autoFocus on TextInput is unreliable on Android. Force the
  // focus via a ref after the modal is shown.
  useEffect(() => {
    if (!isOpen) return;
    const t = setTimeout(() => {
      inputRef.current?.focus();
    }, 100);
    return () => {
      clearTimeout(t);
    };
  }, [isOpen]);

  const search = useGlobalSearch(debouncedQuery);

  const groups: Group[] = useMemo(() => {
    if (!search.data) return [];
    const byType = new Map<SearchResultType, SearchResult[]>();
    for (const r of search.data) {
      const list = byType.get(r.type) ?? [];
      list.push(r);
      byType.set(r.type, list);
    }
    return GROUP_ORDER.flatMap((type) => {
      const results = byType.get(type) ?? [];
      if (results.length === 0) return [];
      return [{ type, label: GROUP_LABELS[type], results }];
    });
  }, [search.data]);

  function handleSelect(r: SearchResult) {
    close();
    switch (r.type) {
      case "cluster":
        router.push({
          pathname: "/(app)/clusters/[id]",
          params: { id: r.id },
        });
        return;
      case "vm":
      case "ct":
        router.push({
          pathname: "/(app)/clusters/[id]/vms/[vmId]",
          params: {
            id: r.cluster_id,
            vmId: r.id,
            type: r.type === "ct" ? "lxc" : "qemu",
          },
        });
        return;
      case "node":
        router.push({
          pathname: "/(app)/clusters/[id]/nodes/[nodeId]",
          params: { id: r.cluster_id, nodeId: r.id },
        });
        return;
      case "storage":
        router.push({
          pathname: "/(app)/clusters/[id]/storage/[storageId]",
          params: { id: r.cluster_id, storageId: r.id },
        });
        return;
    }
  }

  // Defense in depth: hide the modal if the user can't view clusters.
  // The button that opens the modal is already gated on the same
  // permission, but a stale store flag could keep the modal open after
  // a permission change.
  if (!canView("cluster")) return null;

  const trimmed = debouncedQuery.trim();
  const showEmptyHint = trimmed.length < 2;
  const showLoading = search.isFetching && !showEmptyHint;
  const showError = search.isError && !showEmptyHint && !search.isFetching;
  const showNoResults =
    !showEmptyHint &&
    !showLoading &&
    !showError &&
    search.data !== undefined &&
    groups.length === 0;
  const showResults = !showEmptyHint && !showLoading && groups.length > 0;

  return (
    <Modal
      visible={isOpen}
      animationType="fade"
      transparent={false}
      onRequestClose={close}
    >
      <View
        style={{ paddingTop: insets.top, paddingBottom: insets.bottom }}
        className="flex-1 bg-background"
      >
        {/* Top bar */}
        <View className="flex-row items-center gap-2 border-b border-border bg-card px-3 py-2">
          <TouchableOpacity
            onPress={close}
            className="p-1"
            hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
          >
            <XIcon color="#fafafa" size={22} />
          </TouchableOpacity>
          <Text className="ml-1 flex-1 text-base font-semibold text-foreground">
            Search
          </Text>
        </View>

        {/* Search input */}
        <View className="flex-row items-center gap-2 border-b border-border bg-card px-4 py-3">
          <SearchIcon color="#71717a" size={18} />
          <TextInput
            ref={inputRef}
            value={input}
            onChangeText={setInput}
            placeholder="Search clusters, nodes, VMs…"
            placeholderTextColor="#71717a"
            autoCapitalize="none"
            autoCorrect={false}
            returnKeyType="search"
            className="flex-1 text-base text-foreground"
            // Strip the default 4px inner padding so it aligns with the icon
            style={{ padding: 0 }}
          />
          {input.length > 0 ? (
            <TouchableOpacity
              onPress={() => setInput("")}
              hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
            >
              <XIcon color="#71717a" size={16} />
            </TouchableOpacity>
          ) : null}
        </View>

        {/* Body */}
        <ScrollView
          className="flex-1"
          keyboardShouldPersistTaps="handled"
          contentContainerStyle={{ paddingVertical: 16 }}
        >
          {showEmptyHint ? (
            <View className="items-center px-6 py-12">
              <Text className="text-center text-sm text-muted-foreground">
                Type at least 2 characters to search clusters, nodes, VMs, and
                containers.
              </Text>
            </View>
          ) : null}

          {showLoading ? (
            <View className="items-center py-12">
              <ActivityIndicator color="#22c55e" />
            </View>
          ) : null}

          {showError ? (
            <View className="items-center px-6 py-12">
              <Text className="text-center text-sm text-destructive">
                {search.error instanceof Error
                  ? search.error.message
                  : "Search failed"}
              </Text>
            </View>
          ) : null}

          {showNoResults ? (
            <View className="items-center px-6 py-12">
              <Text className="text-center text-sm text-muted-foreground">
                No results for &quot;{trimmed}&quot;.
              </Text>
            </View>
          ) : null}

          {showResults
            ? groups.map((g) => (
                <View key={g.type} className="mb-6 px-4">
                  <Text className="mb-2 px-1 text-xs font-medium uppercase text-muted-foreground">
                    {g.label}
                  </Text>
                  <View className="overflow-hidden rounded-lg border border-border bg-card">
                    {g.results.map((r, idx) => {
                      const last = idx === g.results.length - 1;
                      return (
                        <TouchableOpacity
                          key={`${r.type}-${r.id}`}
                          onPress={() => handleSelect(r)}
                          className={`flex-row items-center gap-3 px-4 py-3 ${
                            last ? "" : "border-b border-border"
                          }`}
                        >
                          <ResultIcon type={r.type} />
                          <View className="flex-1">
                            <Text
                              className="text-sm font-medium text-foreground"
                              numberOfLines={1}
                            >
                              {r.name}
                            </Text>
                            <Text
                              className="text-[11px] text-muted-foreground"
                              numberOfLines={1}
                            >
                              {buildSubtext(r)}
                            </Text>
                          </View>
                        </TouchableOpacity>
                      );
                    })}
                  </View>
                </View>
              ))
            : null}
        </ScrollView>
      </View>
    </Modal>
  );
}

function ResultIcon({ type }: { type: SearchResultType }) {
  const color = "#71717a";
  const size = 16;
  switch (type) {
    case "cluster":
      return <Layers color={color} size={size} />;
    case "node":
      return <Server color={color} size={size} />;
    case "vm":
    case "ct":
      return <Boxes color={color} size={size} />;
    case "storage":
      return <HardDrive color={color} size={size} />;
  }
}

/**
 * Build the subtext line for a result. Includes the most relevant context
 * for the entity type.
 */
function buildSubtext(r: SearchResult): string {
  const parts: string[] = [];
  switch (r.type) {
    case "cluster":
      // Cluster name is already the main label; subtext can be a generic
      // type marker so the row isn't blank.
      parts.push("Cluster");
      break;
    case "node":
      parts.push(r.cluster_name);
      if (r.status) parts.push(r.status);
      break;
    case "vm":
    case "ct":
      if (typeof r.vmid === "number") {
        parts.push(`${r.type === "ct" ? "ct" : "vm"} ${String(r.vmid)}`);
      }
      if (r.node) parts.push(r.node);
      parts.push(r.cluster_name);
      if (r.status) parts.push(r.status);
      break;
    case "storage":
      if (r.node) parts.push(r.node);
      parts.push(r.cluster_name);
      if (r.status) parts.push(r.status);
      break;
  }
  return parts.join(" · ");
}
