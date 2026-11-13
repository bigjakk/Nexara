import type { PBSServer } from "@/features/backup/types/backup";

/** The port Proxmox connects to a PBS on when a storage names none. */
export const PBS_DEFAULT_PORT = "8007";

/**
 * How a storage made from a registered PBS server signs in: with that server's
 * own token, which the server side fills in (pbs_server_id), or with another
 * one the operator types.
 */
export type PBSCredentials = "server-token" | "own-token";

/** A registered PBS server picked in Add storage, and how it signs in. */
export interface PBSSource {
  server: PBSServer;
  credentials: PBSCredentials;
}

// The scheme, then the host (bracketed IPv6, or anything up to the port or
// path), then an optional explicit port. Matched by hand rather than with URL,
// which drops a port that is its scheme's default: "https://pbs:443" would
// read as no port, where the server side reads 443.
const AUTHORITY =
  /^https?:\/\/(\[[0-9a-f:.]+\]|[^/:?#[\]@]+)(?::(\d+))?(?:[/?#]|$)/i;

/**
 * Where Proxmox connects for a registered PBS server, read from its API URL as
 * the server side reads it (pbsConnectionSettings): the host bare, IPv6
 * without its brackets, and the port only when it is not the default. Null for
 * a URL it cannot read.
 */
export function pbsAddress(
  apiUrl: string,
): { server: string; port: string } | null {
  const m = AUTHORITY.exec(apiUrl.trim());
  if (m === null) return null;
  const [, host = "", port = ""] = m;
  return {
    server: host.replace(/^\[(.*)\]$/, "$1"),
    port: port === PBS_DEFAULT_PORT ? "" : port,
  };
}

/** "host:port" as a person reads it, with the default port written out. */
export function pbsAddressLabel(address: {
  server: string;
  port: string;
}): string {
  const host = address.server.includes(":")
    ? `[${address.server}]`
    : address.server;
  return `${host}:${address.port || PBS_DEFAULT_PORT}`;
}

/**
 * A SHA-256 fingerprint as a pbs storage takes it: 32 colon-separated hex
 * pairs. Nexara keeps whatever string it was given; one that is not a digest
 * is passed on as it is, for Proxmox to refuse in its own words.
 */
export function pveFingerprint(fingerprint: string): string {
  const digest = fingerprint.replaceAll(":", "").toUpperCase();
  if (!/^[0-9A-F]{64}$/.test(digest)) return fingerprint;
  return (digest.match(/../g) ?? []).join(":");
}

// What a registered server supplies: where to connect, and the token.
const SERVER_SUPPLIED = [
  "server",
  "port",
  "username",
  "password",
  "fingerprint",
] as const;

/**
 * The form fields a picked server fills in, which the form then neither shows
 * nor requires: where to connect always, and the token too when it is the
 * server's own.
 */
export function pbsSuppliedFields(
  source: PBSSource | null,
): ReadonlySet<string> {
  if (source === null) return new Set();
  return new Set<string>(
    source.credentials === "server-token"
      ? SERVER_SUPPLIED
      : ["server", "port", "fingerprint"],
  );
}

/**
 * The params, and the pbs_server_id, of a create from a registered PBS server.
 *
 * With the server's token, the server side fills in where to connect and the
 * token, and refuses a request that sets any of them itself, so the form's own
 * values for those keys never go along, whatever was typed before the server
 * was picked. With another token, where to connect still comes from the
 * registered server, and the typed username and password go with it.
 */
export function pbsSourceRequest(
  source: PBSSource,
  params: Record<string, string>,
): { params: Record<string, string>; pbs_server_id?: string } {
  const rest = Object.fromEntries(
    Object.entries(params).filter(
      ([key]) => !(SERVER_SUPPLIED as readonly string[]).includes(key),
    ),
  );
  if (source.credentials === "server-token") {
    return { params: rest, pbs_server_id: source.server.id };
  }
  const address = pbsAddress(source.server.api_url);
  const connect: Record<string, string> = {
    server: address?.server ?? "",
    username: params["username"] ?? "",
    password: params["password"] ?? "",
  };
  if (address !== null && address.port !== "") connect["port"] = address.port;
  if (source.server.tls_fingerprint !== "") {
    connect["fingerprint"] = pveFingerprint(source.server.tls_fingerprint);
  }
  return { params: { ...rest, ...connect } };
}
