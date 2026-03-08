import { useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Sun, Moon, Monitor, Check, Palette, Globe } from "lucide-react";
import { supportedLanguages } from "@/lib/i18n";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useThemeStore } from "@/stores/theme-store";
import {
  usePreferencesStore,
  type ByteUnit,
  type DateFormat,
} from "@/stores/preferences-store";
import {
  useSetting,
  useUpsertSetting,
} from "@/features/settings/api/settings-queries";
import { cn } from "@/lib/utils";

const themeModes = [
  { value: "light", labelKey: "light", icon: Sun },
  { value: "dark", labelKey: "dark", icon: Moon },
  { value: "system", labelKey: "system", icon: Monitor },
] as const;

const accentColors = [
  { name: "default", labelKey: "colorDefault", hsl: "240 5.9% 10%", darkHsl: "0 0% 98%" },
  { name: "blue", labelKey: "colorBlue", hsl: "221 83% 53%", darkHsl: "217 91% 60%" },
  { name: "green", labelKey: "colorGreen", hsl: "142 71% 45%", darkHsl: "142 71% 45%" },
  { name: "purple", labelKey: "colorPurple", hsl: "262 83% 58%", darkHsl: "262 83% 58%" },
  { name: "orange", labelKey: "colorOrange", hsl: "25 95% 53%", darkHsl: "25 95% 53%" },
  { name: "red", labelKey: "colorRed", hsl: "0 72% 51%", darkHsl: "0 72% 51%" },
  { name: "pink", labelKey: "colorPink", hsl: "330 81% 60%", darkHsl: "330 81% 60%" },
  { name: "teal", labelKey: "colorTeal", hsl: "173 80% 40%", darkHsl: "173 80% 40%" },
  { name: "cyan", labelKey: "colorCyan", hsl: "189 94% 43%", darkHsl: "189 94% 43%" },
  { name: "amber", labelKey: "colorAmber", hsl: "38 92% 50%", darkHsl: "38 92% 50%" },
] as const;

const byteUnitOptions: { value: ByteUnit; labelKey: string; exampleKey: string }[] = [
  { value: "binary", labelKey: "binaryGiB", exampleKey: "binaryExample" },
  { value: "decimal", labelKey: "decimalGB", exampleKey: "decimalExample" },
];

const dateFormatOptions: { value: DateFormat; labelKey: string; exampleKey: string }[] = [
  { value: "relative", labelKey: "relative", exampleKey: "relativeExample" },
  { value: "iso", labelKey: "iso8601", exampleKey: "isoExample" },
  { value: "locale", labelKey: "local", exampleKey: "localeExample" },
];

const refreshIntervalOptions = [
  { value: 0, labelKey: "manual" },
  { value: 10, labelKey: "10Seconds" },
  { value: 30, labelKey: "30Seconds" },
  { value: 60, labelKey: "1Minute" },
  { value: 300, labelKey: "5Minutes" },
];

