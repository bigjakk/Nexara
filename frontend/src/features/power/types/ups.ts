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
  /**
   * When upsd last took a command from the stored account: upsd checks an
   * account only when a command runs, so this is the only proof it may.
   */
  commands_verified_at: string | null;
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

/**
 * Whether a node a UPS powers would shut down cleanly with it: a upsmon on it
 * is logged in (protected) or not. The others give no verdict: the operator
 * says it shuts down another way, the node is offline, or Nexara cannot tell
 * yet (never listed, a cluster out of reach, or a login it cannot place).
 */
export type UpsProtection =
  | "protected"
  | "not_protected"
  | "checked_elsewhere"
  | "node_offline"
  | "unchecked";

/** Something a UPS powers: a node, or a load that is not one, such as a switch. */
export interface UpsFeed {
  id: string;
  kind: "node" | "label";
  cluster_id: string | null;
  cluster_name: string;
  node_name: string;
  node_id: string | null;
  label: string;
  /** found: a upsmon login came from the node; manual: added by hand. */
  source: "found" | "manual";
  protected_elsewhere: boolean;
  /** "" for a label. */
  protection: UpsProtection | "";
  /** The login that makes the node protected, when it is. */
  login_address: string;
  created_at: string;
}

/** An address logged in to the UPS, as upsd's LIST CLIENT showed it. */
export interface UpsClient {
  address: string;
  connections: number;
  /** Listings in a row this address has been missing from; 0 when listed. */
  missed: number;
  first_seen_at: string;
  last_seen_at: string;
  cluster_id: string | null;
  /** The node the login comes from, when Nexara can tell. */
  node_name: string;
}

export interface UpsNodeRef {
  cluster_id: string;
  node_name: string;
}

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
  /** What the UPS powers and who is logged in to it, as far as the caller may see. */
  feeds: UpsFeed[];
  clients: UpsClient[];
  /** The last listing of logins, taken only on mains; null until the first. */
  clients_listed_at: string | null;
  /** The node the NUT server runs on, when Nexara knows it. */
  host_node: UpsNodeRef | null;
  issues: HealthIssue[];
  vars?: Record<string, string>;
  var_descriptions?: Record<string, string>;
  /** On the detail only, once the poller has listed them. */
  commands?: UpsCommand[];
  writable?: UpsWritableVar[];
  commands_listed_at?: string;
}

/** How much a command can disturb what the UPS powers (decision P6). */
export type UpsCommandTier = "everyday" | "test" | "powers_on" | "driver" | "cuts_power";

/** What running a command or setting a variable asks first: nothing, a confirm, or the UPS's name typed. */
export type UpsConfirm = "" | "plain" | "typed";

export interface UpsCommand {
  name: string;
  description: string;
  tier: UpsCommandTier;
  confirm: UpsConfirm;
}

export interface UpsWritableVar {
  name: string;
  value: string;
  /** "" until the poller has read its type; nothing sets it until then. */
  type: "" | "string" | "number" | "enum" | "range";
  max_len?: number;
  enum?: string[];
  ranges?: [number, number][];
  editable: boolean;
  /** Why it is not editable. */
  reason?: string;
  /** Setting it is gated as cutting power is: execute:ups, and manage:node on the clusters the UPS powers. */
  affects_power: boolean;
  confirm: UpsConfirm;
}

/** One command run or variable set, with what upsd and the driver answered. */
export interface UpsCommandRun {
  id: string;
  kind: "command" | "set";
  name: string;
  argument: string;
  previous: string | null;
  status: "sent" | "pending" | "succeeded" | "failed" | "unconfirmed";
  result: string;
  requested_at: string;
  finished_at: string | null;
  user_email: string;
  user_display_name: string;
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
