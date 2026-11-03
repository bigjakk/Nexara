import type { NutServer, UpsDevice, UpsFeed } from "./types/ups";

/**
 * Shared NUT server and UPS fixtures for the Power tests and the components
 * that read UPS data elsewhere (the health pill, the dashboard widget).
 *
 * Test-only: nothing outside a `*.test.tsx` imports it. It lives here rather
 * than under `src/test/` because the shapes are the Power feature's, and they
 * move when `types/ups.ts` does.
 */

/** A NUT server polled without trouble. */
export function nutServer(over: Partial<NutServer> = {}): NutServer {
  return {
    id: "s1",
    name: "nut01",
    host: "192.0.2.40",
    port: 3493,
    username: "",
    has_password: false,
    tls_mode: "off",
    verify_tls: true,
    tls_fingerprint: "",
    enabled: true,
    nut_version: "2.8.1",
    protocol_version: "1.3",
    last_poll_at: "2026-11-02T03:19:00Z",
    last_poll_error: "",
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
    ...over,
  };
}

/** A UPS on mains with a fresh reading. */
export function upsDevice(over: Partial<UpsDevice> = {}): UpsDevice {
  return {
    id: "u1",
    server_id: "s1",
    server_name: "nut01",
    server_enabled: true,
    name: "ups01",
    description: "",
    manufacturer: "",
    model: "",
    serial: "",
    firmware: "",
    driver: "",
    status: "OL",
    flags: ["OL"],
    state: "online",
    last_known_state: "online",
    reachability: "ok",
    reading_stale: false,
    last_reading_at: new Date().toISOString(),
    on_battery_since: null,
    battery_charge: 100,
    battery_charge_low: 10,
    battery_runtime: 1440,
    battery_runtime_low: 120,
    battery_voltage: null,
    battery_type: "",
    battery_date: "",
    load: 38,
    realpower: 342,
    realpower_estimated: true,
    realpower_nominal: 900,
    input_voltage: 230,
    input_frequency: 50,
    input_transfer_low: null,
    input_transfer_high: null,
    output_voltage: 230,
    temperature: null,
    test_result: "",
    feeds: [],
    clients: [],
    clients_listed_at: null,
    host_node: null,
    issues: [],
    ...over,
  };
}

/** A node feed found from a login and judged protected. */
export function upsFeed(over: Partial<UpsFeed> = {}): UpsFeed {
  return {
    id: "f1",
    kind: "node",
    cluster_id: "c1",
    cluster_name: "cluster01",
    node_name: "pve-01",
    node_id: "n1",
    label: "",
    source: "found",
    protected_elsewhere: false,
    protection: "protected",
    login_address: "192.0.2.11",
    created_at: new Date().toISOString(),
    ...over,
  };
}
