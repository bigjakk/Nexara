import { describe, it, expect } from "vitest";
import type RFB from "@novnc/novnc";
import { typeTextIntoVnc } from "./vnc-keys";

const XK_SHIFT_L = 0xffe1;

type SendKeyCall = [keysym: number, code: string | null, down: boolean];

function makeRfb() {
  const calls: SendKeyCall[] = [];
  const rfb = {
    sendKey: (keysym: number, code: string | null, down: boolean) => {
      calls.push([keysym, code, down]);
    },
  } as unknown as RFB;
  return { rfb, calls };
}

/**
 * A physical US keyboard: code → [unshifted, shifted]. Written from the
 * keyboard's point of view so it stays an independent reference rather than a
 * restatement of the implementation's table.
 */
const KEYBOARD: Record<string, [string, string]> = {
  Backquote: ["`", "~"],
  Digit1: ["1", "!"], Digit2: ["2", "@"], Digit3: ["3", "#"],
  Digit4: ["4", "$"], Digit5: ["5", "%"], Digit6: ["6", "^"],
  Digit7: ["7", "&"], Digit8: ["8", "*"], Digit9: ["9", "("],
  Digit0: ["0", ")"],
  Minus: ["-", "_"], Equal: ["=", "+"],
  BracketLeft: ["[", "{"], BracketRight: ["]", "}"], Backslash: ["\\", "|"],
  Semicolon: [";", ":"], Quote: ["'", '"'],
  Comma: [",", "<"], Period: [".", ">"], Slash: ["/", "?"],
  Space: [" ", " "],
};
for (let i = 0; i < 26; i++) {
  const lower = String.fromCharCode(97 + i);
  KEYBOARD[`Key${lower.toUpperCase()}`] = [lower, lower.toUpperCase()];
}

/** Which physical key produces a given character. */
const CODE_OF = new Map<string, string>();
for (const [code, [plain, shifted]] of Object.entries(KEYBOARD)) {
  CODE_OF.set(plain, code);
  CODE_OF.set(shifted, code);
}

/**
 * Reproduce what the VNC server emits for a stream of sendKey calls.
 *
 * Two mechanisms, chosen per call by noVNC: with a resolvable `code` the key
 * travels as a hardware scancode and honours the held modifier; with
 * `code === null` it falls back to a legacy keysym-only event, where the
 * server resolves the keysym to a physical key and applies *no* modifier.
 * That second path is why "!@#" used to arrive as "123" even once Shift was
 * being sent — so the model must read `code`, not just the shift state.
 */
function replayAsServer(calls: SendKeyCall[]): string {
  let shiftHeld = false;
  let out = "";
  for (const [keysym, code, down] of calls) {
    if (keysym === XK_SHIFT_L) {
      shiftHeld = down;
      continue;
    }
    if (!down) continue; // emit on key-down only
    if (keysym === 0xff0d) { out += "\n"; continue; }
    if (keysym === 0xff09) { out += "\t"; continue; }

    const ch = String.fromCodePoint(keysym);
    const key = code ?? CODE_OF.get(ch);
    const pair = key === undefined ? undefined : KEYBOARD[key];
    if (pair === undefined) {
      out += ch; // not on a US layout — passes through untranslated
      continue;
    }
    out += code === null ? pair[0] : shiftHeld ? pair[1] : pair[0];
  }
  return out;
}

describe("typeTextIntoVnc", () => {
  it("sends a shifted character with Shift held and its physical key", () => {
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, "@");

    expect(calls).toEqual([
      [XK_SHIFT_L, "ShiftLeft", true],
      [0x40, "Digit2", true],
      [0x40, "Digit2", false],
      [XK_SHIFT_L, "ShiftLeft", false],
    ]);
  });

  // A null `code` makes noVNC fall back to a legacy keysym-only event, which
  // ignores the held Shift — that is what produced "1234567890" for symbols.
  it("always resolves a physical key for printable ASCII", () => {
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, "aZ0!~ /?");

    const missing = calls.filter(
      ([keysym, code]) => keysym !== XK_SHIFT_L && code === null,
    );
    expect(missing).toEqual([]);
  });

  it("does not hold Shift for unshifted characters", () => {
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, "a2");

    expect(calls.some(([keysym]) => keysym === XK_SHIFT_L)).toBe(false);
  });

  it("holds Shift for uppercase letters", () => {
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, "A");

    expect(calls).toEqual([
      [XK_SHIFT_L, "ShiftLeft", true],
      [0x41, "KeyA", true],
      [0x41, "KeyA", false],
      [XK_SHIFT_L, "ShiftLeft", false],
    ]);
  });

  // Regression: the shifted number row arrived as bare digits.
  it("round-trips the shifted number row", () => {
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, "!@#$%^&*()");

    expect(replayAsServer(calls)).toBe("!@#$%^&*()");
  });

  // Regression: "@" arrived as "2" and "&" as "7", turning
  // "ceph-osd@1 && ..." into "ceph-osd21 77 ...".
  it("round-trips a shell command containing @ and &&", () => {
    const cmd = "systemctl reset-failed ceph-osd@1 && systemctl start ceph-osd@1";
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, cmd);

    expect(replayAsServer(calls)).toBe(cmd);
  });

  // Pins every key/shift pairing in the layout table: a wrong code (or a
  // dropped one) changes the character the server emits.
  it("round-trips every printable ASCII character", () => {
    let printable = "";
    for (let cp = 0x20; cp <= 0x7e; cp++) printable += String.fromCodePoint(cp);

    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, printable);

    expect(replayAsServer(calls)).toBe(printable);
  });

  it("maps Enter and Tab to their control keysyms", () => {
    const { rfb, calls } = makeRfb();
    typeTextIntoVnc(rfb, "\n\t");

    expect(calls.filter(([, , down]) => down)).toEqual([
      [0xff0d, "Enter", true],
      [0xff09, "Tab", true],
    ]);
  });

  it("reports nothing unmapped for text the US layout covers", () => {
    const { rfb } = makeRfb();

    expect(typeTextIntoVnc(rfb, "sudo reboot\n\tok!")).toEqual([]);
  });

  // Characters off the US layout resolve to keycode 0 under QEMU's en-us
  // keymap and never reach the guest — the caller has to be able to say so.
  it("returns each character with no US-layout key, in order", () => {
    const { rfb } = makeRfb();

    expect(typeTextIntoVnc(rfb, "café — 🎉")).toEqual(["é", "—", "🎉"]);
  });

  it("counts repeats so the caller can report how many were dropped", () => {
    const { rfb } = makeRfb();

    expect(typeTextIntoVnc(rfb, "naïve résumé")).toEqual(["ï", "é", "é"]);
  });

  // Partial success: an unmappable character must not abort the paste, so
  // everything around it still types. (replayAsServer passes an unknown
  // keysym through untranslated; a real server drops it — which is exactly
  // what the returned list exists to report.)
  it("keeps typing the mappable characters around an unmappable one", () => {
    const text = "cafés are nice";
    const { rfb, calls } = makeRfb();

    expect(typeTextIntoVnc(rfb, text)).toEqual(["é"]);
    expect(replayAsServer(calls)).toBe(text);
  });
});
