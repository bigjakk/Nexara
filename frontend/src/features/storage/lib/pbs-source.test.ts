import { describe, expect, it } from "vitest";
import type { PBSServer } from "@/features/backup/types/backup";
import {
  pbsAddress,
  pbsSourceRequest,
  pveFingerprint,
  type PBSCredentials,
} from "./pbs-source";

const PAIRS = Array.from({ length: 32 }, () => "AB").join(":");

describe("pbsAddress", () => {
  // As pbsConnectionSettings reads the URL on the server side.
  it.each([
    ["https://pbs.example.com:8007", { server: "pbs.example.com", port: "" }],
    ["https://pbs.example.com", { server: "pbs.example.com", port: "" }],
    // URL would drop this port as https's default; the server side keeps it.
    [
      "https://pbs.example.com:443/",
      { server: "pbs.example.com", port: "443" },
    ],
    [
      "https://pbs.example.com:8443",
      { server: "pbs.example.com", port: "8443" },
    ],
    ["https://[2001:db8::1]:8007", { server: "2001:db8::1", port: "" }],
    ["not a url", null],
  ])("%s", (apiUrl, want) => {
    expect(pbsAddress(apiUrl)).toEqual(want);
  });
});

describe("pveFingerprint", () => {
  it.each([
    [PAIRS, PAIRS],
    ["ab".repeat(32), PAIRS],
    // Not a digest: passed on for Proxmox to refuse in its own words.
    ["AB:CD", "AB:CD"],
  ])("%s", (fingerprint, want) => {
    expect(pveFingerprint(fingerprint)).toBe(want);
  });
});

describe("pbsSourceRequest", () => {
  const server: PBSServer = {
    id: "pbs-1",
    name: "pbs01",
    api_url: "https://pbs01.example.com:8443",
    token_id: "backup@pbs!test",
    tls_fingerprint: "ab".repeat(32),
    cluster_id: null,
    created_at: "",
    updated_at: "",
  };
  // What the form held, including values typed before the server was picked.
  const form = {
    datastore: "datastore01",
    content: "backup",
    server: "typed.example.com",
    port: "9999",
    username: "backup@pbs!other",
    password: "not-a-real-password",
  };

  it.each<[PBSCredentials, ReturnType<typeof pbsSourceRequest>]>([
    // The server side fills where to connect and the token, and refuses a
    // request that names any of them.
    [
      "server-token",
      {
        params: { datastore: "datastore01", content: "backup" },
        pbs_server_id: "pbs-1",
      },
    ],
    // Where to connect is still the registered server's; the token is typed.
    [
      "own-token",
      {
        params: {
          datastore: "datastore01",
          content: "backup",
          server: "pbs01.example.com",
          port: "8443",
          fingerprint: PAIRS,
          username: "backup@pbs!other",
          password: "not-a-real-password",
        },
      },
    ],
  ])("%s", (credentials, want) => {
    expect(pbsSourceRequest({ server, credentials }, form)).toEqual(want);
  });
});
