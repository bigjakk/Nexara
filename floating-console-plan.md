# Floating Console — iLO/iDRAC-Style Remote Console for ProxDash

## Goal

Replace the current route-locked `/console` page with a **persistent floating console window** that stays visible across all pages, mimicking the remote console experience of HPE iLO and Dell iDRAC. Users can open VNC/terminal sessions to VMs, containers, and node shells that remain connected while navigating dashboards, inventory, audit logs, etc.

## What Exists Today

### Frontend Components
| File | Purpose |
|------|---------|
| `frontend/src/features/console/pages/ConsolePage.tsx` | Full-page `/console` route — renders `<ConsolePanel />` |
| `frontend/src/features/console/components/ConsolePanel.tsx` | Container: tab bar + active VNC/terminal viewer |
| `frontend/src/features/console/components/ConsoleTabBar.tsx` | Tab bar with status icons, reconnect, close buttons |
| `frontend/src/features/console/components/VNCViewer.tsx` | Full-page noVNC viewer (tabbed console) |
| `frontend/src/features/console/components/VNCToolbar.tsx` | Toolbar: Ctrl+Alt+Del, fullscreen, paste, scale/resize |
| `frontend/src/features/console/components/Terminal.tsx` | xterm.js terminal for serial/shell/attach consoles |
| `frontend/src/features/console/components/QuickConnect.tsx` | Dialog to select cluster/node/VM and open a console |
| `frontend/src/features/vms/components/InlineVNCViewer.tsx` | Embedded VNC preview on VM detail page (60vh) |
| `frontend/src/stores/console-store.ts` | Zustand store: tabs[], activeTabId, addTab/removeTab/reconnect |
| `frontend/src/features/console/types/console.ts` | Types: ConsoleType, ConsoleStatus, ConsoleTab |

### Backend WebSocket Handlers
| Endpoint | Handler | Purpose |
|----------|---------|---------|
| `/ws/vnc` | `internal/ws/vnc.go` | Proxies noVNC WebSocket to Proxmox vncwebsocket |
| `/ws/console` | `internal/ws/console.go` | Proxies xterm.js to Proxmox termproxy/vncwebsocket |
| `/ws` | `internal/ws/server.go` | Pub/sub hub for metrics, alerts, events |

### Proxmox Client Methods (`internal/proxmox/console.go`)
- `VMVNCProxy(node, vmid)` → `/nodes/{node}/qemu/{vmid}/vncproxy?websocket=1`
- `CTVNCProxy(node, vmid)` → `/nodes/{node}/lxc/{vmid}/vncproxy?websocket=1`
- `NodeVNCProxy(node)` → `/nodes/{node}/vncproxy?websocket=1`
- `NodeTermProxy(node)` → `/nodes/{node}/termproxy`
- `VMTermProxy(node, vmid)` → serial console via termproxy
- `CTTermProxy(node, vmid)` → container attach via termproxy
- `DialVNCWebSocket(node, ticket, port, path)` → WebSocket connection to Proxmox
- `DialTerminal(node, ticket, port, path, user)` → terminal WebSocket with handshake

### Dependencies
- `@novnc/novnc`: ^1.5.0
- `@xterm/xterm`: ^6.0.0
- `@xterm/addon-fit`: ^0.11.0
- `@xterm/addon-web-links`: ^0.12.0

### WebSocket URL Patterns
- VNC: `ws(s)://{host}/ws/vnc?token=<JWT>&cluster_id=<uuid>&node=<name>&vmid=<id>&type=<qemu|lxc>`
- Terminal: `ws(s)://{host}/ws/console?token=<JWT>&cluster_id=<uuid>&node=<name>&type=<node_shell|vm_serial|ct_attach>&vmid=<id>`
- Auth: JWT from localStorage passed as `token` query parameter

### Authentication Flow
1. Frontend reads `localStorage.getItem("access_token")`
2. Token passed as query param in WebSocket URL
3. Backend `authMiddleware` validates JWT before WebSocket upgrade
4. For VNC: Proxmox returns a ticket, sent to browser as `{"type":"connected","password":"<ticket>"}`
5. noVNC RFB uses ticket as VNC credentials
6. For terminal: `{"type":"connected"}` signals ready, data flows as binary frames

---

## Phase 1: Floating Window + Existing Console

**Goal:** Move the console from a dedicated page into a floating, draggable, resizable window that persists across navigation. No new features — just relocate existing functionality.

### 1.1 Extend Console Store

**Modify: `frontend/src/stores/console-store.ts`**

Add window state fields:

