import { useIsNarrowDesktop } from "@/hooks/useIsMobile";
import { useSidebarStore } from "@/stores/sidebar-store";

/**
 * Whether the desktop inventory tree is shown, and the two ways to change it.
 * At a narrow desktop width the tree starts hidden whatever the saved choice
 * says, and showing or hiding it there lasts for this page load only; at a
 * wider one the saved choice rules. A phone's drawer reads `treeVisible` as
 * it always has.
 */
export function useTreeVisibility(): {
  visible: boolean;
  show: () => void;
  hide: () => void;
} {
  const narrow = useIsNarrowDesktop();
  const treeVisible = useSidebarStore((s) => s.treeVisible);
  const shownNarrow = useSidebarStore((s) => s.treeShownNarrow);
  const setTreeVisible = useSidebarStore((s) => s.setTreeVisible);
  const setShownNarrow = useSidebarStore((s) => s.setTreeShownNarrow);
  return {
    visible: narrow ? shownNarrow : treeVisible,
    show: () => {
      if (narrow) setShownNarrow(true);
      else setTreeVisible(true);
    },
    hide: () => {
      if (narrow) setShownNarrow(false);
      else setTreeVisible(false);
    },
  };
}
