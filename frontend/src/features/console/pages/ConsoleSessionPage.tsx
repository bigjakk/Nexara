import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import {
  Loader2,
  Maximize,
  Minimize,
  RotateCcw,
  TerminalSquare,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { AppToaster } from "@/components/layout/AppToaster";
import { TaskProgressDialog } from "@/components/layout/TaskProgressDialog";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useBranding } from "@/features/settings/api/settings-queries";
import { useVM } from "@/features/vms/api/vm-queries";
import { useIsMobile } from "@/hooks/useIsMobile";
import { useLiveUpdates } from "@/hooks/useLiveUpdates";
import { useLoadBranding } from "@/hooks/useLoadBranding";
import { usePermissions } from "@/hooks/usePermissions";
import { useSignOutFromOtherTabs } from "@/hooks/useSignOutFromOtherTabs";
import { ApiClientError } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { useBrandingStore } from "@/stores/branding-store";
import {
  ConsoleStoreContext,
  createConsoleSessionStore,
  useConsoleSession,
} from "@/stores/console-store";
import { StatusIcon } from "../components/ConsoleTabBar";
import { Terminal } from "../components/Terminal";
import { VNCViewer } from "../components/VNCViewer";
import { consoleIdentity, takeLaunch } from "../lib/console-launch";
import {
  consoleLabel,
  consolePermission,
  parseConsoleSessionTarget,
} from "../lib/console-session";
import type {
  ConsoleStatus,
  ConsoleTab,
  ConsoleTarget,
} from "../types/console";

/** Read out with the header's status icon. */
const STATUS_TEXT: Record<ConsoleStatus, string> = {
  idle: "Not connected",
  connecting: "Connecting",
  connected: "Connected",
  reconnecting: "Reconnecting",
  disconnected: "Disconnected",
  error: "Connection failed",
  "guest-stopped": "Powered off",
};

/**
 * One console in a browser tab of its own, outside the app shell. Its query
 * names the console (consoleSessionHref); everything else — the session's
 * status, its reconnects, a migrated guest's new node — lives in a store of
 * this page's own, never in the floating console's.
 */
export function ConsoleSessionPage() {
  const [params] = useSearchParams();
  const target = useMemo(() => parseConsoleSessionTarget(params), [params]);
  const { canConsole } = usePermissions();
  useSignOutFromOtherTabs();
  // Only a sign-out this tab is following raises it here (there is no Sign
  // out on this page): the console goes at once, not when the revoke answers.
  const signingOut = useAuthStore((s) => s.isLoggingOut);

  let body: ReactNode;
  if (signingOut) {
    body = (
      <ConsoleSessionNotice
        title="Signed out in another tab"
        description="The console has closed."
      />
    );
  } else if (target === null) {
    body = (
      <ConsoleSessionNotice
        title="This console link is incomplete"
        description="It does not name a cluster, a node and a console. Open the console again from Nexara."
      />
    );
  } else if (!canConsole(consolePermission(target.type))) {
    body = (
      <ConsoleSessionNotice
        title="You cannot open this console"
        description="Your role does not allow consoles on this kind of resource."
      />
    );
  } else {
    // Keyed by the console, so that another one in the address is checked
    // afresh; only its node changes within the page, which it does not key on.
    body = (
      <CheckedConsoleSession key={consoleIdentity(target)} target={target} />
    );
  }
  return (
    <>
      <BrandingLoader />
      {body}
    </>
  );
}

/**
 * The installation's branding, for every state of the page. Rendered before
 * the rest, so that its effect runs first and the console's own title, set
 * after it, is the one that stays.
 */
function BrandingLoader() {
  useLoadBranding();
  return null;
}

interface CheckedTarget {
  target: ConsoleTarget;
  guestName: string | undefined;
  /** Its guest row is gone or hidden: what the VMID names is not confirmed. */
  unconfirmed: boolean;
}

type Verdict =
  { ok: true; checked: CheckedTarget } | { ok: false; view: ReactNode };

function notice(title: string, description: string): Verdict {
  return {
    ok: false,
    view: <ConsoleSessionNotice title={title} description={description} />,
  };
}

/** A refusal that says the guest row cannot be had: gone, or hidden. */
function rowUnavailable(error: Error | null): boolean {
  return (
    error instanceof ApiClientError &&
    (error.status === 404 || error.status === 403)
  );
}

