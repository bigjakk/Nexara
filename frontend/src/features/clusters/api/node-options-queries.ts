import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

/**
 * Opts a mutation out of the global error toast in lib/query-client.ts.
 *
 * That toast is a safety net for mutations with no error handling of their own.
 * It checks `mutation.options.onError`, which only sees callbacks given to
 * useMutation — not the per-call ones passed to mutate(). So a mutation whose
 * component already renders the failure gets it reported twice without this.
 *
 * Apply it ONLY where the component surfaces the error somewhere the operator
 * is looking at the moment it happens — an open dialog, or a banner in the
 * section body. Applying it to a mutation that relies on the toast makes the
 * failure silent, which is strictly worse than reporting it twice.
 */
const errorsHandledLocally = { onError: () => undefined };

/**
 * The five node settings the PUT writes — the keys of PVE/NodeConfig.pm's
 * $confdesc that are not ACME's. Proxmox's node Options panel edits the first
 * four, and its Notes panel the last. They are read by two routes: the first
 * four with view:node, the last (the notes) with manage:node.
 */
export const NODE_OPTION_KEYS = [
  "startall-onboot-delay",
  "ballooning-target",
  "wakeonlan",
  "location",
  "description",
] as const;

export type NodeOptionKey = (typeof NODE_OPTION_KEYS)[number];

/**
 * GET …/nodes/:node/options (view:node). A key that is absent is unset, which
 * Proxmox reads as its default: no delay, a ballooning target of 80 %, and a
 * location that falls back to the datacenter's. `wakeonlan` and `location` are
 * Proxmox property strings, passed through as stored.
 *
 * The notes are not in it: they are free text that anyone who can edit the node
 * wrote, and are served only to users who can manage it (NodeNotes).
 *
 * `digest` is a save token for the node's whole config file, and an opaque one:
 * it is not Proxmox's own digest, and it is for the client to send back as it
 * was read, never to compute or compare. It changes whenever any part of the
 * file does, the ACME keys and the notes included, so it is shared with them. A
 * save sends it back, and Nexara re-reads the node and compares it before
 * Proxmox's own check. It is a write's token, so only a caller who can manage
 * the node is given it; it is absent for anyone else, and when the node has no
 * config file yet or an empty one, which makes the write unconditional.
 */
export interface NodeOptions {
  "startall-onboot-delay"?: number;
  "ballooning-target"?: number;
  wakeonlan?: string;
  location?: string;
  digest?: string;
}

/**
 * GET …/nodes/:node/notes (manage:node; a 403 without it): the node's notes,
 * the `description` of its config. It reads back with a "\n" after every line,
 * and is absent when there are none. `digest` is a save token of the same kind
 * as NodeOptions', for the same file, read separately. The route is for callers
 * who can write, so it is absent only when the node has no config file yet, or
 * an empty one.
 */
export interface NodeNotes {
  description?: string;
  digest?: string;
}

/**
 * Either read, or both: what changedNodeOptions compares a dialog's drafts
 * against. The Options dialog opens with a NodeOptions, the Notes dialog with a
 * NodeNotes, and each is a NodeConfigRead.
 */
export type NodeConfigRead = NodeOptions & NodeNotes;

/** The settings NodeOptions carries: every key of NODE_OPTION_KEYS but the notes. */
export type NodeOptionReadKey = Exclude<NodeOptionKey, "description">;

/**
 * PUT …/nodes/:node/options (manage:node). A key left out, or sent as "", is
 * left alone: a value is cleared only by naming its key in `delete`, and a key
 * cannot be both set and cleared in one request. `digest`, the save token as it
 * was read, turns the write into a compare-and-swap: Nexara re-reads the node
 * and compares it before Proxmox's own check, and a stale one is answered with
 * a 409. A request that sets nothing and clears nothing is a 400, and one over
 * Proxmox's size cap a 413, each with a message of its own.
 */
export interface NodeOptionsUpdate {
  "startall-onboot-delay"?: number;
  "ballooning-target"?: number;
  wakeonlan?: string;
  location?: string;
  description?: string;
  delete?: NodeOptionKey[];
  digest?: string;
}

/** What a dialog changed: a NodeOptionsUpdate before the digest is added. */
export type NodeOptionsChanges = Omit<NodeOptionsUpdate, "digest">;

/**
 * The options read's cache key. It names the node, as the DNS and ACME reads
 * do, and sits under ["clusters", id, "nodes", node], so the invalidation
 * every ACME save already makes on that prefix (useSetNodeACMEConfig) refreshes
 * it too: both read the same file, and the save token each holds moves with it.
 */
export function nodeOptionsKey(clusterId: string, nodeName: string) {
  return ["clusters", clusterId, "nodes", nodeName, "options"] as const;
}

/**
 * Reads a node's own settings (not its notes: see useNodeNotes). `enabled` is the
 * node being online: Proxmox forwards the read to the node itself, so an offline
 * one cannot be read at all.
 *
 * The default stale time and no polling: an open dialog pins the digest it was
 * opened from (useNodeOptionsSave), so what refreshes underneath it is shown on
 * the card and never reaches the form.
 */