```typescript
interface ConsoleState {
  // Existing
  tabs: ConsoleTab[];
  activeTabId: string | null;
  // New — window state
  windowMode: "hidden" | "minimized" | "floating" | "maximized";
  windowPosition: { x: number; y: number };
  windowSize: { width: number; height: number };
}

interface ConsoleActions {
  // Existing actions...
  // New
  setWindowMode: (mode: WindowMode) => void;
  setWindowPosition: (pos: { x: number; y: number }) => void;
  setWindowSize: (size: { width: number; height: number }) => void;
  openConsole: () => void;  // sets mode to "floating" if hidden
}
```

- Default `windowMode`: `"hidden"` (no console visible)
- Default `windowSize`: `{ width: 800, height: 500 }`
- Default `windowPosition`: `{ x: window.innerWidth - 820, y: window.innerHeight - 520 }` (bottom-right)
- Persist `windowMode`, `windowPosition`, `windowSize` to localStorage alongside tabs
- When `addTab()` is called and `windowMode === "hidden"`, auto-set to `"floating"`

### 1.2 Create FloatingConsole Component

**New: `frontend/src/features/console/components/FloatingConsole.tsx`**

The outer window shell. This is the main new component.

**Three render modes:**

#### Hidden (`windowMode === "hidden"`)
- Render nothing (but keep the component mounted so store subscriptions stay active)

#### Minimized (`windowMode === "minimized"`)
- Small pill/bar fixed to bottom-right corner: ~250px wide
- Shows: console icon, active tab label, tab count badge, expand button
- Example: `[▶ VNC: VM-100 (3)] [↗]`
- Click anywhere → restore to `"floating"`
- CSS: `fixed bottom-4 right-4 z-40`

#### Floating (`windowMode === "floating"`)
- Draggable window at `windowPosition`, sized to `windowSize`
- CSS: `fixed z-40` with inline `left/top/width/height`
- Components inside:
  - **Title bar** — drag handle, tab label, minimize/maximize/close buttons
  - **Tab bar** — reuse existing `ConsoleTabBar` (or simplified inline version)
  - **Content area** — active VNC/terminal viewer
