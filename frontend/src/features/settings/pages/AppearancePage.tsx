import { useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Sun, Moon, Monitor, Check } from "lucide-react";
import { supportedLanguages } from "@/lib/i18n";

import { PageHeader } from "@/components/page/PageHeader";
import { SettingRow, SettingsSection } from "@/components/page/Settings";
import { Segmented } from "@/components/ui/segmented";
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
  type ConsoleOpenMode,
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
  {
    name: "default",
    labelKey: "colorDefault",
    hsl: "240 5.9% 10%",
    darkHsl: "0 0% 98%",
  },
  {
    name: "blue",
    labelKey: "colorBlue",
    hsl: "221 83% 53%",
    darkHsl: "217 91% 60%",
  },
  {
    name: "green",
    labelKey: "colorGreen",
    hsl: "142 71% 45%",
    darkHsl: "142 71% 45%",
  },
  {
    name: "purple",
    labelKey: "colorPurple",
    hsl: "262 83% 58%",
    darkHsl: "262 83% 58%",
  },
  {
    name: "orange",
    labelKey: "colorOrange",
    hsl: "25 95% 53%",
    darkHsl: "25 95% 53%",
  },
  { name: "red", labelKey: "colorRed", hsl: "0 72% 51%", darkHsl: "0 72% 51%" },
  {
    name: "pink",
    labelKey: "colorPink",
    hsl: "330 81% 60%",
    darkHsl: "330 81% 60%",
  },
  {
    name: "teal",
    labelKey: "colorTeal",
    hsl: "173 80% 40%",
    darkHsl: "173 80% 40%",
  },
  {
    name: "cyan",
    labelKey: "colorCyan",
    hsl: "189 94% 43%",
    darkHsl: "189 94% 43%",
  },
  {
    name: "amber",
    labelKey: "colorAmber",
    hsl: "38 92% 50%",
    darkHsl: "38 92% 50%",
  },
] as const;

const byteUnitOptions: {
  value: ByteUnit;
  labelKey: string;
  exampleKey: string;
}[] = [
  { value: "binary", labelKey: "binaryGiB", exampleKey: "binaryExample" },
  { value: "decimal", labelKey: "decimalGB", exampleKey: "decimalExample" },
];

const dateFormatOptions: {
  value: DateFormat;
  labelKey: string;
  exampleKey: string;
}[] = [
  { value: "relative", labelKey: "relative", exampleKey: "relativeExample" },
  { value: "iso", labelKey: "iso8601", exampleKey: "isoExample" },
  { value: "locale", labelKey: "local", exampleKey: "localeExample" },
];

const consoleOpenModeOptions: { value: ConsoleOpenMode; labelKey: string }[] = [
  { value: "floating", labelKey: "consoleFloatingWindow" },
  { value: "tab", labelKey: "consoleNewTab" },
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
    <div className="space-y-5">
      <PageHeader title={t("appearance")} summary={t("customizeLookAndFeel")} />

      <div className="max-w-3xl space-y-4">
        <SettingsSection title={t("look")}>
          <SettingRow label={t("theme")} description={t("chooseLightDarkMode")}>
            <Segmented
              aria-label={t("theme")}
              size="md"
              value={themeMode}
              onValueChange={setThemeMode}
              options={themeModes.map(({ value, labelKey, icon: Icon }) => ({
                value,
                label: (
                  <>
                    <Icon />
                    {t(labelKey)}
                  </>
                ),
              }))}
            />
          </SettingRow>
          <SettingRow
            label={t("accentColor")}
            description={t("chooseAccentColor")}
          >
            <div
              role="group"
              aria-label={t("accentColor")}
              className="flex flex-wrap gap-2 sm:justify-end"
            >
              {accentColors.map((color) => {
                const selected = preferences.accentColor === color.name;
                // "Default" is the theme's own neutral, so its swatch follows
                // the theme rather than showing the light theme's near-black.
                const neutral = color.name === "default";
                return (
                  <button
                    key={color.name}
                    type="button"
                    aria-pressed={selected}
                    aria-label={t(color.labelKey)}
                    onClick={() => {
                      handleAccentChange(color.name);
                    }}
                    className={cn(
                      "grid h-7 w-7 place-items-center rounded-full outline-hidden ring-offset-2 ring-offset-card transition-shadow hover:ring-2 hover:ring-foreground/30 focus-visible:ring-2 focus-visible:ring-ring",
                      neutral && "bg-foreground",
                      selected &&
                        "ring-2 ring-foreground hover:ring-foreground",
                    )}
                    style={
                      neutral
                        ? undefined
                        : { backgroundColor: `hsl(${color.hsl})` }
                    }
                    title={t(color.labelKey)}
                  >
                    {selected && (
                      <Check
                        className={cn(
                          "h-3.5 w-3.5",
                          neutral ? "text-background" : "text-white",
                        )}
                      />
                    )}
                  </button>
                );
              })}
            </div>
          </SettingRow>
        </SettingsSection>

        <SettingsSection
          title={t("displayPreferences")}
          description={t("configureDataDisplay")}
        >
          <SettingRow
            label={t("language")}
            description={t("selectLanguage")}
            htmlFor="pref-language"
          >
            <Select
              value={preferences.language}
              onValueChange={(v) => {
                savePreferences({ language: v });
              }}
            >
              <SelectTrigger id="pref-language" className="w-full sm:w-64">
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
          </SettingRow>

          <SettingRow
            label={t("storageUnits")}
            description={t("storageUnitsHelp")}
            htmlFor="pref-byte-unit"
          >
            <Select
              value={preferences.byteUnit}
              onValueChange={(v) => {
                savePreferences({ byteUnit: v as ByteUnit });
              }}
            >
              <SelectTrigger id="pref-byte-unit" className="w-full sm:w-64">
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
          </SettingRow>

          <SettingRow
            label={t("dateFormat")}
            description={t("dateFormatHelp")}
            htmlFor="pref-date-format"
          >
            <Select
              value={preferences.dateFormat}
              onValueChange={(v) => {
                savePreferences({ dateFormat: v as DateFormat });
              }}
            >
              <SelectTrigger id="pref-date-format" className="w-full sm:w-64">
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
          </SettingRow>

          <SettingRow
            label={t("defaultRefreshInterval")}
            description={t("refreshIntervalHelp")}
            htmlFor="pref-refresh"
          >
            <Select
              value={String(preferences.refreshInterval)}
              onValueChange={(v) => {
                savePreferences({ refreshInterval: Number(v) });
              }}
            >
              <SelectTrigger id="pref-refresh" className="w-full sm:w-64">
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
          </SettingRow>
        </SettingsSection>

        <SettingsSection title={t("console")}>
          <SettingRow
            label={t("openConsolesIn")}
            description={t("consoleOpenDescription")}
            htmlFor="pref-console-open"
          >
            <Select
              value={preferences.consoleOpenMode}
              onValueChange={(v) => {
                savePreferences({ consoleOpenMode: v as ConsoleOpenMode });
              }}
            >
              <SelectTrigger id="pref-console-open" className="w-full sm:w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {consoleOpenModeOptions.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>
                    {t(opt.labelKey)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </SettingRow>
        </SettingsSection>
      </div>
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