export function AppearancePage() {
  const { t } = useTranslation("settings");
  const themeMode = useThemeStore((s) => s.mode);
  const setThemeMode = useThemeStore((s) => s.setMode);
  const { preferences, setPreferences } = usePreferencesStore();
  const upsertSetting = useUpsertSetting();

  // Load user preferences from backend on mount
  const prefsQuery = useSetting("user.preferences", "user");
  const { loadFromJSON } = usePreferencesStore();

  useEffect(() => {
    if (prefsQuery.data?.value) {
      loadFromJSON(prefsQuery.data.value);
    }
  }, [prefsQuery.data?.value, loadFromJSON]);

  const savePreferences = useCallback(
    (updated: Partial<typeof preferences>) => {
      const newPrefs = { ...preferences, ...updated };
      setPreferences(updated);
      upsertSetting.mutate({
        key: "user.preferences",
        value: newPrefs,
        scope: "user",
      });
    },
    [preferences, setPreferences, upsertSetting],
  );

  const handleAccentChange = useCallback(
    (colorName: string) => {
      savePreferences({ accentColor: colorName });
      applyAccentColor(colorName);
    },
    [savePreferences],
  );

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">{t("appearance")}</h1>
        <p className="text-muted-foreground">
          {t("customizeLookAndFeel")}
        </p>
      </div>

      {/* Language */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Globe className="h-5 w-5" />
            {t("language")}
          </CardTitle>
          <CardDescription>
            {t("selectLanguage")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Select
            value={preferences.language}
            onValueChange={(v) => { savePreferences({ language: v }); }}
          >
            <SelectTrigger className="w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {supportedLanguages.map((lang) => (
                <SelectItem key={lang.code} value={lang.code}>
                  <span>{lang.nativeName}</span>
                  {lang.nativeName !== (lang.name as string) && (
                    <span className="ml-2 text-xs text-muted-foreground">
                      ({lang.name})
                    </span>
                  )}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardContent>
      </Card>

      {/* Theme Mode */}
      <Card>
        <CardHeader>
          <CardTitle>{t("theme")}</CardTitle>
          <CardDescription>
            {t("chooseLightDarkMode")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex gap-3">
            {themeModes.map(({ value, labelKey, icon: Icon }) => (
              <Button
                key={value}
                variant={themeMode === value ? "default" : "outline"}
                className="flex-1 gap-2"
                onClick={() => { setThemeMode(value); }}
              >
                <Icon className="h-4 w-4" />
                {t(labelKey)}
              </Button>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* Accent Color */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Palette className="h-5 w-5" />
            {t("accentColor")}
          </CardTitle>
          <CardDescription>
            {t("chooseAccentColor")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-5 gap-3 sm:grid-cols-10">
            {accentColors.map((color) => (
              <button
                key={color.name}
                onClick={() => { handleAccentChange(color.name); }}
                className={cn(
                  "group relative flex h-10 w-10 items-center justify-center rounded-full border-2 transition-all hover:scale-110",
                  preferences.accentColor === color.name
                    ? "border-foreground"
                    : "border-transparent",
                )}
                style={{ backgroundColor: `hsl(${color.hsl})` }}
                title={t(color.labelKey)}
              >
                {preferences.accentColor === color.name && (
                  <Check className="h-4 w-4 text-white" />
                )}
              </button>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* Display Preferences */}
      <Card>
        <CardHeader>
          <CardTitle>{t("displayPreferences")}</CardTitle>
          <CardDescription>
            {t("configureDataDisplay")}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* Byte Units */}
          <div className="space-y-2">
            <Label>{t("storageUnits")}</Label>
            <Select
              value={preferences.byteUnit}
              onValueChange={(v) => { savePreferences({ byteUnit: v as ByteUnit }); }}
            >
              <SelectTrigger className="w-64">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {byteUnitOptions.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>
                    <span>{t(opt.labelKey)}</span>
                    <span className="ml-2 text-xs text-muted-foreground">
                      ({t(opt.exampleKey)})
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Date Format */}
          <div className="space-y-2">
            <Label>{t("dateFormat")}</Label>
            <Select
              value={preferences.dateFormat}
              onValueChange={(v) => { savePreferences({ dateFormat: v as DateFormat }); }}
            >
              <SelectTrigger className="w-64">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {dateFormatOptions.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>
                    <span>{t(opt.labelKey)}</span>
                    <span className="ml-2 text-xs text-muted-foreground">
                      ({t(opt.exampleKey)})
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Refresh Interval */}
          <div className="space-y-2">
            <Label>{t("defaultRefreshInterval")}</Label>
            <Select
              value={String(preferences.refreshInterval)}
              onValueChange={(v) => { savePreferences({ refreshInterval: Number(v) }); }}
            >
              <SelectTrigger className="w-64">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {refreshIntervalOptions.map((opt) => (
                  <SelectItem key={opt.value} value={String(opt.value)}>
                    {t(opt.labelKey)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// Apply accent color to CSS variables
function applyAccentColor(colorName: string) {
  const color = accentColors.find((c) => c.name === colorName);
  if (!color) return;

  const root = document.documentElement;
  if (colorName === "default") {
    // Reset to original theme values
    root.style.removeProperty("--primary");
    root.style.removeProperty("--ring");
  } else {
    const isDark = root.classList.contains("dark");
    const hsl = isDark ? color.darkHsl : color.hsl;
    root.style.setProperty("--primary", hsl);
    root.style.setProperty("--ring", hsl);
  }
}