/**
 * The link's console as the server answers for it. The node must be one of
 * the cluster's: the header would otherwise show whatever text the link
 * carries. And the guest its resource names must be the VMID it dials: the
 * label, Power and Media follow the resource while the screen follows the
 * VMID, so a link pairing two guests would name and act on one while showing
 * the other.
 */
function judge(
  target: ConsoleTarget,
  nodes: ReturnType<typeof useClusterNodes>,
  guest: ReturnType<typeof useVM>,
): Verdict {
  if (nodes.data === undefined) {
    if (!nodes.isError) return { ok: false, view: <ConsoleSessionLoading /> };
    return notice(
      "This console link could not be checked",
      nodes.error instanceof ApiClientError && nodes.error.status === 403
        ? "Your role cannot list this cluster's nodes, which a console tab checks its link against."
        : "Nexara could not check it. Reload the tab to try again, or open the console again from Nexara.",
    );
  }
  if (!nodes.data.some((n) => n.name === target.node)) {
    return notice(
      "This console link names no node of its cluster",
      "Open the console again from Nexara.",
    );
  }
  if (target.resourceId === undefined) {
    return {
      ok: true,
      checked: { target, guestName: undefined, unconfirmed: false },
    };
  }
  if (guest.data === undefined) {
    if (!guest.isError) return { ok: false, view: <ConsoleSessionLoading /> };
    if (!rowUnavailable(guest.error)) {
      return notice(
        "This console link could not be checked",
        "Nexara could not check it. Reload the tab to try again, or open the console again from Nexara.",
      );
    }
    // The guest row is gone (re-created since the link was made) or hidden
    // from this role: the console still dials by VMID, without the menus that
    // act on the row.
    const bare: ConsoleTarget = {
      clusterID: target.clusterID,
      node: target.node,
      type: target.type,
      label: target.label,
      ...(target.vmid !== undefined ? { vmid: target.vmid } : {}),
    };
    return {
      ok: true,
      checked: { target: bare, guestName: undefined, unconfirmed: true },
    };
  }
  if (
    guest.data.vmid !== target.vmid ||
    guest.data.cluster_id !== target.clusterID
  ) {
    return notice(
      "This console link does not match its guest",
      "It names one guest and opens another. Open the console again from Nexara.",
    );
  }
  return {
    ok: true,
    checked: { target, guestName: guest.data.name, unconfirmed: false },
  };
}

/**
 * The link's console, checked against the server before anything of it is
 * shown or dialled (judge) — on the first answers only. A pass is kept: a
 * later answer that no longer lists its node (a guest migrated to one newer
 * than the list) or a changed guest row must not take down a console that is
 * up.
 */
function CheckedConsoleSession({ target }: { target: ConsoleTarget }) {
  const nodes = useClusterNodes(target.clusterID);
  const guest = useVM(
    target.clusterID,
    target.resourceId ?? "",
    target.kind ?? "vm",
  );
  const [passed, setPassed] = useState<CheckedTarget | null>(null);
  const verdict: Verdict =
    passed !== null
      ? { ok: true, checked: passed }
      : judge(target, nodes, guest);
  // Kept from the render that passed (state derived during render, which
  // React re-renders at once, before anything of it is committed).
  if (passed === null && verdict.ok) setPassed(verdict.checked);

  if (!verdict.ok) return verdict.view;
  return (
    <ConsoleSessionGate
      target={verdict.checked.target}
      guestName={verdict.checked.guestName}
      unconfirmed={verdict.checked.unconfirmed}
    />
  );
}

/** Which console this browser tab runs, and for whom (see ConsoleSessionGate). */
const OWNER_KEY = "nexara-console-owner";

/** Whether `user` ran this console in this tab; null when nobody ran one. */
function ranHere(user: string, identity: string): boolean | null {
  try {
    const raw = sessionStorage.getItem(OWNER_KEY);
    if (raw === null) return null;
    const owner = JSON.parse(raw) as { user?: unknown; console?: unknown };
    return owner.user === user && owner.console === identity;
  } catch {
    return false;
  }
}

/**
 * Dials only a console a Nexara launcher opened in this tab for this user (a
 * launch, console-launch.ts) — and again after a reload or a restore, which
 * sessionStorage, per tab, remembers. Anything else asks first: a link from
 * another site, a pasted or bookmarked one, one a guest printed in a terminal,
 * a tab sent elsewhere by the page that opened it, or the login page bringing
 * whoever signs in next back here. Each would dial a console, audited as this
 * user, that they did not choose.
 */
