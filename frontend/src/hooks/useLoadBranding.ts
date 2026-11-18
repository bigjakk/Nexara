import { useEffect } from "react";
import {
  useBranding,
  useBrandingImage,
} from "@/features/settings/api/settings-queries";
import { DEFAULT_FAVICON, useBrandingStore } from "@/stores/branding-store";

function setFaviconLink(url: string) {
  const link =
    document.querySelector<HTMLLinkElement>('link[rel="icon"]') ??
    document.createElement("link");
  link.rel = "icon";
  link.href = url;
  if (!link.parentNode) {
    document.head.appendChild(link);
  }
}

/**
 * The installation's branding, loaded into the branding store — title, logo
 * and favicon — for a page that runs outside the login screens: AppShell, and
 * a console in a browser tab of its own. The tab's icon is the uploaded
 * favicon once it has loaded, else index.html's own. Returns the query, so
 * that a page that sets its own document title can set it again after a load,
 * which writes the title (branded, or the default) when it changes.
 */
export function useLoadBranding() {
  const brandingQuery = useBranding();
  const loadBranding = useBrandingStore((s) => s.loadFromBranding);
  const faviconUrl = useBrandingStore((s) => s.faviconUrl);
  const favicon = useBrandingImage("favicon", faviconUrl);

  useEffect(() => {
    if (brandingQuery.data) {
      loadBranding(brandingQuery.data);
    }
  }, [brandingQuery.data, loadBranding]);

  useEffect(() => {
    setFaviconLink(favicon ?? DEFAULT_FAVICON);
  }, [favicon]);

  return brandingQuery;
}
