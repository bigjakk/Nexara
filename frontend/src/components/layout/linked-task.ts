import type { ParsedDetails } from "./task-status";

/**
 * The part of a larger Proxmox action a row's task is.
 *
 * Proxmox runs some actions as more than one task, and lists each: a start,
 * stop or migration of an HA-managed guest is a request (run by whoever
 * asked) and the task that does the work (run by the HA manager, as
 * root@pam), and a live migration starts the guest on its target node as a
 * third. The rows are all kept, as in Proxmox's own task log; the role is what
 * lets a list say that a root@pam start beside the operator's own is its
 * second half and not a second action.
 */
export type LinkedTaskRole = "ha-request" | "ha-worker" | "migration-start";

const HA_REQUESTS = new Set(["hastart", "hastop", "hamigrate", "harelocate"]);

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/**
 * A request is one by its task type alone. The other two are tasks like any
 * start or migration, told apart only by the links the collector records in
 * the entry's details (queries/proxmox_task_sync.sql).
 */
export function linkedTaskRole(details: ParsedDetails): LinkedTaskRole | null {
  if (text(details["migration_upid"]) !== "") return "migration-start";
  if (text(details["ha_request_upid"]) !== "") return "ha-worker";
  if (HA_REQUESTS.has(text(details.upid).split(":")[5] ?? ""))
    return "ha-request";
  return null;
}

/**
 * Who asked the HA manager for a worker task: the actor of the request's own
 * row when the list holds it (`actorByUpid`), else the PVE account the
 * collector recorded for a request made in Proxmox. Empty when neither says.
 */
export function haRequester(
  details: ParsedDetails,
  actorByUpid: ReadonlyMap<string, string>,
): string {
  return (
    actorByUpid.get(text(details["ha_request_upid"])) ||
    text(details["ha_requested_by"])
  );
}