function ConsoleSessionGate({
  target,
  guestName,
  unconfirmed,
}: {
  target: ConsoleTarget;
  guestName: string | undefined;
  unconfirmed: boolean;
}) {
  const user = useAuthStore((s) => s.user?.id ?? "");
  const identity = consoleIdentity(target);
  const location = useLocation();
  const navigate = useNavigate();
  // A guest whose row is gone or hidden may not be the one this tab ran: only
  // a launch made for it just now dials that without asking.
  const [connect, setConnect] = useState(() =>
    unconfirmed
      ? takeLaunch(location.hash, user, identity)
      : (ranHere(user, identity) ?? takeLaunch(location.hash, user, identity)),
  );
  // The launch is spent once the gate has judged it: off the address, so that
  // a copy of it carries none.
  useEffect(() => {
    if (location.hash === "") return;
    void navigate({ search: location.search }, { replace: true });
  }, [location.hash, location.search, navigate]);
  useEffect(() => {
    if (!connect) return;
    try {
      sessionStorage.setItem(
        OWNER_KEY,
        JSON.stringify({ user, console: identity }),
      );
    } catch {
      // Not remembered: a reload asks again.
    }
  }, [connect, user, identity]);

  if (!connect) {
    const label = consoleLabel(target, guestName);
    const where =
      target.type === "node_shell"
        ? `node ${target.node}`
        : `VMID ${String(target.vmid)} on ${target.node}`;
    return (
      <ConsoleSessionNotice
        title="Open this console?"
        description={
          unconfirmed
            ? `${label} (${where}) could not be matched to its guest record, so it waits for you to connect.`
            : `${label} (${where}) was not opened for you from Nexara, so it waits for you to connect.`
        }
      >
        <Button
          size="sm"
          onClick={() => {
            setConnect(true);
          }}
        >
          Connect
        </Button>
      </ConsoleSessionNotice>
    );
  }
  return <ConsoleSession target={target} />;
}

function ConsoleSession({ target }: { target: ConsoleTarget }) {
  // Seeded once: the URL changes later only to follow the session's own node.
  const [store] = useState(() => createConsoleSessionStore(target));
  return (
    <ConsoleStoreContext value={store}>
      <ConsoleSessionTab />
    </ConsoleStoreContext>
  );
}

function ConsoleSessionTab() {
  const tab = useConsoleSession((s) => s.tabs[0]);
  return tab === undefined ? null : <ConsoleSessionScreen tab={tab} />;
}

function ConsoleSessionScreen({ tab }: { tab: ConsoleTab }) {
  const reconnectTab = useConsoleSession((s) => s.reconnectTab);
  const clusterIds = useMemo(() => [tab.clusterID], [tab.clusterID]);
  useLiveUpdates(clusterIds);

  // The guest's name, from the query its power sync runs too. The clusters
  // list names the cluster; its 30 s poll, while the tab is shown, also meets
  // a revoked session once the access token runs out, as the app's do.
  const { data: guest } = useVM(
    tab.clusterID,
    tab.resourceId ?? "",
    tab.kind ?? "vm",
  );
  const { data: clusters } = useClusters();
  const clusterName = clusters?.find((c) => c.id === tab.clusterID)?.name;
  const label = consoleLabel(
    tab,
    guest !== undefined && guest.vmid === tab.vmid ? guest.name : undefined,
  );
  const viewTab = useMemo(() => ({ ...tab, label }), [tab, label]);

  // A migrated guest's new node goes into the URL, so a reload dials it.
  const [params, setParams] = useSearchParams();
  useEffect(() => {
    if (params.get("node") === tab.node) return;
    const next = new URLSearchParams(params);
    next.set("node", tab.node);
    setParams(next, { replace: true });
  }, [tab.node, params, setParams]);

  const brandingQuery = useBranding();
  const appTitle = useBrandingStore((s) => s.appTitle);
  // Both this and BrandingLoader watch the branding query, so they render in
  // one commit, and BrandingLoader's effect — it writes the branded title,
  // where the installation has one, on every load — runs first: its subtree
  // comes earlier. This one lists the query's data, so it runs after it.
  useEffect(() => {
    document.title = `${label} · ${appTitle}`;
  }, [label, appTitle, brandingQuery.data]);
  // The title names the guest, so it goes with the page: the login page, and
  // whoever signs in on it next, would keep it otherwise.
  useEffect(
    () => () => {
      document.title = useBrandingStore.getState().appTitle;
    },
    [],
  );

  const shown = useShownOnce();
  const fullscreen = useDocumentFullscreen();
  const isMobile = useIsMobile();
  const isVnc = tab.type === "vm_vnc" || tab.type === "ct_vnc";
  const headerButton = isMobile ? "h-8 w-8 p-0" : "h-7 px-2";

  return (
    <div className="flex h-screen flex-col bg-background supports-[height:100dvh]:h-dvh">
      <header className="flex h-10 shrink-0 items-center gap-2 border-b bg-card px-3">
        <StatusIcon status={tab.status} />
        <span className="sr-only">{STATUS_TEXT[tab.status]}</span>
        <h1 className="min-w-0 truncate text-sm font-medium">{label}</h1>
        <span className="hidden min-w-0 truncate text-xs text-muted-foreground sm:inline">
          {clusterName !== undefined
            ? `${clusterName} / ${tab.node}`
            : tab.node}
        </span>
        <div className="ml-auto flex shrink-0 items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            className={headerButton}
            title="Reconnect"
            aria-label={`Reconnect ${label}`}
            onClick={() => {
              reconnectTab(tab.id);
            }}
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </Button>
          {fullscreen.supported && (
            <Button
              variant="ghost"
              size="sm"
              className={headerButton}
              title={fullscreen.active ? "Exit fullscreen" : "Fullscreen"}
              onClick={fullscreen.toggle}
            >
              {fullscreen.active ? (
                <Minimize className="h-3.5 w-3.5" />
              ) : (
                <Maximize className="h-3.5 w-3.5" />
              )}
            </Button>
          )}
        </div>
      </header>

      {/* min-h-0 so the console can shrink to the space left: an xterm's
          intrinsic rows would otherwise push this past the viewport. */}
      <main className="relative min-h-0 flex-1 bg-[#1a1b26]">
        {isVnc ? (
          <VNCViewer tab={viewTab} visible={shown} windowControls={false} />
        ) : (
          <Terminal tab={viewTab} visible={shown} />
        )}
      </main>

      <TaskProgressDialog />
      <AppToaster />
    </div>
  );
}

