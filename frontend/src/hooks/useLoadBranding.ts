import { useEffect } from "react";
import { useBranding } from "@/features/settings/api/settings-queries";
import { useBrandingStore } from "@/stores/branding-store";

/**
 * The installation's branding, loaded into the branding store — title, logo
 * and favicon — for a page that runs outside the login screens: AppShell, and
 * a console in a browser tab of its own. Returns the query, so that a page
 * that sets its own document title can set it again after each load, which
 * writes the branded title when there is one.
 */
export function useLoadBranding() {
  const brandingQuery = useBranding();
  const loadBranding = useBrandingStore((s) => s.loadFromBranding);

  useEffect(() => {
    if (brandingQuery.data) {
      loadBranding(brandingQuery.data);
    }
  }, [brandingQuery.data, loadBranding]);

  return brandingQuery;
}
