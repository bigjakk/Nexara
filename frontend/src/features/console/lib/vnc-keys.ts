import type RFB from "@novnc/novnc";

/**
 * Keyboard mapping for synthetic VNC input (the toolbar's paste dialog).
 *
 * Kept out of the component tree deliberately: VNCToolbar needs
 * `typeTextIntoVnc` and VNCViewer renders VNCToolbar, so hosting the helper
 * in VNCViewer made those two modules import each other.
 */

/** X11 keysym for the left Shift modifier. */
const XK_SHIFT_L = 0xffe1;

/**
 * Physical key (KeyboardEvent.code) and Shift state that produces each
 * printable character on a US layout.
 *
 * Passing a real `code` matters: noVNC only uses QEMU's extended key event —
 * which carries a hardware scancode and honours held modifiers — when it can
 * resolve `code` to a scancode. With `code = null` it falls back to a legacy
 * keysym-only event, so a Shift sent alongside never applies to the character
 * and "!@#$%^&*()" arrives as "1234567890". This mirrors what the live
 * keyboard handler does, which is why typing has always worked.
 */
const US_LAYOUT = new Map<string, { code: string; shift: boolean }>();

// [unshifted, shifted, physical code]
const US_KEYS: [string, string, string][] = [
  ["`", "~", "Backquote"],
  ["1", "!", "Digit1"],
  ["2", "@", "Digit2"],
  ["3", "#", "Digit3"],
  ["4", "$", "Digit4"],
  ["5", "%", "Digit5"],
  ["6", "^", "Digit6"],
  ["7", "&", "Digit7"],
  ["8", "*", "Digit8"],
  ["9", "(", "Digit9"],
  ["0", ")", "Digit0"],
  ["-", "_", "Minus"],
  ["=", "+", "Equal"],
  ["[", "{", "BracketLeft"],
  ["]", "}", "BracketRight"],
  ["\\", "|", "Backslash"],
  [";", ":", "Semicolon"],
  ["'", '"', "Quote"],
  [",", "<", "Comma"],
  [".", ">", "Period"],
  ["/", "?", "Slash"],
];

for (const [plain, shifted, code] of US_KEYS) {
  US_LAYOUT.set(plain, { code, shift: false });
  US_LAYOUT.set(shifted, { code, shift: true });
}
for (let i = 0; i < 26; i++) {
  const lower = String.fromCharCode(97 + i);
  const upper = String.fromCharCode(65 + i);
  US_LAYOUT.set(lower, { code: `Key${upper}`, shift: false });
  US_LAYOUT.set(upper, { code: `Key${upper}`, shift: true });
}
US_LAYOUT.set(" ", { code: "Space", shift: false });

/**
 * Type out text as individual key events into a VNC session.
 * Converts each character to an X11 keysym plus its US-layout physical key,
 * bracketing shifted characters with Shift down/up.
 *
 * Characters with no US-layout key ("é", em-dash, emoji, stray control
 * codes) are still attempted as legacy keysym-only events — harmless, and it
 * works against servers that resolve keysyms directly — but QEMU's en-us
 * keymap has no scancode for them, so they resolve to keycode 0 and never
 * reach the guest. They are returned so the caller can tell the user what
 * was dropped instead of losing it silently.
 *
 * @returns every character that had no US-layout key, in the order typed
 *   (duplicates included, so `.length` is the number of characters at risk).
 */
export function typeTextIntoVnc(rfb: RFB, text: string): string[] {
  const unmapped: string[] = [];

  for (const ch of text) {
    const cp = ch.codePointAt(0);
    if (cp === undefined) continue;

    let keysym: number;
    let code: string | null;
    if (ch === "\n" || ch === "\r") {
      keysym = 0xff0d; // XK_Return
      code = "Enter";
    } else if (ch === "\t") {
      keysym = 0xff09; // XK_Tab
      code = "Tab";
    } else {
      // Latin-1: keysym === unicode code point; above that, 0x01000000 + cp.
      keysym = cp <= 0x00ff ? cp : 0x01000000 + cp;
      code = US_LAYOUT.get(ch)?.code ?? null;
      if (code === null) unmapped.push(ch);
    }

    const shift = US_LAYOUT.get(ch)?.shift ?? false;
    if (shift) rfb.sendKey(XK_SHIFT_L, "ShiftLeft", true);
    rfb.sendKey(keysym, code, true); // key down
    rfb.sendKey(keysym, code, false); // key up
    if (shift) rfb.sendKey(XK_SHIFT_L, "ShiftLeft", false);
  }

  return unmapped;
}
