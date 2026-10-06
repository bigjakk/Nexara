import type { ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ApiClientError } from "@/lib/api-client";
import { queryClient as appQueryClient } from "@/lib/query-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeResponse } from "@/types/api";
import type {
  NodePCIDevice,
  NodeUSBDevice,
} from "@/features/vms/api/vm-queries";

/**
 * What the tests of the USB and PCI mapping UIs share: the Resource Mappings
 * cards (USBMappingsCard, PCIMappingsCard) and the VM dialogs that add a mapped
 * device (AddDeviceMenu). Each of those test files mocks "@/lib/api-client"
 * itself — vi.mock only reaches the file it is in — and keeps its own fixtures.
 */

export const CLUSTER = "c0000000-0000-4000-8000-000000000001";

/** Signs in a user who holds exactly these permissions. */
export function setPermissions(permissions: string[]) {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "Test User",
      role: "user",
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

/** The 409 a write answers with when the listing's digest is stale. */
export function conflict(kind: "USB" | "PCI"): ApiClientError {
  return new ApiClientError(409, {
    error: "conflict",
    message: `The cluster's ${kind} mappings changed since they were loaded`,
  });
}

export const node = (name: string, status = "online") =>
  ({ name, status }) as NodeResponse;

export function usbDevice(partial: Partial<NodeUSBDevice>): NodeUSBDevice {
  return {
    busnum: 1,
    devnum: 1,
    port: "0",
    prodid: "",
    vendid: "",
    product: "",
    manufacturer: "",
    speed: "12",
    class: 0,
    usbpath: "",
    level: 1,
    ...partial,
  };
}

export function pciDevice(partial: Partial<NodePCIDevice>): NodePCIDevice {
  return {
    id: "",
    class: "0x030000",
    device_name: "",
    vendor_name: "Example Corp",
    device: "",
    vendor: "0x1234",
    iommugroup: -1,
    ...partial,
  };
}

/**
 * Renders a card on a client of its own and returns it, for the tests that
 * invalidate or inspect a query. `cached` gives it the app's own query
 * defaults (lib/query-client.ts) so a test of what the cache may serve runs
 * against what production does; the default has no cache, so no other test
 * leans on a cached answer by accident.
 */
export function renderCard(
  card: ReactElement,
  { cached = false }: { cached?: boolean } = {},
) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: cached
        ? { ...appQueryClient.getDefaultOptions().queries, retry: false }
        : { retry: false, gcTime: 0 },
    },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>{card}</MemoryRouter>
    </QueryClientProvider>,
  );
  return queryClient;
}

/** The header row of a mapping, where its own actions sit. */
export async function mappingRow(id: string) {
  const row = (await screen.findByText(id)).closest("tr");
  if (!row) throw new Error(`no row for ${id}`);
  return row;
}
