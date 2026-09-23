/// <reference types="vite/client" />
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, Outlet, useBlocker } from "react-router-dom";
import { AppRoot } from "@/components/AppRoot";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { useAuthStore } from "@/stores/auth-store";
import { usePBSKeyStore } from "@/stores/pbs-key-store";
import type { User } from "@/types/api";
import { PendingPBSKeyNavigationGuard } from "./PendingPBSKey";

// Synthetic key files whose data members are markers, with full-length
// placeholder fingerprints.
const KEY =
  '{"kdf":null,"data":"CANARY-pending-key-one","fingerprint":"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"}';
const SECOND_KEY =
  '{"kdf":null,"data":"CANARY-pending-key-two","fingerprint":"11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00"}';
const THIRD_KEY =
  '{"kdf":null,"data":"CANARY-pending-key-three","fingerprint":"99:88:77:66:55:44:33:22:11:00:ff:ee:dd:cc:bb:aa:99:88:77:66:55:44:33:22:11:00:ff:ee:dd:cc:bb:aa"}';

const OWNER: User = {
  id: "user-owner",
  email: "owner@example.com",
  display_name: "Owner",
  role: "user",
};
const SOMEONE_ELSE: User = {
  id: "user-other",
  email: "other@example.com",
  display_name: "Other",
  role: "user",
};

// Two clusters, each with a storage called store01.
const CLUSTER_ONE = { id: "c1", name: "cluster01" };
const CLUSTER_TWO = { id: "c2", name: "cluster02" };

function signIn(user: User) {
  act(() => {
    useAuthStore.setState({ user, permissions: [], isAuthenticated: true });
  });
}

/** The session ending the way it does when a refresh fails. */
function sessionExpires() {
  act(() => {
    useAuthStore.getState().clearAuth();
  });
}

// AppShell as far as the key is concerned: the page, then the navigation
// guard after it, in AppShell's order.
function Shell() {
  return (
    <>
      <Outlet />
      <PendingPBSKeyNavigationGuard />
    </>
  );
}

/** A page with a navigation prompt of its own, as an unsaved form might. */
function PageWithItsOwnPrompt() {
  const blocker = useBlocker(true);
  return <p>form page: {blocker.state}</p>;
}

/**
 * The app's routes as far as the key is concerned — the real ProtectedRoute
 * around the shell, and a login page beside it — on the given page, with
 * somewhere to go Back to.
 */
function renderApp(start = "/storage") {
  const router = createMemoryRouter(
    [
      { path: "/login", element: <p>login page</p> },
      {
        element: <ProtectedRoute />,
        children: [
          {
            path: "/",
            element: <Shell />,
            children: [
              { path: "storage", element: <p>storage page</p> },
              { path: "form", element: <PageWithItsOwnPrompt /> },
              { path: "elsewhere", element: <p>elsewhere page</p> },
            ],
          },
        ],
      },
    ],
    { initialEntries: ["/elsewhere", start], initialIndex: 1 },
  );
  const user = userEvent.setup();
  render(<AppRoot router={router} />);
  return { router, user };
}

function deliver(
  keyText: string,
  {
    storage = "store01",
    cluster = CLUSTER_ONE,
    owner = OWNER.id,
  }: {
    storage?: string;
    cluster?: { id: string; name: string };
    owner?: string;
  } = {},
) {
  act(() => {
    usePBSKeyStore.getState().deliver({
      owner,
      cluster: cluster.id,
      clusterName: cluster.name,
      storage,
      keyText,
    });
  });
}

async function saveAndClose(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByLabelText("I have saved this key"));
  await user.click(screen.getByRole("button", { name: "Done" }));
}

/** Whether leaving the page now would ask the browser to confirm. */
function leavingAsks(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}

/** Everything in web storage, as one string to search. */
function webStorage(): string {
  const dump = (store: Storage) =>
    Array.from({ length: store.length }, (_, i) => {
      const k = store.key(i) ?? "";
      return `${k}=${store.getItem(k) ?? ""}`;
    }).join("\n");
  return dump(localStorage) + dump(sessionStorage);
}

// The mounting sites, read as source: rendering the real router or AppShell
// in a unit test would pull in the whole application.
const mountingSites: Record<string, string> = import.meta.glob(
  ["/src/App.tsx", "/src/components/layout/AppShell.tsx"],
  { eager: true, query: "?raw", import: "default" },
);

