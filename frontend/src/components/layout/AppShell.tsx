import { useEffect, useMemo, useState } from "react";
import { Outlet, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import {
  Info,
  Key,
  LogOut,
  LogOutIcon,
  ShieldCheck,
  Sparkles,
  User,
} from "lucide-react";
import { apiClient } from "@/lib/api-client";

import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/hooks/useAuth";
import { useEventInvalidation } from "@/hooks/useEventInvalidation";
import { useWebSocketStore } from "@/stores/websocket-store";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useBranding } from "@/features/settings/api/settings-queries";
import { useBrandingStore } from "@/stores/branding-store";
import { useChangelogStore } from "@/stores/changelog-store";
import {
  extractBaseVersion,
  getEntriesToShow,
  type ChangelogEntry,
} from "@/lib/changelog";
import { useChangelog } from "@/features/changelog/api/changelog-queries";
import { Sidebar } from "./Sidebar";
import { TaskLogPanel } from "./TaskLogPanel";
import { TaskProgressDialog } from "./TaskProgressDialog";
import { ThemeToggle } from "./ThemeToggle";
import { ChangelogDialog } from "./ChangelogDialog";
import { AboutDialog } from "./AboutDialog";
import { FloatingConsole } from "@/features/console/components/FloatingConsole";
import { SearchBar } from "./SearchBar";
import { CreateResourceMenu } from "./CreateResourceMenu";

function getInitials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .filter(Boolean)
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

export function AppShell() {
  const { t } = useTranslation("navigation");
  const navigate = useNavigate();
  const { user, logout, logoutAll } = useAuth();
  const wsConnect = useWebSocketStore((s) => s.connect);
  const wsDisconnect = useWebSocketStore((s) => s.disconnect);
  const { data: clusters } = useClusters();
  const brandingQuery = useBranding();
  const loadBranding = useBrandingStore((s) => s.loadFromBranding);

  useEffect(() => {
    wsConnect();
    return () => { wsDisconnect(); };
  }, [wsConnect, wsDisconnect]);

  // Load branding settings on mount
  useEffect(() => {
    if (brandingQuery.data) {
      loadBranding(brandingQuery.data);
    }
  }, [brandingQuery.data, loadBranding]);

  // For dev builds, append "-Dev" to the browser tab title
  const { data: versionInfo } = useQuery({
    queryKey: ["version"],
    queryFn: () => apiClient.get<{ version: string }>("/api/v1/version"),
    staleTime: Infinity,
  });
  useEffect(() => {
    if (versionInfo?.version === "dev") {
      const base = useBrandingStore.getState().appTitle;
      if (!base.includes("Dev")) {
        document.title = `${base}-Dev`;
      }
    }
  }, [versionInfo, brandingQuery.data]);

  // Changelog popup: show on first launch and after version bumps.
  const lastSeenVersion = useChangelogStore((s) => s.lastSeenVersion);
  const setLastSeenVersion = useChangelogStore((s) => s.setLastSeenVersion);
  const [changelogOpen, setChangelogOpen] = useState(false);
  const [changelogEntries, setChangelogEntries] = useState<ChangelogEntry[]>([]);
  const [aboutOpen, setAboutOpen] = useState(false);
  const changelogQuery = useChangelog();

  useEffect(() => {
    if (!versionInfo) return;
    if (!changelogQuery.data) return;
    const base = extractBaseVersion(versionInfo.version);
    if (!base) return; // dev / unknown — no popup
    if (base === lastSeenVersion) return;

    const entries = getEntriesToShow(base, lastSeenVersion, changelogQuery.data);
    if (entries.length === 0) {
      // If GitHub returned no data at all (fetch failed or no releases yet),
      // don't advance — try again on the next page load when the cache
      // refreshes.
      if (changelogQuery.data.length === 0) return;
      // Otherwise the current version genuinely has nothing parseable (or is
      // outside the fetched window). Advance silently so we don't re-evaluate
      // on every render.
      setLastSeenVersion(base);
      return;
    }
    setChangelogEntries(entries);
    setChangelogOpen(true);
  }, [versionInfo, lastSeenVersion, setLastSeenVersion, changelogQuery.data]);

  const handleChangelogOpenChange = (open: boolean) => {
    setChangelogOpen(open);
    if (!open) {
      const base = extractBaseVersion(versionInfo?.version);
      if (base) setLastSeenVersion(base);
    }
  };

  const handleShowChangelog = () => {
    const base = extractBaseVersion(versionInfo?.version);
    const data = changelogQuery.data ?? [];
    const entry = base ? data.find((e) => e.version === base) : undefined;
    setChangelogEntries(entry ? [entry] : data.slice(0, 1));
    setChangelogOpen(true);
  };

  const clusterIds = useMemo(
    () => (clusters ?? []).map((c) => c.id),
    [clusters],
  );
  useEventInvalidation(clusterIds);

  const handleLogout = () => {
    void logout();
  };

  const handleLogoutAll = () => {
    void logoutAll();
  };

  return (
    <div className="flex h-screen">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden transition-all duration-200">
        <header className="flex h-14 items-center border-b px-4">
          <div className="flex flex-1 items-center justify-center">
            <SearchBar />
          </div>
          <div className="flex items-center gap-2">
          <CreateResourceMenu />
          <ThemeToggle />
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" className="gap-2">
                <Avatar className="h-7 w-7">
                  <AvatarFallback className="text-xs">
                    {user ? getInitials(user.display_name) : "?"}
                  </AvatarFallback>
                </Avatar>
                <span className="text-sm">
                  {user?.display_name ?? "User"}
                </span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <div className="px-2 py-1.5">
                <p className="text-sm font-medium">{user?.display_name}</p>
                <p className="text-xs text-muted-foreground">{user?.email}</p>
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => { void navigate("/settings/profile"); }}>
                <User className="mr-2 h-4 w-4" />
                {t("profile", { ns: "common" })}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => { void navigate("/settings/security"); }}>
                <ShieldCheck className="mr-2 h-4 w-4" />
                {t("security")}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => { void navigate("/settings/api-keys"); }}>
                <Key className="mr-2 h-4 w-4" />
                API Keys
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={handleShowChangelog}>
                <Sparkles className="mr-2 h-4 w-4" />
                What&apos;s New
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => { setAboutOpen(true); }}>
                <Info className="mr-2 h-4 w-4" />
                About
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={handleLogout}>
                <LogOut className="mr-2 h-4 w-4" />
                {t("signOut", { ns: "common" })}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={handleLogoutAll}>
                <LogOutIcon className="mr-2 h-4 w-4" />
                {t("signOutAllDevices", { ns: "common" })}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          </div>
        </header>
        <Separator />
        <main className="flex-1 overflow-auto p-6">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </main>
        <TaskLogPanel />
        <TaskProgressDialog />
      </div>
      <FloatingConsole />
      <ChangelogDialog
        open={changelogOpen}
        onOpenChange={handleChangelogOpenChange}
        entries={changelogEntries}
        loading={changelogQuery.isLoading && changelogEntries.length === 0}
        repoReleasesUrl="https://github.com/bigjakk/Nexara/releases"
      />
      <AboutDialog
        open={aboutOpen}
        onOpenChange={setAboutOpen}
        version={versionInfo?.version}
      />
    </div>
  );
}
