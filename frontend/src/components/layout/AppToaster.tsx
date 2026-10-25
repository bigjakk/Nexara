import { Toaster } from "sonner";
import { useThemeStore } from "@/stores/theme-store";

/** The app's toasts: AppShell's, and a console tab's of its own. */
export function AppToaster() {
  const themeMode = useThemeStore((s) => s.mode);
  return (
    <Toaster position="top-right" richColors closeButton theme={themeMode} />
  );
}