export function useNodeOptions(
  clusterId: string,
  nodeName: string,
  enabled = true,
) {
  return useQuery({
    queryKey: nodeOptionsKey(clusterId, nodeName),
    queryFn: () =>
      apiClient.get<NodeOptions>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/options`,
      ),
    enabled: enabled && clusterId.length > 0 && nodeName.length > 0,
  });
}

/**
 * The notes read's cache key, under the same node prefix as nodeOptionsKey and
 * for the same reason: the ACME save's invalidation of the prefix refreshes it,
 * since it holds a save token for the same file.
 */
export function nodeNotesKey(clusterId: string, nodeName: string) {
  return ["clusters", clusterId, "nodes", nodeName, "notes"] as const;
}

/**
 * Reads a node's notes. `enabled` is the node being online (Proxmox forwards the
 * read to the node itself) AND the user being able to manage it: the route
 * answers 403 to anyone else, so a viewer's card must not ask at all.
 *
 * Its own query, not the options read's: the two routes have different
 * permissions, and a dialog pins the digest of the read it opened from
 * (useNodeOptionsSave), so the Notes dialog opens from this one.
 */
export function useNodeNotes(
  clusterId: string,
  nodeName: string,
  enabled = true,
) {
  return useQuery({
    queryKey: nodeNotesKey(clusterId, nodeName),
    queryFn: () =>
      apiClient.get<NodeNotes>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/notes`,
      ),
    enabled: enabled && clusterId.length > 0 && nodeName.length > 0,
  });
}

/**
 * What Nexara's own permission check refuses the notes read with, word for word.
 * The route is declared clusterCheck("manage", "node") (internal/api/
 * registry_nodes.go), which installs handlers.RequireClusterPermission
 * (internal/api/permissions.go), and that calls requireClusterPerm
 * (internal/api/handlers/permission.go), which refuses with
 * `fiber.NewError(403, "Insufficient permissions")`. The backend pins that text
 * as rbacDeniedMessage (internal/api/registry_route_sweep_test.go).
 */
const NEXARA_REFUSAL = "Insufficient permissions";

/**
 * The start of the other refusal Nexara's registry has. RequireAnyPermission
 * (internal/api/handlers/permission_middleware.go) serves the routes declared
 * with Alternatives, and answers `fiber.NewError(403, "Requires one of: " +
 * names)`. The notes route is not one, so it never sends this today: it is
 * recognised so that the viewer's state survives the day it becomes one.
 */
const NEXARA_ALTERNATIVES_REFUSAL_PREFIX = "Requires one of:";

/**
 * Who refused a read of the notes with a 403. Two things answer it, and they
 * mean different things to the person looking at the card:
 *
 *   "nexara"  Nexara's own permission check (see NEXARA_REFUSAL). This user may
 *             not manage the node, which is the viewer's state: they are told
 *             who can read the notes, and are not offered a Retry that would be
 *             refused again.
 *   "other"   any other 403, which in practice is Proxmox's. mapProxmoxError
 *             (internal/api/handlers/proxmox_error.go) hands a Proxmox 403 on as
 *             a 403 too, with "Proxmox API: …" or "Proxmox API permission
 *             denied" — when Nexara's own API token lacks Sys.Audit on /, for
 *             one, which the node's config read needs. That says nothing about
 *             this user, and its message says what to fix, so it is shown as the
 *             failure it is, with its Retry.
 *
 * The two are told apart by the message alone, since the status and the error
 * slug ("forbidden") are the same. Nexara's is matched EXACTLY: anything that
 * merely contains its words, such as "Proxmox API: Insufficient permissions", is
 * Proxmox's text in mapProxmoxError's wrapping, and is not Nexara's to claim.
 * Null for anything that is not a 403: those are treated as they always were. A
 * 403 of either kind withdraws what the card had read (NodeNotesCard).
 */
export type NotesReadRefusal = "nexara" | "other";

export function notesReadRefusal(error: Error | null): NotesReadRefusal | null {
  if (!(error instanceof ApiClientError) || error.status !== 403) return null;
  const nexara =
    error.message === NEXARA_REFUSAL ||
    error.message.startsWith(NEXARA_ALTERNATIVES_REFUSAL_PREFIX);
  return nexara ? "nexara" : "other";
}

export function useSetNodeOptions(clusterId: string, nodeName: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: NodeOptionsUpdate) =>
      apiClient.put<unknown>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/options`,
        body,
      ),
    onSuccess: () => {
      // The ACME read holds a save token for the same file, so it is stale too.
      void qc.invalidateQueries({
        queryKey: ["clusters", clusterId, "nodes", nodeName, "acme-config"],
      });
      // Returned, not voided: the mutation stays pending until BOTH reads are
      // taken again, so the cards already show what was saved when the dialog
      // closes, and the next Edit seeds from that read rather than the old
      // one. Both, whichever dialog saved: the options and the notes are read
      // separately but carry save tokens for one file, so after any save both
      // hold a stale one, and a dialog opened from either would be refused.
      // invalidateQueries does not reject when the re-read fails, so that never
      // turns a saved write into an error.
      return Promise.all([
        qc.invalidateQueries({ queryKey: nodeOptionsKey(clusterId, nodeName) }),
        qc.invalidateQueries({ queryKey: nodeNotesKey(clusterId, nodeName) }),
      ]);
    },
    // Both dialogs show a failure themselves while they are open and toast it
    // once they are gone (useNodeOptionsSave), and a stale digest is a routine
    // outcome here rather than an exception. A new caller of this hook gets
    // its own mutation, so it must do the same or the failure is silent — and,
    // as useNodeOptionsSave does, say nothing once the session the save was
    // made in has ended (sessionScope).
    ...errorsHandledLocally,
  });
}
