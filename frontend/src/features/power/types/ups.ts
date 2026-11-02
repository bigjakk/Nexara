import type { HealthIssue } from "@/types/api";

/** A NUT server as the API returns it. There is never a password field. */
export interface NutServer {
  id: string;
  name: string;
  host: string;
  port: number;
  /** Empty unless the caller holds manage:ups. */
  username: string;
  has_password: boolean;
  tls_mode: "off" | "starttls";
  verify_tls: boolean;
  tls_fingerprint: string;
  enabled: boolean;
  nut_version: string;
  protocol_version: string;
  last_poll_at: string | null;
  last_poll_error: string;
  created_at: string;
  updated_at: string;
}

export interface NutServerRequest {
  name?: string;
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  tls_mode?: "off" | "starttls";
  verify_tls?: boolean;
  tls_fingerprint?: string;
  enabled?: boolean;
  clear_credentials?: boolean;
  allow_private_address?: boolean;
  acknowledge_cleartext_credentials?: boolean;
  acknowledge_insecure_tls?: boolean;
}

export interface NutProbeResult {
  nut_version: string;
  protocol_version: string;
  took_ms: number;
  tls: boolean;
  tls_fingerprint: string;
  devices: { name: string; description: string }[];
}

export type UpsState =
  | "online"
  | "on_battery"
  | "low_battery"
  | "off"
  | "unknown";

export type UpsReachability =
  | "ok"
  | "stale"
  | "driver_down"
  | "unreachable"
  | "unknown";

export interface UpsDevice {
  id: string;
  server_id: string;
  server_name: string;
  server_enabled: boolean;
  name: string;
  description: string;
  manufacturer: string;
  model: string;
  serial: string;
  firmware: string;
  driver: string;
  status: string;
  flags: string[];
  /** "unknown" unless the reading is fresh and its server polled. */
  state: UpsState;
  /** The last state read, kept for when state is "unknown". */
  last_known_state: UpsState;
  reachability: UpsReachability;
  reading_stale: boolean;
  last_reading_at: string | null;
  on_battery_since: string | null;
  battery_charge: number | null;
  battery_charge_low: number | null;
  /** Seconds. */
  battery_runtime: number | null;
  battery_runtime_low: number | null;
  battery_voltage: number | null;
  battery_type: string;
  battery_date: string;
  load: number | null;
  realpower: number | null;
  /** True when realpower is load x nominal, not a measurement. */
  realpower_estimated: boolean;
  realpower_nominal: number | null;
  input_voltage: number | null;
  input_frequency: number | null;
  input_transfer_low: number | null;
  input_transfer_high: number | null;
  output_voltage: number | null;
  temperature: number | null;
  test_result: string;
  issues: HealthIssue[];
  vars?: Record<string, string>;
  var_descriptions?: Record<string, string>;
}

export type UpsRange = "1h" | "6h" | "24h" | "7d";

export interface UpsMetricPoint {
  timestamp: number;
  charge: number | null;
  runtime: number | null;
  battery_voltage: number | null;
  load: number | null;
  realpower: number | null;
  /** The bucket's lowest and highest input: sags, and surges a TRIM corrects. */
  input_voltage_min: number | null;
  input_voltage_max: number | null;
  output_voltage: number | null;
  input_frequency: number | null;
  temperature: number | null;
  on_battery: boolean | null;
}

export interface UpsMetrics {
  range: UpsRange;
  bucket_seconds: number;
  points: UpsMetricPoint[];
}

export type UpsEventKind =
  | "on_battery"
  | "low_battery"
  | "overload"
  | "bypass"
  | "voltage_correction"
  | "replace_battery"
  | "unreachable"
  | "stale"
  | "self_test";

export interface UpsEvent {
  id: string;
  ups_id: string;
  ups_name: string;
  ups_description: string;
  server_name: string;
  kind: UpsEventKind;
  started_at: string;
  ended_at: string | null;
  end_observed: boolean;
  details: {
    lowest_charge?: number;
    lowest_runtime?: number;
    peak_load?: number;
    status?: string;
    reason?: string;
    result?: string;
    /** Why an episode closed unseen without contact being lost. */
    ended_by?: "polling_off" | "removed";
  };
}
