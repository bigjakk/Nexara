import { describe, expect, it } from "vitest";
import {
  pbsEncryptionRequest,
  pbsKeyFileFingerprint,
  pbsKeyStatus,
  pbsKeyTextError,
} from "./pbs-encryption";

describe("pbsKeyTextError", () => {
  // PBSPlugin accepts a pasted key when decode_json succeeds and the result
  // has a `data` member (on_add_hook / on_update_hook); these rows are that
  // rule, and nothing stricter.
  it.each([
    ["a key file", '{"kdf":null,"data":"AAAA","fingerprint":"aa:bb"}', null],
    ["surrounding whitespace", '\n  {"data":"AAAA"}\n', null],
    [
      "a null data member, which Perl's exists() still counts",
      '{"data":null}',
      null,
    ],
    ["nothing", "   ", "Paste a key, or load one from a file."],
    [
      "the literal autogen",
      "autogen",
      "This is not JSON. Paste the whole key file.",
    ],
    [
      "truncated JSON",
      '{"data":"AAAA"',
      "This is not JSON. Paste the whole key file.",
    ],
    [
      "an object without data",
      '{"kdf":null}',
      'This is not a Proxmox Backup Server key: it has no "data" field.',
    ],
    [
      "an array",
      '[{"data":"AAAA"}]',
      'This is not a Proxmox Backup Server key: it has no "data" field.',
    ],
    [
      "null",
      "null",
      'This is not a Proxmox Backup Server key: it has no "data" field.',
    ],
    [
      "a JSON string",
      '"{\\"data\\":1}"',
      'This is not a Proxmox Backup Server key: it has no "data" field.',
    ],
  ])("%s", (_, text, want) => {
    expect(pbsKeyTextError(text)).toBe(want);
  });
});

describe("pbsKeyStatus", () => {
  it("reads no key, the 1 placeholder and a fingerprint apart", () => {
    const fp =
      "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99";
    expect(pbsKeyStatus(undefined)).toBeNull();
    expect(pbsKeyStatus("")).toBeNull();
    expect(pbsKeyStatus("1")).toEqual({
      fingerprint: null,
      shortFingerprint: null,
    });
    expect(pbsKeyStatus(fp)).toEqual({
      fingerprint: fp,
      shortFingerprint: "AA:BB:CC:DD:EE:FF:00:11",
    });
  });
});

describe("pbsKeyFileFingerprint", () => {
  it("reads the fingerprint a key file names, and none from anything else", () => {
    const fp =
      "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99";
    expect(
      pbsKeyFileFingerprint(JSON.stringify({ data: "x", fingerprint: fp })),
    ).toEqual({ fingerprint: fp, shortFingerprint: "aa:bb:cc:dd:ee:ff:00:11" });

    for (const text of [
      "",
      "not json",
      "null",
      '["aa:bb:cc:dd:ee:ff:00:11"]',
      '{"data":"x"}',
      '{"data":"x","fingerprint":"1"}',
      '{"data":"x","fingerprint":["aa:bb:cc:dd:ee:ff:00:11"]}',
    ]) {
      expect(pbsKeyFileFingerprint(text), text).toBeNull();
    }
  });
});

describe("pbsEncryptionRequest", () => {
  it("sends a key only for autogen and an existing key, and deletes only for remove", () => {
    expect(pbsEncryptionRequest({ mode: "keep", keyText: "x" })).toEqual({
      remove: false,
    });
    expect(pbsEncryptionRequest({ mode: "none", keyText: "x" })).toEqual({
      remove: false,
    });
    expect(pbsEncryptionRequest({ mode: "autogen", keyText: "x" })).toEqual({
      param: "autogen",
      remove: false,
    });
    expect(
      pbsEncryptionRequest({ mode: "existing", keyText: ' {"data":"A"}\n' }),
    ).toEqual({ param: '{"data":"A"}', remove: false });
    expect(pbsEncryptionRequest({ mode: "remove", keyText: "x" })).toEqual({
      remove: true,
    });
  });
});
