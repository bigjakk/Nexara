import type { ReactNode } from "react";

import { useBrandingImage } from "@/features/settings/api/settings-queries";
import { useBrandingStore } from "@/stores/branding-store";

interface BrandingLogoProps {
  className: string;
  alt?: string;
  /** Shown while no logo is uploaded, and until the uploaded one has loaded. */
  fallback?: ReactNode;
}

/** The installation's uploaded logo, read with the session (see useBrandingImage). */
export function BrandingLogo({
  className,
  alt = "",
  fallback = null,
}: BrandingLogoProps) {
  const logoUrl = useBrandingStore((s) => s.logoUrl);
  const src = useBrandingImage("logo", logoUrl);
  return src ? <img src={src} alt={alt} className={className} /> : fallback;
}
