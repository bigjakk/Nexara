import { useEffect, useCallback } from "react";
import { Sun, Moon, Monitor, Check, Palette } from "lucide-react";

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
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
] as const;

const accentColors = [
  { name: "default", label: "Default", hsl: "240 5.9% 10%", darkHsl: "0 0% 98%" },
  { name: "blue", label: "Blue", hsl: "221 83% 53%", darkHsl: "217 91% 60%" },
  { name: "green", label: "Green", hsl: "142 71% 45%", darkHsl: "142 71% 45%" },
  { name: "purple", label: "Purple", hsl: "262 83% 58%", darkHsl: "262 83% 58%" },
  { name: "orange", label: "Orange", hsl: "25 95% 53%", darkHsl: "25 95% 53%" },
  { name: "red", label: "Red", hsl: "0 72% 51%", darkHsl: "0 72% 51%" },
  { name: "pink", label: "Pink", hsl: "330 81% 60%", darkHsl: "330 81% 60%" },
  { name: "teal", label: "Teal", hsl: "173 80% 40%", darkHsl: "173 80% 40%" },
  { name: "cyan", label: "Cyan", hsl: "189 94% 43%", darkHsl: "189 94% 43%" },
  { name: "amber", label: "Amber", hsl: "38 92% 50%", darkHsl: "38 92% 50%" },
] as const;

const byteUnitOptions: { value: ByteUnit; label: string; example: string }[] = [
  { value: "binary", label: "Binary (GiB)", example: "1024 MiB = 1 GiB" },
  { value: "decimal", label: "Decimal (GB)", example: "1000 MB = 1 GB" },
];

const dateFormatOptions: { value: DateFormat; label: string; example: string }[] = [
  { value: "relative", label: "Relative", example: "5 minutes ago" },
  { value: "iso", label: "ISO 8601", example: "2026-03-08T14:30:00Z" },
  { value: "locale", label: "Local", example: "Mar 8, 2026, 2:30 PM" },
];

const refreshIntervalOptions = [
  { value: 0, label: "Manual" },
  { value: 10, label: "10 seconds" },
  { value: 30, label: "30 seconds" },
  { value: 60, label: "1 minute" },
  { value: 300, label: "5 minutes" },
];

export function AppearancePage() {
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
        <h1 className="text-2xl font-bold">Appearance</h1>
        <p className="text-muted-foreground">
          Customize the look and feel of ProxDash.
        </p>
      </div>

      {/* Theme Mode */}
      <Card>
        <CardHeader>
          <CardTitle>Theme</CardTitle>
          <CardDescription>
            Choose between light and dark mode, or follow your system setting.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex gap-3">
            {themeModes.map(({ value, label, icon: Icon }) => (
              <Button
                key={value}
                variant={themeMode === value ? "default" : "outline"}
                className="flex-1 gap-2"
                onClick={() => { setThemeMode(value); }}
              >
                <Icon className="h-4 w-4" />
                {label}
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
            Accent Color
          </CardTitle>
          <CardDescription>
            Choose an accent color for buttons, links, and highlights.
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
                title={color.label}
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
          <CardTitle>Display Preferences</CardTitle>
          <CardDescription>
            Configure how data is displayed throughout the application.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* Byte Units */}
          <div className="space-y-2">
            <Label>Storage Units</Label>
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
                    <span>{opt.label}</span>
                    <span className="ml-2 text-xs text-muted-foreground">
                      ({opt.example})
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Date Format */}
          <div className="space-y-2">
            <Label>Date Format</Label>
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
                    <span>{opt.label}</span>
                    <span className="ml-2 text-xs text-muted-foreground">
                      ({opt.example})
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Refresh Interval */}
          <div className="space-y-2">
            <Label>Default Refresh Interval</Label>
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
                    {opt.label}
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

