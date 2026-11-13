import { useState, useRef, useEffect } from "react";
import { Upload, Loader2, Server, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import {
  SettingRow,
  SettingsFooter,
  SettingsSection,
} from "@/components/page/Settings";
import {
  useBranding,
  useUpsertSetting,
  useUploadLogo,
  useUploadFavicon,
} from "@/features/settings/api/settings-queries";
import { useBrandingStore } from "@/stores/branding-store";
import { AdminNav } from "../components/AdminNav";

export function BrandingPage() {
  const brandingQuery = useBranding();
  const upsertSetting = useUpsertSetting();
  const uploadLogo = useUploadLogo();
  const uploadFavicon = useUploadFavicon();
  const branding = useBrandingStore();

  const [appTitle, setAppTitle] = useState(branding.appTitle);
  const logoInputRef = useRef<HTMLInputElement>(null);
  const faviconInputRef = useRef<HTMLInputElement>(null);

  // Sync from backend when data arrives
  useEffect(() => {
    if (brandingQuery.data) {
      const data = brandingQuery.data;
      if (typeof data["branding.app_title"] === "string") {
        const title = JSON.parse(data["branding.app_title"]) as string;
        setAppTitle(title);
      }
    }
  }, [brandingQuery.data]);

  const handleTitleSave = () => {
    const trimmed = appTitle.trim();
    if (!trimmed) return;
    upsertSetting.mutate(
      {
        key: "branding.app_title",
        value: trimmed,
        scope: "global",
      },
      {
        onSuccess: () => {
          branding.setAppTitle(trimmed);
        },
      },
    );
  };

  const handleLogoUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    uploadLogo.mutate(file, {
      onSuccess: (data) => {
        branding.setLogoUrl(data.logo_url);
      },
    });
  };

  const handleFaviconUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    uploadFavicon.mutate(file, {
      onSuccess: (data) => {
        branding.setFaviconUrl(data.favicon_url);
      },
    });
  };

  const handleResetTitle = () => {
    setAppTitle("Nexara");
    upsertSetting.mutate(
      {
        key: "branding.app_title",
        value: "Nexara",
        scope: "global",
      },
      {
        onSuccess: () => {
          branding.setAppTitle("Nexara");
        },
      },
    );
  };

  // What the preview shows: the title as typed, saved or not.
  const previewTitle = appTitle.trim() || "Nexara";
  const images =
    branding.logoUrl && branding.faviconUrl
      ? "a custom logo and favicon"
      : branding.logoUrl
        ? "a custom logo"
        : branding.faviconUrl
          ? "a custom favicon"
          : "the default logo and favicon";

  return (
    <div>
      <AdminNav />
      <div className="max-w-5xl space-y-5">
        <PageHeader
          title="Branding"
          summary={`This install is called "${branding.appTitle}" and uses ${images}.`}
        />

        {/* Settings on the left, what they produce on the right. */}
        <div className="flex flex-wrap items-start gap-4">
          <div className="flex min-w-0 flex-[999_1_460px] flex-col gap-4">
            <SettingsSection title="Name">
              <SettingRow
                label="Application title"
                description="Shown in the top bar and the browser tab."
                htmlFor="branding-app-title"
              >
                <Input
                  id="branding-app-title"
                  value={appTitle}
                  onChange={(e) => {
                    setAppTitle(e.target.value);
                  }}
                  placeholder="Nexara"
                  maxLength={50}
                />
              </SettingRow>
              <SettingsFooter>
                <Button variant="outline" onClick={handleResetTitle}>
                  Reset
                </Button>
                <Button
                  onClick={handleTitleSave}
                  disabled={!appTitle.trim() || upsertSetting.isPending}
                >
                  {upsertSetting.isPending ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : null}
                  Save
                </Button>
              </SettingsFooter>
            </SettingsSection>

            <SettingsSection
              title="Images"
              description="An upload replaces the current image as soon as it finishes."
            >
              <SettingRow
                label="Logo"
                description="PNG, JPG, SVG or WebP, up to 2 MiB."
              >
                {branding.logoUrl && (
                  <div className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-foreground/[0.06]">
                    <img
                      src={branding.logoUrl}
                      alt="Logo"
                      className="h-8 w-8 object-contain"
                    />
                  </div>
                )}
                <input
                  ref={logoInputRef}
                  type="file"
                  accept="image/png,image/jpeg,image/svg+xml,image/webp"
                  className="hidden"
                  onChange={handleLogoUpload}
                />
                <Button
                  variant="outline"
                  onClick={() => {
                    logoInputRef.current?.click();
                  }}
                  disabled={uploadLogo.isPending}
                >
                  {uploadLogo.isPending ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Upload className="h-4 w-4" />
                  )}
                  Upload logo
                </Button>
              </SettingRow>
              <SettingRow
                label="Favicon"
                description="The icon in the browser tab: .ico, .png or .svg, up to 512 KiB. 32x32 or 64x64 pixels works best."
              >
                {branding.faviconUrl && (
                  <div className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-foreground/[0.06]">
                    <img
                      src={branding.faviconUrl}
                      alt="Favicon"
                      className="h-6 w-6 object-contain"
                    />
                  </div>
                )}
                <input
                  ref={faviconInputRef}
                  type="file"
                  accept="image/x-icon,image/png,image/svg+xml"
                  className="hidden"
                  onChange={handleFaviconUpload}
                />
                <Button
                  variant="outline"
                  onClick={() => {
                    faviconInputRef.current?.click();
                  }}
                  disabled={uploadFavicon.isPending}
                >
                  {uploadFavicon.isPending ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Upload className="h-4 w-4" />
                  )}
                  Upload favicon
                </Button>
              </SettingRow>
            </SettingsSection>
          </div>

          <div className="flex min-w-0 flex-[1_1_280px] flex-col gap-4">
            <Panel title="Preview" aside="follows the title as you type">
              <div className="space-y-4">
                <div>
                  <div className="mb-1.5 text-[12.5px] text-muted-foreground">
                    Top bar
                  </div>
                  <div className="flex items-center gap-2.5 rounded-lg bg-background px-2.5 py-2">
                    <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-foreground/[0.08]">
                      {branding.logoUrl ? (
                        <img
                          src={branding.logoUrl}
                          alt=""
                          className="h-5 w-5 object-contain"
                        />
                      ) : (
                        <Server className="h-5 w-5" />
                      )}
                    </span>
                    <span className="truncate text-base font-semibold tracking-tight [font-stretch:112%]">
                      {previewTitle}
                    </span>
                  </div>
                </div>
                <div>
                  <div className="mb-1.5 text-[12.5px] text-muted-foreground">
                    Browser tab
                  </div>
                  <div className="flex max-w-[15rem] items-center gap-2 rounded-t-lg bg-background px-3 py-2 text-[13px]">
                    {/* index.html's own icon until one is uploaded. */}
                    <img
                      src={branding.faviconUrl ?? "/favicon.png"}
                      alt=""
                      className="h-4 w-4 shrink-0 object-contain"
                    />
                    <span className="truncate">{previewTitle}</span>
                    <X className="ml-auto h-3 w-3 shrink-0 text-muted-foreground" />
                  </div>
                </div>
              </div>
            </Panel>
          </div>
        </div>
      </div>
    </div>
  );
}
