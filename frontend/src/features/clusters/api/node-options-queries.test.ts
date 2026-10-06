import { describe, expect, it } from "vitest";

import { ApiClientError } from "@/lib/api-client";
import { notesReadRefusal } from "./node-options-queries";

/**
 * notesReadRefusal tells Nexara's own refusal of the notes read from any other
 * 403 by the message alone (the status and error slug are the same): the notes
 * route's "Insufficient permissions" (requireClusterPerm) or a route with
 * Alternatives' "Requires one of: …" (RequireAnyPermission), against anything
 * else, which is Proxmox's, passed on by mapProxmoxError.
 */

function forbidden(message: string): ApiClientError {
  return new ApiClientError(403, { error: "forbidden", message });
}

describe("notesReadRefusal", () => {
  it.each([
    [
      "Nexara's own refusal, as the notes route sends it",
      "Insufficient permissions",
    ],
    [
      "Nexara's refusal of a route declared with Alternatives",
      "Requires one of: manage:node",
    ],
    [
      "Nexara's, naming more than one permission",
      "Requires one of: manage:node, manage:cluster",
    ],
  ])("calls %s Nexara's", (_, message) => {
    expect(notesReadRefusal(forbidden(message))).toBe("nexara");
  });

  it.each([
    ["Proxmox's permission denied", "Proxmox API permission denied"],
    [
      "Proxmox's own words",
      "Proxmox API: Permission check failed (/, Sys.Audit)",
    ],
    // Nexara's own words count only as the whole message: anything more or less
    // is something else saying them, Proxmox's text in mapProxmoxError's wrapping
    // among it.
    ["Nexara's words in another case", "insufficient permissions"],
    ["Nexara's words with more after them", "Insufficient permissions."],
    ["Nexara's words with a space after them", "Insufficient permissions "],
    ["Nexara's words with a space before them", " Insufficient permissions"],
    [
      "Nexara's words inside Proxmox's wrapping",
      "Proxmox API: Insufficient permissions",
    ],
    // The alternatives wording counts only at the start of the message.
    [
      "a message that only mentions the alternatives phrase",
      "Proxmox API: Requires one of: x",
    ],
    ["the alternatives phrase in another case", "requires one of: manage:node"],
    ["a plain refusal", "Forbidden"],
    ["no message at all", ""],
  ])("calls %s another's", (_, message) => {
    expect(notesReadRefusal(forbidden(message))).toBe("other");
  });

  it.each([
    [401, "Insufficient permissions"],
    [404, "Insufficient permissions"],
    [502, "Insufficient permissions"],
    [401, "Requires one of: manage:node"],
    [404, "Requires one of: manage:node"],
    [409, "Requires one of: manage:node"],
    [502, "Requires one of: manage:node"],
    [502, "Failed to connect to Proxmox"],
    [500, "Internal Server Error"],
  ])("is null for a %i, whatever its message (%j)", (status, message) => {
    expect(
      notesReadRefusal(new ApiClientError(status, { error: "x", message })),
    ).toBeNull();
  });

  it("is null for an error that did not come from the API, and for none", () => {
    expect(notesReadRefusal(new Error("Insufficient permissions"))).toBeNull();
    expect(
      notesReadRefusal(new Error("Requires one of: manage:node")),
    ).toBeNull();
    expect(notesReadRefusal(new TypeError("Failed to fetch"))).toBeNull();
    expect(notesReadRefusal(null)).toBeNull();
  });
});