describe("the pending PBS key", () => {
  beforeEach(() => {
    usePBSKeyStore.setState({ pending: [] });
    useAuthStore.setState({
      user: OWNER,
      permissions: [],
      isAuthenticated: true,
      isInitialized: true,
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("stays on screen and holds navigation until Done, then leaves nothing behind", async () => {
    const { router, user } = renderApp();
    deliver(KEY);
    expect(await screen.findByRole("alertdialog")).toBeInTheDocument();

    // Back, and a jump elsewhere — a search result, say.
    await act(async () => {
      await router.navigate(-1);
    });
    await act(async () => {
      await router.navigate("/elsewhere");
    });

    expect(router.state.location.pathname).toBe("/storage");
    expect(screen.getByText("storage page")).toBeInTheDocument();
    expect(screen.getByLabelText("Encryption key")).toHaveValue(KEY);
    expect(webStorage()).not.toContain("CANARY-pending-key-one");

    await saveAndClose(user);

    expect(usePBSKeyStore.getState().pending).toEqual([]);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    // Nothing holds navigation any more.
    await act(async () => {
      await router.navigate(-1);
    });
    expect(router.state.location.pathname).toBe("/elsewhere");
  });

  it("leaves a page's own navigation prompt working while no key is waiting", async () => {
    // Loaded first, the page registers its blocker before the guard would.
    const { router } = renderApp("/form");
    expect(await screen.findByText("form page: unblocked")).toBeInTheDocument();

    await act(async () => {
      await router.navigate("/elsewhere");
    });

    // react-router consults only the blocker registered last; with no key
    // waiting the guard has registered none, so the page's own one decided.
    expect(router.state.location.pathname).toBe("/form");
    expect(screen.getByText("form page: blocked")).toBeInTheDocument();
  });

  it("hides a waiting key on the login page once the session ends, but keeps it and the leave prompt", async () => {
    const { router } = renderApp();
    deliver(KEY);
    await screen.findByRole("alertdialog");
    // Shown, it holds the page under it, and the key is in the document.
    expect(document.body.style.pointerEvents).toBe("none");
    expect(document.body.innerHTML).toContain("CANARY-pending-key-one");

    sessionExpires();

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(document.body.innerHTML).not.toContain("CANARY-pending-key-one");
    // Nothing modal left over the login page.
    expect(document.body.style.pointerEvents).not.toBe("none");
    expect(usePBSKeyStore.getState().pending).toHaveLength(1);
    expect(leavingAsks()).toBe(true);
  });

  it("shows it again, unconfirmed, when its owner signs back in", async () => {
    const { user } = renderApp();
    deliver(KEY);
    await screen.findByRole("alertdialog");
    await user.click(screen.getByLabelText("I have saved this key"));
    sessionExpires();
    await screen.findByText("login page");

    signIn(OWNER);

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(KEY);
    expect(screen.getByLabelText("I have saved this key")).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Done" })).toBeDisabled();
    await saveAndClose(user);
    expect(usePBSKeyStore.getState().pending).toEqual([]);
  });

  it("drops it when someone else signs in", async () => {
    renderApp();
    deliver(KEY);
    await screen.findByRole("alertdialog");
    sessionExpires();
    await screen.findByText("login page");

    signIn(SOMEONE_ELSE);

    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(document.body.innerHTML).not.toContain("CANARY-pending-key-one");
    expect(usePBSKeyStore.getState().pending).toEqual([]);
    expect(leavingAsks()).toBe(false);
    // Nor does it come back for its owner: it is gone from this page.
    sessionExpires();
    signIn(OWNER);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("never queues a key whose answer came into someone else's session", () => {
    renderApp();
    signIn(SOMEONE_ELSE);

    deliver(KEY, { owner: OWNER.id });

    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(usePBSKeyStore.getState().pending).toEqual([]);
    expect(leavingAsks()).toBe(false);
  });

  it("asks the browser to confirm leaving only while a key is waiting", async () => {
    const added = vi.spyOn(window, "addEventListener");
    const removed = vi.spyOn(window, "removeEventListener");
    const registrations = () =>
      added.mock.calls.filter(([type]) => type === "beforeunload");

    const { user } = renderApp();
    expect(registrations()).toHaveLength(0);
    expect(leavingAsks()).toBe(false);

    deliver(KEY);
    await screen.findByRole("alertdialog");
    expect(registrations()).toHaveLength(1);
    expect(leavingAsks()).toBe(true);

    await saveAndClose(user);

    const handler = registrations()[0]?.[1];
    expect(
      removed.mock.calls.some(
        ([type, fn]) => type === "beforeunload" && fn === handler,
      ),
    ).toBe(true);
    expect(leavingAsks()).toBe(false);
  });

  it("is mounted where the app runs: the dialog beside the router, the guard in the shell", () => {
    // Both are tested above through a stand-in shell; these are the places
    // the application itself puts them.
    expect(mountingSites["/src/App.tsx"]).toMatch(
      /^\s*return <AppRoot router=\{router\} \/>;$/m,
    );
    expect(mountingSites["/src/components/layout/AppShell.tsx"]).toMatch(
      /^\s*<PendingPBSKeyNavigationGuard \/>$/m,
    );
  });

  it("shows queued keys one at a time, each with its box unticked", async () => {
    const { user } = renderApp();
    deliver(KEY);
    deliver(SECOND_KEY, { storage: "store02" });

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(KEY);
    await saveAndClose(user);

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(
      SECOND_KEY,
    );
    expect(screen.getByRole("button", { name: "Done" })).toBeDisabled();
    await saveAndClose(user);

    expect(usePBSKeyStore.getState().pending).toEqual([]);
  });

  it("names each key by its short fingerprint, and says which of a storage's keys is current", async () => {
    const { user } = renderApp();
    deliver(KEY);
    deliver(THIRD_KEY, { storage: "store02" });
    deliver(SECOND_KEY);

    // The first key for store01, replaced by the second.
    const first = await screen.findByRole("alertdialog");
    expect(screen.getByText("aa:bb:cc:dd:ee:ff:00:11")).toHaveAttribute(
      "title",
      "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99",
    );
    expect(first).toHaveTextContent(
      "Not the current key. Another key was generated for store01 on cluster cluster01 after this one, and only the last one generated is current — fingerprint 11:22:33:44:55:66:77:88. It is shown after this one.",
    );
    // Proxmox no longer keeps this one as the storage's key.
    expect(first).not.toHaveTextContent(
      "Proxmox keeps it only on this cluster",
    );
    await saveAndClose(user);

    // The one key for store02: current, though a key for store01 follows it.
    const second = await screen.findByRole("alertdialog");
    expect(screen.getByText("99:88:77:66:55:44:33:22")).toBeInTheDocument();
    expect(second).not.toHaveTextContent("Not the current key");
    await saveAndClose(user);

    // The last key for store01: the current one.
    const third = await screen.findByRole("alertdialog");
    expect(screen.getByText("11:22:33:44:55:66:77:88")).toBeInTheDocument();
    expect(third).not.toHaveTextContent("Not the current key");
    expect(third).toHaveTextContent("Proxmox keeps it only on this cluster");
  });

  it("tells a same-named storage on another cluster apart, and names each key's cluster", async () => {
    const { user } = renderApp();
    deliver(KEY, { cluster: CLUSTER_ONE });
    deliver(SECOND_KEY, { cluster: CLUSTER_TWO });

    // Two storages, one key each: both current.
    expect(await screen.findByLabelText("Encryption key")).toHaveValue(KEY);
    const first = screen.getByRole("alertdialog");
    expect(first).toHaveTextContent("for store01 on cluster cluster01.");
    expect(first).not.toHaveTextContent("cluster02");
    expect(first).not.toHaveTextContent("Not the current key");
    expect(first).not.toHaveTextContent(".enc.old");
    expect(first).toHaveTextContent("Proxmox keeps it only on this cluster");
    await saveAndClose(user);

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(
      SECOND_KEY,
    );
    const second = screen.getByRole("alertdialog");
    expect(second).toHaveTextContent("for store01 on cluster cluster02.");
    expect(second).not.toHaveTextContent("cluster01");
    expect(second).not.toHaveTextContent("Not the current key");
  });

  it("names the last of several keys for one storage as the current one", async () => {
    const { user } = renderApp();
    deliver(KEY);
    deliver(SECOND_KEY);
    deliver(THIRD_KEY);
    const current =
      "only the last one generated is current — fingerprint 99:88:77:66:55:44:33:22.";

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(KEY);
    expect(screen.getByRole("alertdialog")).toHaveTextContent(current);
    await saveAndClose(user);

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(
      SECOND_KEY,
    );
    expect(screen.getByRole("alertdialog")).toHaveTextContent(current);
    await saveAndClose(user);

    expect(await screen.findByLabelText("Encryption key")).toHaveValue(
      THIRD_KEY,
    );
    expect(screen.getByRole("alertdialog")).not.toHaveTextContent(
      "Not the current key",
    );
  });

  it("says a key was replaced even when the one after it did not come back", async () => {
    renderApp();
    deliver(KEY);
    deliver("");

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(
      "Another key was generated for store01 on cluster cluster01 after this one, and only the last one generated is current. It is shown after this one.",
    );
  });
});