/**
 * Whether the page has been shown yet. A console opened behind this tab
 * (Ctrl/⌘-click) or restored with the browser dials when it is first looked
 * at, as a floating console's tab does: every dial mints an audited token and
 * holds a Proxmox console.
 */
function useShownOnce(): boolean {
  const [shown, setShown] = useState(
    () => document.visibilityState === "visible",
  );
  useEffect(() => {
    if (shown) return;
    function onChange() {
      if (document.visibilityState === "visible") setShown(true);
    }
    document.addEventListener("visibilitychange", onChange);
    return () => {
      document.removeEventListener("visibilitychange", onChange);
    };
  }, [shown]);
  return shown;
}

/**
 * Fullscreen for the whole document, not the console alone: menus, dialogs and
 * toasts render into body, and would vanish behind a fullscreen element that
 * is not their ancestor. Unsupported (iPhone, Safari before 16.4): no button.
 */
function useDocumentFullscreen() {
  const [active, setActive] = useState(() =>
    Boolean(document.fullscreenElement),
  );
  useEffect(() => {
    function onChange() {
      setActive(Boolean(document.fullscreenElement));
    }
    document.addEventListener("fullscreenchange", onChange);
    return () => {
      document.removeEventListener("fullscreenchange", onChange);
    };
  }, []);
  const toggle = useCallback(() => {
    const change = document.fullscreenElement
      ? document.exitFullscreen()
      : document.documentElement.requestFullscreen();
    change.catch(() => {
      // Refused (no user activation, or a permissions policy): nothing changed.
    });
  }, []);
  // Absent, not false, where there is no unprefixed API.
  const supported =
    (document as { fullscreenEnabled?: boolean }).fullscreenEnabled === true;
  return { supported, active, toggle };
}

function ConsoleSessionLoading() {
  return (
    <div className="flex h-screen items-center justify-center bg-background supports-[height:100dvh]:h-dvh">
      <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
    </div>
  );
}

function ConsoleSessionNotice({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children?: ReactNode;
}) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-3 bg-background p-6 text-center supports-[height:100dvh]:h-dvh">
      <TerminalSquare className="h-10 w-10 text-muted-foreground opacity-50" />
      <h1 className="text-base font-semibold">{title}</h1>
      <p className="max-w-sm text-sm text-muted-foreground">{description}</p>
      <div className="flex flex-wrap justify-center gap-2">
        {children}
        {/* A page load, not an in-app navigation: this tab becomes the app. */}
        <Button variant="outline" size="sm" asChild>
          <a href="/">Open Nexara</a>
        </Button>
      </div>
    </div>
  );
}