- Minimum size: 400x300
- Constrain to viewport (can't drag fully off-screen)

#### Maximized (`windowMode === "maximized"`)
- Nearly fullscreen: `fixed inset-4 z-40` (16px margin all around)
- Same content as floating, just larger
- Title bar shows restore button instead of maximize

**Drag implementation (no library):**
```typescript
// onPointerDown on title bar → capture pointer
// onPointerMove → update windowPosition (delta from start)
// onPointerUp → release
// Same pattern already used by TaskLogPanel resize handle
```

**Resize implementation:**
- 8px invisible resize handles on all four edges + four corners
- Same pointer capture pattern as drag
- Update `windowSize` and optionally `windowPosition` (for top/left resize)
- `cursor: nw-resize` etc. on hover

**Close behavior:**
- Close button → `setWindowMode("hidden")` (doesn't remove tabs — they persist)
- Tabs can still be closed individually via tab bar X buttons
- If last tab is removed → auto-hide window

### 1.3 Mount FloatingConsole in AppShell

**Modify: `frontend/src/components/layout/AppShell.tsx`**

```tsx
import { FloatingConsole } from "@/features/console/components/FloatingConsole";

export function AppShell() {
  return (
    <div className="flex h-screen">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        <header>...</header>
        <main className="flex-1 overflow-auto p-6">
          <Outlet />
        </main>
        <TaskLogPanel />
        <TaskProgressDialog />
      </div>
      <FloatingConsole />  {/* Portal to body, persists across routes */}
    </div>
  );
}
```

### 1.4 Update Launch Points

All console launch points should open the floating console instead of navigating to `/console`:

**VM Detail Page (`VMDetailPage.tsx`):**
- Change `openConsole()` function: instead of `navigate("/console")`, call `addTab(...)` + `openConsole()`
- The floating window appears/focuses automatically

**Context Menu (`VMContextMenu.tsx`):**
- Add "Console" action to the context menu
- Calls `addTab(...)` from the console store

**QuickConnect dialog:**
- Already calls `addTab()` — just needs to also call `openConsole()` to show the window

**Sidebar / inventory page console buttons:**
- Same pattern: `addTab()` + `openConsole()`

### 1.5 Handle the /console Route

**Modify: `frontend/src/features/console/pages/ConsolePage.tsx`**

Two options:
- **Option A (recommended):** Keep the route but have it auto-maximize the floating console and show a message: "Console is running in the floating window. Click here to focus it."
- **Option B:** Remove the route entirely, redirect to `/` and open floating console in maximized mode

### 1.6 Refactor VNC/Terminal Viewers

The existing `VNCViewer` and `Terminal` components are designed for the full-page console. They should work inside the floating window with minimal changes:

- **VNCViewer.tsx** — already takes a `tab` prop and `visible` boolean. The noVNC canvas auto-scales via `scaleViewport`. Just needs the container to have defined dimensions (which the floating window provides).
- **Terminal.tsx** — already uses `FitAddon` which auto-sizes to container via `ResizeObserver`. Should work as-is.

Key concern: **noVNC canvas lifecycle.** When minimized, the canvas is hidden (`display: none` or `visibility: hidden`). noVNC may not render correctly when restored. Two approaches:
- Keep the canvas rendered but `visibility: hidden` (preserves WebGL context)
- On restore, call `rfb.scaleViewport = true` to force a re-layout

Test both during implementation.

### 1.7 z-index and Interaction Layering

```
z-50  — Radix dialogs (ContextMenu, Dialog, DropdownMenu) — portaled to body
z-40  — FloatingConsole
z-30  — TaskLogPanel (fixed bottom)
z-0   — Normal page content
```

FloatingConsole should be below dialogs (so you can still open Clone/Migrate/Destroy dialogs over it) but above everything else.

### Phase 1 Files Summary

| File | Action |
|------|--------|
| `frontend/src/stores/console-store.ts` | Modify — add window state |
| `frontend/src/features/console/components/FloatingConsole.tsx` | **Create** — the floating window |
| `frontend/src/components/layout/AppShell.tsx` | Modify — mount FloatingConsole |
| `frontend/src/features/vms/pages/VMDetailPage.tsx` | Modify — open floating instead of navigate |
| `frontend/src/features/vms/components/VMContextMenu.tsx` | Modify — add Console action |
| `frontend/src/features/console/components/QuickConnect.tsx` | Modify — call openConsole() |
| `frontend/src/features/console/pages/ConsolePage.tsx` | Modify — redirect/focus floating |

---

## Phase 2: Enhanced Toolbar (iLO/iDRAC Parity)

**Goal:** Add the toolbar features that make this feel like a real enterprise BMC console.

### 2.1 Power Controls Dropdown

**Modify: `frontend/src/features/console/components/VNCToolbar.tsx`**

Add a power button dropdown to the VNC toolbar. When a console tab is active and connected to a VM/CT, show:

```
[Power ▾]
  ├── Start        (if stopped/suspended)
  ├── Shutdown     (if running)
  ├── Reboot       (if running)
  ├── Stop         (if running, confirm)
  ├── Reset        (if running, VM only, confirm)
  ├── Suspend      (if running)
  └── Resume       (if suspended)
```

**Implementation:**
- Reuse `lifecycleActions` from `frontend/src/features/vms/lib/vm-action-defs.tsx` (already extracted in context menu work)
- Use `useVMAction()` mutation hook
- Need the active tab's VM status — fetch via `useVM(clusterId, vmId, kind)` or pass through store
- Use `DropdownMenu` from shadcn/ui (already installed)
- For confirmations: reuse the same pattern as VMContextDialogs (small confirmation dialog)

**Challenge:** The console store tab only has `clusterID`, `node`, `vmid`, `type` — it doesn't have `resourceId` (DB UUID) or `status`. Options:
- **Option A:** Add `resourceId` and `status` to `ConsoleTab` type (set when creating tab)
- **Option B:** Look up the VM by vmid using the existing `useClusterVMs` data
- **Recommended:** Option A — extend ConsoleTab with optional `resourceId`, `kind`, `status` fields. Update all `addTab()` call sites.

### 2.2 Keyboard Macros Dropdown

**Add to VNC toolbar:**

```
[Keyboard ▾]
  ├── Ctrl+Alt+Del
  ├── ─────────────
  ├── Ctrl+Alt+F1
  ├── Ctrl+Alt+F2
  ├── ...
  ├── Ctrl+Alt+F12
  ├── ─────────────
  ├── Alt+Tab
  ├── Alt+F4
  ├── Print Screen
  └── SysRq
```

**Implementation using noVNC RFB API:**
```typescript
// noVNC's RFB exposes sendKey(keysym, code, down)
// For key combos, send key-down events then key-up events

function sendCtrlAltFn(rfb: RFB, n: number) {
  const keysyms = {
    ctrl: 0xffe3,   // XK_Control_L
    alt: 0xffe9,    // XK_Alt_L
    f1: 0xffbe,     // XK_F1 (add n-1 for F2-F12)
  };
  rfb.sendKey(keysyms.ctrl, "ControlLeft", true);
  rfb.sendKey(keysyms.alt, "AltLeft", true);
  rfb.sendKey(keysyms.f1 + n - 1, `F${n}`, true);
  rfb.sendKey(keysyms.f1 + n - 1, `F${n}`, false);
  rfb.sendKey(keysyms.alt, "AltLeft", false);
  rfb.sendKey(keysyms.ctrl, "ControlLeft", false);
}
```

### 2.3 Screenshot Capture

**Add to toolbar:**
- Camera icon button
- Captures the noVNC canvas to PNG and downloads it

```typescript
function handleScreenshot() {
  const canvas = containerRef.current?.querySelector("canvas");
  if (!canvas) return;
  const link = document.createElement("a");
  link.download = `console-${tab.label}-${new Date().toISOString().slice(0, 19)}.png`;
  link.href = canvas.toDataURL("image/png");
  link.click();
}
```

### 2.4 Connection Info Display

Small info popover accessible from the toolbar showing:
- Connected to: `{node}` / VM `{vmid}`
- Cluster: `{clusterName}`
- Resolution: `{width}x{height}` (from noVNC)
- Connection time / duration
- Latency (if measurable)

### Phase 2 Files Summary

| File | Action |
|------|--------|
| `frontend/src/features/console/components/VNCToolbar.tsx` | Major modify — power, macros, screenshot |
| `frontend/src/features/console/types/console.ts` | Modify — extend ConsoleTab type |
| `frontend/src/stores/console-store.ts` | Modify — store extended tab fields |
| `frontend/src/features/vms/lib/vm-action-defs.tsx` | Read — reuse lifecycle actions |
| Various addTab() call sites | Modify — pass resourceId/kind/status |

---

## Phase 3: Virtual Media (ISO Mount)

**Goal:** Allow mounting ISO images to a VM's CD-ROM drive directly from the console toolbar, like iDRAC's "Connect Virtual Media" feature.

### 3.1 Backend: ISO/CDROM API Endpoints

**New handler methods needed in `internal/api/handlers/vms.go`:**

1. **List available ISOs:**
   - `GET /api/v1/clusters/{id}/storage/iso` → lists ISO images from Proxmox storage
   - Proxmox API: `GET /nodes/{node}/storage/{storage}/content?content=iso`
   - Returns: `[{ volid: "local:iso/ubuntu-22.04.iso", filename: "ubuntu-22.04.iso", size: 1234567890 }]`

2. **Mount ISO to VM:**
   - `POST /api/v1/clusters/{id}/vms/{vmid}/cdrom`
   - Body: `{ "iso": "local:iso/ubuntu-22.04.iso" }` or `{ "iso": "none" }` to eject
   - Proxmox API: `PUT /nodes/{node}/qemu/{vmid}/config` with `ide2: "{volid},media=cdrom"` or `ide2: "none,media=cdrom"`

3. **Get current CDROM:**
   - Already available via VM config endpoint — parse `ide2` field

**New Proxmox client methods in `internal/proxmox/`:**
- `ListStorageContent(node, storage, contentType)` → list ISOs
- VM config update already exists

### 3.2 Frontend: Virtual Media Dropdown

**Add to VNC toolbar (VM consoles only):**

```
[💿 Media ▾]
  ├── Mount ISO...    → opens ISO picker sub-dialog
  ├── ─────────────
  ├── Eject CD-ROM    (if ISO mounted)
  └── ─────────────
  └── Current: ubuntu-22.04.iso  (info label)
```

**ISO Picker Dialog:**
- Fetches ISO list from all storage pools on the VM's node
- Searchable dropdown/combobox
- Shows filename and size
- Mount button fires the API call

### 3.3 Queries/Mutations

```typescript
// New hooks in vm-queries.ts or a new console-queries.ts
function useStorageISOs(clusterId: string, node: string);
function useMountISO();
function useEjectCDROM();
```

### Phase 3 Files Summary

| File | Action |
|------|--------|
| `internal/api/handlers/vms.go` | Modify — add CDROM mount/eject/list endpoints |
| `internal/proxmox/storage.go` or new file | Modify — add ListStorageContent method |
| `queries/` + `internal/db/` | Only if we want to audit-log ISO mounts |
| `frontend/src/features/console/components/VNCToolbar.tsx` | Modify — add media dropdown |
| `frontend/src/features/console/components/ISOPickerDialog.tsx` | **Create** — ISO selection UI |
| `frontend/src/features/console/api/console-queries.ts` | **Create** — ISO list/mount queries |

---

## Phase 4: Polish & Advanced Features (Stretch)

### 4.1 Multi-Monitor / Detach to Browser Window
- "Pop out" button that opens the console in a true browser `window.open()` popup
- Uses `BroadcastChannel` to communicate between main app and popup
- Useful for multi-monitor setups

### 4.2 Session Recording / Playback
- Record VNC frames to a buffer for playback (iLO Advanced feature)
- Store as binary blob, replay in a read-only viewer

### 4.3 Chat Between Console Sessions
- iDRAC supports multi-user chat when multiple admins view the same console
- Lower priority — ProxDash is typically single-user

### 4.4 On-Screen Keyboard
- Soft keyboard overlay for touch devices / tablet access
- Useful when physical keyboard can't send certain key combos

### 4.5 Clipboard Sync
- Bidirectional clipboard between VM guest agent and browser
- Requires QEMU guest agent with `guest-clipboard` support

---

## Implementation Order

```
Phase 1 (Floating Window)
  1.1  Extend console store with window state
  1.2  Build FloatingConsole component (drag, resize, minimize, maximize)
  1.3  Mount in AppShell
  1.4  Update all launch points (VMDetailPage, context menu, QuickConnect)
  1.5  Handle /console route (redirect or auto-maximize)
  1.6  Test VNC/terminal lifecycle across minimize/restore
  1.7  Verify z-index layering with dialogs
  → Docker rebuild & UAC test

Phase 2 (Enhanced Toolbar)
  2.1  Extend ConsoleTab type with resourceId/kind/status
  2.2  Power controls dropdown in VNC toolbar
  2.3  Keyboard macros dropdown
  2.4  Screenshot capture
  2.5  Connection info popover
  → Docker rebuild & UAC test

Phase 3 (Virtual Media)
  3.1  Backend ISO list/mount endpoints
  3.2  Proxmox client methods
  3.3  Frontend ISO picker dialog
  3.4  Media dropdown in toolbar
  → Docker rebuild & UAC test
```

## Verification Checklist

### Phase 1
- [ ] Right-click VM in sidebar → "Console" → floating window appears with VNC session
- [ ] Click "VNC" on VM detail page → floating console opens (no page navigation)
- [ ] Navigate to Dashboard while console is open → console stays visible and connected
- [ ] Minimize console → small pill in bottom-right, connection stays alive
- [ ] Restore from minimized → VNC canvas renders correctly
- [ ] Maximize → near-fullscreen view
- [ ] Drag window by title bar → repositions smoothly
- [ ] Resize from edges/corners → canvas rescales
- [ ] Open multiple tabs (VNC + shell) → tab switching works
- [ ] Close all tabs → window auto-hides
- [ ] Refresh page → tabs and window state persist (localStorage)
- [ ] Open a Dialog (Clone, Migrate) → dialog renders above console

### Phase 2
- [ ] Power dropdown shows correct actions for VM status
- [ ] Click "Shutdown" from console toolbar → VM shuts down, task appears in panel
- [ ] Keyboard macros: Ctrl+Alt+Del sends to VM (visible response in guest)
- [ ] Ctrl+Alt+F2 switches to tty2 in Linux guest
- [ ] Screenshot downloads a PNG of current console view
- [ ] Connection info shows correct node/vmid/resolution

### Phase 3
- [ ] Media dropdown lists ISOs from Proxmox storage
- [ ] Mount ISO → VM sees new CD-ROM device
- [ ] Eject → CD-ROM removed
- [ ] Boot from mounted ISO (set boot order, reboot from console)

## Key Technical Risks

1. **noVNC canvas after minimize/restore** — WebGL context may be lost. Mitigation: use `visibility: hidden` instead of `display: none`, or force reconnect on restore.

2. **Keyboard focus** — When console is floating, keyboard events must go to the VNC canvas when focused but NOT when user is typing in other inputs. noVNC's `focusOnClick` handles this, but need to verify it works in floating container context.

3. **Drag/resize performance** — Pointer events during VNC streaming. Mitigation: drag only from title bar (not content area), use `will-change: transform` for GPU-accelerated positioning.

4. **Mobile/touch** — Floating draggable windows don't work well on mobile. Consider auto-maximizing on small viewports (`< 768px`).

5. **Multiple VNC sessions** — Each VNC tab maintains its own WebSocket connection. 3-4 simultaneous sessions should be fine, but memory usage from noVNC canvases may add up. Consider disconnecting minimized VNC sessions after a timeout and reconnecting on restore.
