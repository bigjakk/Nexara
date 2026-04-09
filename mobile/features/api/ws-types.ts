/**
 * WebSocket message + event types — mirrors `frontend/src/types/ws.ts`.
 * Kept in sync manually until we have an OpenAPI spec.
 */

export type WsClientMessageType = "subscribe" | "unsubscribe" | "ping";

export type WsServerMessageType =
  | "welcome"
  | "subscribed"
  | "data"
  | "error"
  | "pong";

export interface WsOutgoingMessage {
  type: WsClientMessageType;
  channels?: string[];
}

export interface WsIncomingMessage {
  type: WsServerMessageType;
  channel?: string;
  message?: string;
  payload?: unknown;
}

export type WsConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting";

export type EventKind =
  | "task_created"
  | "task_update"
  | "audit_entry"
  | "vm_state_change"
  | "inventory_change"
  | "migration_update"
  | "drs_action"
  | "pbs_change"
  | "cve_scan"
  | "alert_fired"
  | "alert_state_change"
  | "rolling_update"
  | "ha_change"
  | "pool_change"
  | "replication_change"
  | "acme_change";

export interface NexaraEvent {
  kind: EventKind;
  cluster_id?: string;
  resource_type?: string;
  resource_id?: string;
  action?: string;
  /**
   * Non-OK completion reason for actions that have a background lifecycle
   * (e.g. a Proxmox task that exits with a non-OK exit status). Present
   * iff the action failed — clients that correlate fire-and-forget
   * mutations to their background completion (like the mobile task
   * tracker) check this field to flip a tracked task from pending to
   * failed instead of pending to success. See `ClusterEventWithError`
   * in the backend `internal/events` package for the publisher side.
   */
  error?: string;
  timestamp: string;
}
