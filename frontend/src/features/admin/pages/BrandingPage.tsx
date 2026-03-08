import { useState, useRef, useEffect } from "react";
import { Upload, Image, Type, Globe, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
    setAppTitle("ProxDash");
    upsertSetting.mutate(
      {
        key: "branding.app_title",
        value: "ProxDash",
        scope: "global",
      },
      {
        onSuccess: () => {
          branding.setAppTitle("ProxDash");
        },
      },
    );
  };

  return (
    <div>
      <AdminNav />
      <div className="space-y-6 p-6">
      <div>
        <h1 className="text-2xl font-bold">Branding</h1>
        <p className="text-muted-foreground">
          Customize the appearance and branding of your ProxDash instance.
        </p>
      </div>

      {/* App Title */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Type className="h-5 w-5" />
            Application Title
          </CardTitle>
          <CardDescription>
            Set the title shown in the sidebar and browser tab.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center gap-3">
            <Input
              value={appTitle}
              onChange={(e) => { setAppTitle(e.target.value); }}
              placeholder="ProxDash"
              className="max-w-xs"
              maxLength={50}
            />
            <Button
              onClick={handleTitleSave}
              disabled={
                !appTitle.trim() ||
                upsertSetting.isPending
              }
            >
              {upsertSetting.isPending ? (
                <Loader2 className="mr-1 h-4 w-4 animate-spin" />
              ) : null}
              Save
            </Button>
            <Button variant="outline" onClick={handleResetTitle}>
              Reset
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* Logo Upload */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Image className="h-5 w-5" />
            Logo
          </CardTitle>
          <CardDescription>
            Upload a custom logo (PNG, JPG, SVG, or WebP, max 2MB).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center gap-4">
            {branding.logoUrl && (
              <div className="flex h-16 w-16 items-center justify-center rounded-lg border bg-card">
                <img
                  src={branding.logoUrl}
                  alt="Logo"
                  className="h-12 w-12 object-contain"
                />
              </div>
            )}
            <div>
              <input
                ref={logoInputRef}
                type="file"
                accept="image/png,image/jpeg,image/svg+xml,image/webp"
                className="hidden"
                onChange={handleLogoUpload}
              />
              <Button
                variant="outline"
                className="gap-1"
                onClick={() => { logoInputRef.current?.click(); }}
                disabled={uploadLogo.isPending}
              >
                {uploadLogo.isPending ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Upload className="h-4 w-4" />
                )}
                Upload Logo
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Favicon Upload */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Globe className="h-5 w-5" />
            Favicon
          </CardTitle>
          <CardDescription>
            Upload a custom favicon (.ico, .png, or .svg, max 512KB).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center gap-4">
            {branding.faviconUrl && (
              <div className="flex h-10 w-10 items-center justify-center rounded border bg-card">
                <img
                  src={branding.faviconUrl}
                  alt="Favicon"
                  className="h-6 w-6 object-contain"
                />
              </div>
            )}
            <div>
              <input
                ref={faviconInputRef}
                type="file"
                accept="image/x-icon,image/png,image/svg+xml"
                className="hidden"
                onChange={handleFaviconUpload}
              />
              <Button
                variant="outline"
                className="gap-1"
                onClick={() => { faviconInputRef.current?.click(); }}
                disabled={uploadFavicon.isPending}
              >
                {uploadFavicon.isPending ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Upload className="h-4 w-4" />
                )}
                Upload Favicon
              </Button>
            </div>
          </div>
          <Label className="text-xs text-muted-foreground">
            The favicon appears in the browser tab. Recommended size: 32x32 or 64x64 pixels.
          </Label>
        </CardContent>
      </Card>
    </div>
    </div>
  );
}
