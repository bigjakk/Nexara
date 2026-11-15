/**
 * The webhook's Headers field read as the object the dispatcher sends: error
 * is null for valid input (empty is no headers), else why it is not one. Every
 * value must be a string: the dispatcher reads the headers as strings, and one
 * that is not fails every send.
 */
export function parseWebhookHeaders(
  raw: string,
): { headers: Record<string, string>; error: null } | { headers: null; error: "json" | "shape" } {
  if (raw.trim() === "") return { headers: {}, error: null };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { headers: null, error: "json" };
  }
  if (
    parsed === null ||
    typeof parsed !== "object" ||
    Array.isArray(parsed) ||
    !Object.values(parsed).every((v) => typeof v === "string")
  ) {
    return { headers: null, error: "shape" };
  }
  return { headers: parsed as Record<string, string>, error: null };
}
