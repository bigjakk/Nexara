/**
 * Search icon button. Opens the global SearchModal via the Zustand
 * store. Hidden when the user lacks `view:cluster` (the same RBAC
 * required by the backend `/api/v1/search` endpoint).
 *
 * Used in two places:
 *   - The custom `StackHeader` component (clusters / alerts screens)
 *   - The Tabs navigator's `headerRight` (Dashboard / Activity / Settings)
 *
 * Both surfaces render the same button so the search icon is visible
 * from every screen in the (app) group.
 */

import { TouchableOpacity } from "react-native";
import { Search as SearchIcon } from "lucide-react-native";

import { usePermissions } from "@/hooks/usePermissions";
import { useSearchStore } from "@/stores/search-store";

export function SearchButton() {
  const open = useSearchStore((s) => s.open);
  const { canView } = usePermissions();

  if (!canView("cluster")) return null;

  return (
    <TouchableOpacity
      onPress={open}
      className="-mr-1 p-2"
      hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
    >
      <SearchIcon color="#fafafa" size={20} />
    </TouchableOpacity>
  );
}
