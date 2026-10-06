import { describe, expect, it } from "vitest";

import type { NodeConfigRead, NodeOptions } from "../api/node-options-queries";
import {
  buildLocation,
  buildWakeOnLan,
  changedNodeOptions,
  characterCount,
  checkCoordinate,
  checkInteger,
  checkInterfaceName,
  checkIPv4Address,
  checkLocationName,
  checkMacAddress,
  checkNotes,
  describeBallooningTarget,
  describeLocation,
  describeStartDelay,
  describeWakeOnLan,
  descriptionForEditing,
  NODE_OPTION_LABELS,
  nodeOptionSupport,
  notesChangedBetween,
  NOTES_MAX_LENGTH,
  optionsChangedBetween,
  parseLocation,
  parseWakeOnLan,
  visibleNotes,
  type LocationFields,
  type WakeOnLanFields,
} from "./node-options";

const MAC = "02:00:00:00:00:01";
const MAC_2 = "02:00:00:00:00:02";

function wol(
  mac: string,
  bindInterface = "",
  broadcastAddress = "",
): WakeOnLanFields {
  return { mac, bindInterface, broadcastAddress };
}

function place(latitude: string, longitude: string, name = ""): LocationFields {
  return { latitude, longitude, name };
}

describe("parseWakeOnLan", () => {
  it.each([
    // Proxmox's own UI writes the bare MAC; the keyed form occurs too.
    [MAC, wol(MAC), []],
    [`mac=${MAC}`, wol(MAC), []],
    [`${MAC},bind-interface=vmbr0`, wol(MAC, "vmbr0"), []],
    [
      `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      wol(MAC, "vmbr0", "192.0.2.255"),
      [],
    ],
    // Order is not meaning.
    [
      `broadcast-address=192.0.2.255,${MAC},bind-interface=vmbr0`,
      wol(MAC, "vmbr0", "192.0.2.255"),
      [],
    ],
    // A key a newer node added is kept, not refused.
    [`${MAC},foo=bar`, wol(MAC), ["foo=bar"]],
    [`foo=bar,${MAC},baz=1`, wol(MAC), ["foo=bar", "baz=1"]],
    // A blank segment is skipped, as parse_property_string skips it.
    [`${MAC},,bind-interface=vmbr0,`, wol(MAC, "vmbr0"), []],
    // A value of only whitespace is a value to it (its /^([^=]+)=(.+)\z/ never
    // trims), not an empty one, so it is read, as the empty field it shows as.
    [`${MAC},bind-interface= `, wol(MAC), []],
    [`${MAC}, ,bind-interface=vmbr0`, wol(MAC, "vmbr0"), []],
    // Nothing at all is no setting.
    ["", wol(""), []],
  ])("reads %j", (raw, fields, unknownSegments) => {
    expect(parseWakeOnLan(raw)).toEqual({
      ok: true,
      fields,
      unknownSegments,
    });
  });

  it.each([
    ["a second bare segment", `${MAC},${MAC_2}`],
    ["a bare segment beside mac=", `${MAC},mac=${MAC_2}`],
    ["mac= beside a bare segment", `mac=${MAC_2},${MAC}`],
    ["mac given twice", `mac=${MAC},mac=${MAC_2}`],
    ["a repeated subkey", `${MAC},bind-interface=vmbr0,bind-interface=vmbr1`],
    ["a repeated unknown key", `${MAC},foo=1,foo=2`],
    ["an empty value", `${MAC},bind-interface=`],
    ["an empty mac value", "mac="],
    ["an empty key", `${MAC},=vmbr0`],
    ["a lone =", "="],
    ["no MAC", "bind-interface=vmbr0"],
    ["only an unknown key", "foo=bar"],
    ["a newline", `${MAC},bind-interface=vm\nbr0`],
  ])("refuses %s", (_, raw) => {
    expect(parseWakeOnLan(raw)).toEqual({ ok: false });
  });
});

describe("buildWakeOnLan", () => {
  // Stored values, in the shapes Proxmox leaves them: a rebuild with nothing
  // touched has to give back every byte, which is what makes the dirty check
  // "rebuilt === stored" sound, and what keeps an edit of one field from
  // rewriting the rest.
  const CORPUS = [
    "",
    MAC,
    `mac=${MAC}`,
    "02:00:00:00:00:AB",
    `${MAC},bind-interface=vmbr0`,
    `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
    `bind-interface=vmbr0,${MAC}`,
    `broadcast-address=192.0.2.255,bind-interface=vmbr0,mac=${MAC}`,
    `${MAC},foo=bar`,
    `foo=bar,${MAC},baz=1`,
    `${MAC},,bind-interface=vmbr0,`,
    `${MAC}, bind-interface = vmbr0`,
    `mac=${MAC},future-option=a=b`,
  ];

  it.each(CORPUS)("rebuilds %j to exactly itself", (raw) => {
    const parsed = parseWakeOnLan(raw);
    if (!parsed.ok) throw new Error(`corpus value ${raw} does not parse`);
    expect(buildWakeOnLan(parsed.fields, raw)).toBe(raw);
  });

  it("writes a new value with the MAC bare and first", () => {
    expect(buildWakeOnLan(wol(MAC))).toBe(MAC);
    expect(buildWakeOnLan(wol(MAC, "vmbr0"))).toBe(
      `${MAC},bind-interface=vmbr0`,
    );
    expect(buildWakeOnLan(wol(MAC, "vmbr0", "192.0.2.255"))).toBe(
      `${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
    );
    expect(buildWakeOnLan(wol(MAC, "", "192.0.2.255"))).toBe(
      `${MAC},broadcast-address=192.0.2.255`,
    );
  });

  it("keeps a bare MAC bare and a keyed one keyed when the MAC changes", () => {
    expect(
      buildWakeOnLan(wol(MAC_2, "vmbr0"), `${MAC},bind-interface=vmbr0`),
    ).toBe(`${MAC_2},bind-interface=vmbr0`);
    expect(
      buildWakeOnLan(wol(MAC_2, "vmbr0"), `mac=${MAC},bind-interface=vmbr0`),
    ).toBe(`mac=${MAC_2},bind-interface=vmbr0`);
    // Where it stands, not moved to the front.
    expect(
      buildWakeOnLan(wol(MAC_2, "vmbr0"), `bind-interface=vmbr0,${MAC}`),
    ).toBe(`bind-interface=vmbr0,${MAC_2}`);
    expect(
      buildWakeOnLan(wol(MAC_2, "vmbr0"), `bind-interface=vmbr0,mac=${MAC}`),
    ).toBe(`bind-interface=vmbr0,mac=${MAC_2}`);
  });

  it("adds a subkey after what is there, leaving a bare MAC bare", () => {
    expect(buildWakeOnLan(wol(MAC, "vmbr0"), MAC)).toBe(
      `${MAC},bind-interface=vmbr0`,
    );
    expect(buildWakeOnLan(wol(MAC, "vmbr0", "192.0.2.255"), `mac=${MAC}`)).toBe(
      `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
    );
    expect(buildWakeOnLan(wol(MAC, "vmbr0", "192.0.2.255"), MAC)).toBe(
      `${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
    );
  });

  it("removes only the subkey that was cleared", () => {
    const base = `${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`;
    expect(buildWakeOnLan(wol(MAC, "", "192.0.2.255"), base)).toBe(
      `${MAC},broadcast-address=192.0.2.255`,
    );
    expect(buildWakeOnLan(wol(MAC, "vmbr0", ""), base)).toBe(
      `${MAC},bind-interface=vmbr0`,
    );
    expect(buildWakeOnLan(wol(MAC), base)).toBe(MAC);
    expect(
      buildWakeOnLan(
        wol(MAC, "", "192.0.2.255"),
        `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      ),
    ).toBe(`mac=${MAC},broadcast-address=192.0.2.255`);
  });

  it("rewrites a changed subkey where it stands", () => {
    expect(
      buildWakeOnLan(
        wol(MAC, "vmbr1", "192.0.2.255"),
        `bind-interface=vmbr0,${MAC},broadcast-address=192.0.2.255`,
      ),
    ).toBe(`bind-interface=vmbr1,${MAC},broadcast-address=192.0.2.255`);
  });

  it("keeps a key it has no field for, byte for byte and in its place", () => {
    expect(
      buildWakeOnLan(
        wol(MAC_2, "vmbr0"),
        `foo=bar,${MAC},bind-interface=vmbr0,baz=1`,
      ),
    ).toBe(`foo=bar,${MAC_2},bind-interface=vmbr0,baz=1`);
    expect(
      buildWakeOnLan(wol(MAC), `foo=bar,${MAC},bind-interface=vmbr0,baz=1`),
    ).toBe(`foo=bar,${MAC},baz=1`);
    expect(buildWakeOnLan(wol(MAC, "vmbr0"), `${MAC},foo=a=b`)).toBe(
      `${MAC},foo=a=b,bind-interface=vmbr0`,
    );
  });

  it("is no value at all without a MAC, whatever else is set", () => {
    expect(buildWakeOnLan(wol(""))).toBe("");
    expect(buildWakeOnLan(wol("", "vmbr0", "192.0.2.255"), MAC)).toBe("");
    expect(buildWakeOnLan(wol(""), `${MAC},foo=bar`)).toBe("");
  });

  it("builds over a value it cannot read as if there were none", () => {
    expect(buildWakeOnLan(wol(MAC, "vmbr0"), `${MAC},${MAC_2}`)).toBe(
      `${MAC},bind-interface=vmbr0`,
    );
  });

  it("parses what it builds", () => {
    const fields = wol(MAC, "vmbr0", "192.0.2.255");
    expect(parseWakeOnLan(buildWakeOnLan(fields))).toEqual({
      ok: true,
      fields,
      unknownSegments: [],
    });
  });
});

describe("parseLocation", () => {
  it.each([
    ["latitude=12.5,longitude=-45.25", place("12.5", "-45.25"), []],
    [
      "latitude=12.5,longitude=-45.25,name=Site A",
      place("12.5", "-45.25", "Site A"),
      [],
    ],
    // Order is not meaning, and a name may hold an "=".
    [
      "name=Site A=1,longitude=-45.25,latitude=12.5",
      place("12.5", "-45.25", "Site A=1"),
      [],
    ],
    ["latitude=0,longitude=0,foo=bar", place("0", "0"), ["foo=bar"]],
    ["latitude=0,,longitude=0,", place("0", "0"), []],
    // A name of only whitespace is a name to Proxmox (one space: its
    // /^([^=]+)=(.+)\z/ never trims), not an empty one, so it is no refusal.
    ["latitude=1,name= ,longitude=2", place("1", "2"), []],
    ["latitude=1,longitude=2,name=   ", place("1", "2"), []],
    ["", place("", ""), []],
  ])("reads %j", (raw, fields, unknownSegments) => {
    expect(parseLocation(raw)).toEqual({ ok: true, fields, unknownSegments });
  });

  it.each([
    // pve-node-location has no default key.
    ["a bare segment", "latitude=0,longitude=0,Site A"],
    ["a bare value alone", "Site A"],
    ["a repeated key", "latitude=0,latitude=1,longitude=0"],
    ["an empty value", "latitude=,longitude=0"],
    ["an empty name", "latitude=1,longitude=2,name="],
    ["an empty key", "=1,latitude=0,longitude=0"],
    ["no latitude", "longitude=0"],
    ["no longitude", "latitude=0,name=Site A"],
    ["only a name", "name=Site A"],
    ["only an unknown key", "foo=bar"],
  ])("refuses %s", (_, raw) => {
    expect(parseLocation(raw)).toEqual({ ok: false });
  });
});

describe("buildLocation", () => {
  const CORPUS = [
    "",
    "latitude=12.5,longitude=-45.25",
    "latitude=12.5,longitude=-45.25,name=Site A",
    "name=Site A,longitude=-45.25,latitude=12.5",
    "longitude=-45.25,latitude=12.5",
    "latitude=12.5,longitude=-45.25,foo=bar",
    "foo=bar,latitude=12.5,,longitude=-45.25,",
    "latitude=+12.50,longitude=-45.25e0",
    // A name of whitespace is read (see parseLocation), so it round-trips.
    "latitude=1,name= ,longitude=2",
  ];

  it.each(CORPUS)("rebuilds %j to exactly itself", (raw) => {
    const parsed = parseLocation(raw);
    if (!parsed.ok) throw new Error(`corpus value ${raw} does not parse`);
    expect(buildLocation(parsed.fields, raw)).toBe(raw);
  });

  it("writes a new value latitude, longitude, name", () => {
    expect(buildLocation(place("12.5", "-45.25"))).toBe(
      "latitude=12.5,longitude=-45.25",
    );
    expect(buildLocation(place("12.5", "-45.25", "Site A"))).toBe(
      "latitude=12.5,longitude=-45.25,name=Site A",
    );
    // 0 is a coordinate, not an empty field.
    expect(buildLocation(place("0", "0"))).toBe("latitude=0,longitude=0");
  });

  it("rewrites a field that is not the last where it stands", () => {
    expect(
      buildLocation(
        place("13", "-45.25", "Site A"),
        "latitude=12.5,longitude=-45.25,name=Site A",
      ),
    ).toBe("latitude=13,longitude=-45.25,name=Site A");
    expect(
      buildLocation(
        place("12.5", "-46", "Site A"),
        "latitude=12.5,longitude=-45.25,name=Site A",
      ),
    ).toBe("latitude=12.5,longitude=-46,name=Site A");
  });

  it("rewrites a changed field where it stands and adds a missing name last", () => {
    expect(
      buildLocation(
        place("1", "-45.25", "Site A"),
        "name=Site A,longitude=-45.25,latitude=12.5",
      ),
    ).toBe("name=Site A,longitude=-45.25,latitude=1");
    expect(
      buildLocation(
        place("12.5", "-45.25", "Site B"),
        "longitude=-45.25,latitude=12.5",
      ),
    ).toBe("longitude=-45.25,latitude=12.5,name=Site B");
  });

  it("keeps a whitespace-only name, which Proxmox counts, where it stands", () => {
    expect(
      buildLocation(place("1", "3"), "latitude=1,name= ,longitude=2"),
    ).toBe("latitude=1,name= ,longitude=3");
  });

  it("removes a cleared name and keeps a key it has no field for", () => {
    expect(
      buildLocation(
        place("12.5", "-45.25"),
        "latitude=12.5,name=Site A,foo=bar,longitude=-45.25",
      ),
    ).toBe("latitude=12.5,foo=bar,longitude=-45.25");
  });

  it("is no value at all with every field empty", () => {
    expect(buildLocation(place("", ""))).toBe("");
    expect(
      buildLocation(place("", ""), "latitude=12.5,longitude=-45.25,foo=bar"),
    ).toBe("");
  });

  it("builds over a value it cannot read as if there were none", () => {
    expect(buildLocation(place("1", "2"), "Site A")).toBe(
      "latitude=1,longitude=2",
    );
  });
});

describe("what the card shows", () => {
  it.each([
    [undefined, "Default (no delay)"],
    [0, "0 seconds"],
    [1, "1 second"],
    [30, "30 seconds"],
    [300, "300 seconds"],
  ])("describes a start delay of %j", (seconds, text) => {
    expect(describeStartDelay(seconds)).toBe(text);
  });

  it.each([
    [undefined, "Default (80%)"],
    [0, "0%"],
    [60, "60%"],
    [100, "100%"],
  ])("describes a ballooning target of %j", (percent, text) => {
    expect(describeBallooningTarget(percent)).toBe(text);
  });

  it.each([
    [undefined, "Not configured"],
    ["", "Not configured"],
    [MAC, MAC],
    [`mac=${MAC}`, MAC],
    [`${MAC},bind-interface=vmbr0`, `${MAC} via vmbr0`],
    [`${MAC},broadcast-address=192.0.2.255`, `${MAC}, broadcast 192.0.2.255`],
    [
      `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      `${MAC} via vmbr0, broadcast 192.0.2.255`,
    ],
    // Not one Nexara can read: shown as stored.
    [`${MAC},${MAC_2}`, `${MAC},${MAC_2}`],
    ["bind-interface=vmbr0", "bind-interface=vmbr0"],
  ])("describes wakeonlan %j", (raw, text) => {
    expect(describeWakeOnLan(raw)).toBe(text);
  });

  it.each([
    [undefined, "From datacenter"],
    ["", "From datacenter"],
    ["latitude=12.5,longitude=-45.25", "12.5, -45.25"],
    ["latitude=12.5,longitude=-45.25,name=Site A", "Site A (12.5, -45.25)"],
    // A name of whitespace shows as none.
    ["latitude=1,name= ,longitude=2", "1, 2"],
    ["Site A", "Site A"],
    ["latitude=1", "latitude=1"],
  ])("describes location %j", (raw, text) => {
    expect(describeLocation(raw)).toBe(text);
  });

  it.each([
    [undefined, null],
    ["", null],
    ["\n", null],
    ["  \n \n", null],
    ["sentinel notes\n", "sentinel notes"],
    ["line one\nline two\n", "line one\nline two"],
    ["  indented\n", "  indented"],
    ["<b>x</b>\n", "<b>x</b>"],
  ])("shows the notes %j as %j", (description, shown) => {
    expect(visibleNotes(description)).toBe(shown);
  });
});

describe("nodeOptionSupport", () => {
  const NODE = (version: string) => `pve-manager/${version}/0123abcd`;

  // Each version is the first release of one floor, or the one before it.
  const flags = (
    ballooningTarget: boolean,
    wolBindBroadcast: boolean,
    location: boolean,
  ) => ({ ballooningTarget, wolBindBroadcast, location });

  it.each([
    ["9.2.20", flags(true, true, true)],
    ["9.1.13", flags(true, true, true)],
    ["9.1.12", flags(true, true, false)],
    ["8.3.6", flags(true, true, false)],
    ["8.3.5", flags(false, true, false)],
    ["8.1.9", flags(false, true, false)],
    ["8.1.8", flags(false, false, false)],
    ["7.4.1", flags(false, false, false)],
    // An unknown version hides every gated field.
    ["", flags(false, false, false)],
  ])("for a read holding none of them, on %j", (version, support) => {
    expect(nodeOptionSupport(version === "" ? "" : NODE(version), {})).toEqual(
      support,
    );
  });

  it.each([
    ["", ""],
    ["7.4.1", NODE("7.4.1")],
    ["8.3.5", NODE("8.3.5")],
  ])("offers what the read already holds, on version %j", (_, version) => {
    expect(
      nodeOptionSupport(version, { "ballooning-target": 60 }),
    ).toMatchObject({ ballooningTarget: true });
    // A target of 0 is a value too.
    expect(
      nodeOptionSupport(version, { "ballooning-target": 0 }),
    ).toMatchObject({ ballooningTarget: true });
    expect(
      nodeOptionSupport(version, {
        location: "latitude=1,longitude=2",
      }),
    ).toMatchObject({ location: true });
    expect(
      nodeOptionSupport(version, { wakeonlan: `${MAC},bind-interface=vmbr0` }),
    ).toMatchObject({ wolBindBroadcast: true });
    expect(
      nodeOptionSupport(version, {
        wakeonlan: `mac=${MAC},broadcast-address=192.0.2.255`,
      }),
    ).toMatchObject({ wolBindBroadcast: true });
  });

  it("does not take the bare MAC of a Wake-on-LAN value for its subkeys", () => {
    expect(nodeOptionSupport(NODE("8.1.8"), { wakeonlan: MAC })).toMatchObject({
      wolBindBroadcast: false,
    });
    expect(nodeOptionSupport(NODE("8.1.8"), { wakeonlan: "" })).toMatchObject({
      wolBindBroadcast: false,
    });
    expect(nodeOptionSupport(NODE("8.1.8"), { location: "" })).toMatchObject({
      location: false,
    });
  });

  it("does not offer the subkeys on the strength of a value it cannot read", () => {
    expect(
      nodeOptionSupport("", { wakeonlan: `${MAC},${MAC_2},bind-interface=a` }),
    ).toMatchObject({ wolBindBroadcast: false });
  });
});

describe("changedNodeOptions", () => {
  const OPENED: NodeConfigRead = {
    "startall-onboot-delay": 30,
    "ballooning-target": 60,
    wakeonlan: MAC,
    location: "latitude=12.5,longitude=-45.25",
    description: "sentinel notes\n",
    digest: "d1",
  };

  it("is empty when the dialog edits nothing", () => {
    expect(changedNodeOptions(OPENED, {})).toEqual({});
    expect(changedNodeOptions({}, {})).toEqual({});
  });

  it("is empty when every draft is what was opened", () => {
    expect(
      changedNodeOptions(OPENED, {
        "startall-onboot-delay": 30,
        "ballooning-target": 60,
        wakeonlan: MAC,
        location: "latitude=12.5,longitude=-45.25",
        description: "sentinel notes",
      }),
    ).toEqual({});
  });

  it("never sends a key the dialog does not edit", () => {
    expect(changedNodeOptions(OPENED, { "startall-onboot-delay": 60 })).toEqual(
      { "startall-onboot-delay": 60 },
    );
    expect(changedNodeOptions(OPENED, { description: "changed" })).toEqual({
      description: "changed",
    });
  });

  it("sends the digest-less body: the changed keys and nothing else", () => {
    expect(
      changedNodeOptions(OPENED, {
        "startall-onboot-delay": 31,
        "ballooning-target": 60,
        wakeonlan: MAC_2,
      }),
    ).toEqual({ "startall-onboot-delay": 31, wakeonlan: MAC_2 });
  });

  it("sends an integer set where there was none, and 0 as a value", () => {
    expect(
      changedNodeOptions(
        {},
        { "startall-onboot-delay": 0, "ballooning-target": 0 },
      ),
    ).toEqual({ "startall-onboot-delay": 0, "ballooning-target": 0 });
    expect(
      changedNodeOptions(
        { "startall-onboot-delay": 30, "ballooning-target": 60 },
        { "startall-onboot-delay": 0, "ballooning-target": 0 },
      ),
    ).toEqual({ "startall-onboot-delay": 0, "ballooning-target": 0 });
  });

  it("sends nothing for 0 where 0 was stored", () => {
    expect(
      changedNodeOptions(
        { "startall-onboot-delay": 0 },
        { "startall-onboot-delay": 0 },
      ),
    ).toEqual({});
  });

  it("clears a set value through delete and never sends it as empty", () => {
    const changes = changedNodeOptions(OPENED, {
      "startall-onboot-delay": null,
      "ballooning-target": null,
      wakeonlan: null,
      location: null,
      description: null,
    });
    expect(changes).toEqual({
      delete: [
        "startall-onboot-delay",
        "ballooning-target",
        "wakeonlan",
        "location",
        "description",
      ],
    });
    expect(JSON.stringify(changes)).not.toContain('""');
  });

  it("sends nothing for an unset value left unset", () => {
    expect(
      changedNodeOptions(
        {},
        {
          "startall-onboot-delay": null,
          "ballooning-target": null,
          wakeonlan: null,
          location: null,
          description: null,
        },
      ),
    ).toEqual({});
    // An empty string is unset on the way in too.
    expect(
      changedNodeOptions(
        { wakeonlan: "", location: "", description: "" },
        { wakeonlan: null, location: null, description: null },
      ),
    ).toEqual({});
  });

  it("treats an empty string draft as unset, so it is never sent", () => {
    expect(changedNodeOptions(OPENED, { wakeonlan: "", location: "" })).toEqual(
      {
        delete: ["wakeonlan", "location"],
      },
    );
    expect(changedNodeOptions({}, { wakeonlan: "", location: "" })).toEqual({});
  });

  it("puts a key in exactly one bucket: set or delete, never both", () => {
    const changes = changedNodeOptions(OPENED, {
      "startall-onboot-delay": 31,
      "ballooning-target": null,
      wakeonlan: null,
      location: "latitude=1,longitude=2",
    });
    expect(changes).toEqual({
      "startall-onboot-delay": 31,
      location: "latitude=1,longitude=2",
      delete: ["ballooning-target", "wakeonlan"],
    });
    for (const key of changes.delete ?? []) {
      expect(changes).not.toHaveProperty([key]);
    }
  });

  it("compares strings by their exact bytes", () => {
    const lower = "02:00:00:00:00:ab";
    expect(
      changedNodeOptions({ wakeonlan: lower }, { wakeonlan: lower }),
    ).toEqual({});
    expect(
      changedNodeOptions(
        { wakeonlan: lower },
        { wakeonlan: lower.toUpperCase() },
      ),
    ).toEqual({ wakeonlan: lower.toUpperCase() });
    expect(changedNodeOptions(OPENED, { wakeonlan: ` ${MAC}` })).toEqual({
      wakeonlan: ` ${MAC}`,
    });
  });

  describe("the description", () => {
    it("does not count the trailing newline Proxmox gives every line", () => {
      expect(
        changedNodeOptions(
          { description: "line one\nline two\n" },
          { description: "line one\nline two" },
        ),
      ).toEqual({});
    });

    it("takes off only one trailing newline from what was read", () => {
      expect(
        changedNodeOptions(
          { description: "line one\n\n" },
          { description: "line one\n" },
        ),
      ).toEqual({});
      expect(
        changedNodeOptions(
          { description: "line one\n\n" },
          { description: "line one" },
        ),
      ).toEqual({ description: "line one" });
    });

    it("sends a changed description as typed", () => {
      expect(
        changedNodeOptions(
          { description: "sentinel notes\n" },
          { description: "sentinel notes\nand more" },
        ),
      ).toEqual({ description: "sentinel notes\nand more" });
      // Line breaks, a trailing one included, are the author's.
      expect(
        changedNodeOptions({}, { description: "line one\n\nline three\n" }),
      ).toEqual({ description: "line one\n\nline three\n" });
    });

    it("counts whitespace alone as none, on either side", () => {
      expect(
        changedNodeOptions(
          { description: "sentinel notes\n" },
          { description: "   \n " },
        ),
      ).toEqual({ delete: ["description"] });
      expect(
        changedNodeOptions({ description: "\n" }, { description: null }),
      ).toEqual({});
      expect(
        changedNodeOptions({ description: "  \n" }, { description: "text" }),
      ).toEqual({ description: "text" });
      expect(changedNodeOptions({}, { description: "   " })).toEqual({});
    });
  });
});

describe("optionsChangedBetween", () => {
  const ALL = { ballooningTarget: true, location: true };
  const BASE: NodeOptions = {
    "startall-onboot-delay": 30,
    "ballooning-target": 60,
    wakeonlan: MAC,
    location: "latitude=12.5,longitude=-45.25",
    digest: "d1",
  };

  it("finds nothing when the two reads agree, whatever their digests say", () => {
    expect(optionsChangedBetween(BASE, { ...BASE, digest: "d2" }, ALL)).toEqual(
      [],
    );
    expect(optionsChangedBetween({}, {}, ALL)).toEqual([]);
  });

  it.each([
    ["the delay", { "startall-onboot-delay": 31 }, ["startall-onboot-delay"]],
    [
      "the ballooning target",
      { "ballooning-target": 61 },
      ["ballooning-target"],
    ],
    ["Wake-on-LAN", { wakeonlan: MAC_2 }, ["wakeonlan"]],
    ["the location", { location: "latitude=1,longitude=2" }, ["location"]],
  ])("names %s when it changed", (_, over, keys) => {
    expect(optionsChangedBetween(BASE, { ...BASE, ...over }, ALL)).toEqual(
      keys,
    );
  });

  it("lists several in the order the card lists them", () => {
    expect(
      optionsChangedBetween(
        BASE,
        {
          ...BASE,
          location: "latitude=1,longitude=2",
          wakeonlan: MAC_2,
          "startall-onboot-delay": 31,
        },
        ALL,
      ),
    ).toEqual(["startall-onboot-delay", "wakeonlan", "location"]);
  });

  it("counts a setting that was set or unset as changed, and 0 as a value", () => {
    expect(
      optionsChangedBetween({}, { "startall-onboot-delay": 0 }, ALL),
    ).toEqual(["startall-onboot-delay"]);
    expect(optionsChangedBetween({ wakeonlan: MAC }, {}, ALL)).toEqual([
      "wakeonlan",
    ]);
    expect(
      optionsChangedBetween(
        { "startall-onboot-delay": 30 },
        { "startall-onboot-delay": 0 },
        ALL,
      ),
    ).toEqual(["startall-onboot-delay"]);
  });

  it("takes an empty string for unset", () => {
    expect(
      optionsChangedBetween({ wakeonlan: "" }, { location: "" }, ALL),
    ).toEqual([]);
    expect(optionsChangedBetween({}, { wakeonlan: "" }, ALL)).toEqual([]);
  });

  it("compares a string by its exact bytes", () => {
    expect(
      optionsChangedBetween(
        { wakeonlan: MAC },
        { wakeonlan: `mac=${MAC}` },
        ALL,
      ),
    ).toEqual(["wakeonlan"]);
  });

  it("leaves out a setting the dialog does not show", () => {
    const changed: NodeOptions = {
      ...BASE,
      "ballooning-target": 70,
      location: "latitude=1,longitude=2",
      wakeonlan: MAC_2,
    };
    expect(
      optionsChangedBetween(BASE, changed, {
        ballooningTarget: false,
        location: false,
      }),
    ).toEqual(["wakeonlan"]);
    expect(
      optionsChangedBetween(BASE, changed, {
        ballooningTarget: true,
        location: false,
      }),
    ).toEqual(["ballooning-target", "wakeonlan"]);
    expect(
      optionsChangedBetween(BASE, changed, {
        ballooningTarget: false,
        location: true,
      }),
    ).toEqual(["wakeonlan", "location"]);
  });
});

describe("notesChangedBetween", () => {
  it.each([
    [{ description: "sentinel notes\n" }, { description: "sentinel notes\n" }],
    // The newline Proxmox gives every line is not a difference.
    [{ description: "sentinel notes\n" }, { description: "sentinel notes" }],
    [{ description: "a\nb\n" }, { description: "a\nb" }],
    // Whitespace alone is none.
    [{}, { description: "\n" }],
    [{ description: "  \n" }, {}],
    [{}, {}],
    [
      { description: "x", digest: "d1" },
      { description: "x", digest: "d2" },
    ],
  ])("finds no change between %j and %j", (was, now) => {
    expect(notesChangedBetween(was, now)).toBe(false);
  });

  it.each([
    [{ description: "sentinel notes\n" }, { description: "other notes\n" }],
    [{ description: "sentinel notes\n" }, {}],
    [{}, { description: "sentinel notes\n" }],
    [{ description: "a\nb\n" }, { description: "a\n\nb\n" }],
    [{ description: "a\n" }, { description: "a \n" }],
  ])("finds a change between %j and %j", (was, now) => {
    expect(notesChangedBetween(was, now)).toBe(true);
  });
});

describe("NODE_OPTION_LABELS", () => {
  it("names every setting the way the card does", () => {
    expect(NODE_OPTION_LABELS).toEqual({
      "startall-onboot-delay": "Start on boot delay",
      "ballooning-target": "RAM ballooning target",
      wakeonlan: "Wake-on-LAN",
      location: "Location",
      description: "Notes",
    });
  });
});

describe("descriptionForEditing", () => {
  it.each([
    [undefined, ""],
    ["", ""],
    ["one\n", "one"],
    ["one\ntwo\n", "one\ntwo"],
    ["one\n\n", "one\n"],
    ["one", "one"],
  ])("starts the editor with %j as %j", (stored, text) => {
    expect(descriptionForEditing(stored)).toBe(text);
  });
});

describe("checkInteger", () => {
  it.each([
    ["", null],
    ["   ", null],
    ["0", 0],
    ["30", 30],
    ["99", 99],
    [" 30 ", 30],
    ["300", 300],
    ["+5", 5],
    ["05", 5],
    ["-0", 0],
  ])("accepts %j for 0-300", (text, value) => {
    expect(checkInteger(text, 0, 300)).toEqual({ ok: true, value });
    expect(Object.is(value, -0)).toBe(false);
  });

  it.each([
    ["301"],
    ["-1"],
    ["1.5"],
    ["1e2"],
    ["0x10"],
    ["3 0"],
    ["thirty"],
    ["٣٠"],
    ["99999999999999999999"],
    ["-"],
  ])("refuses %j for 0-300", (text) => {
    expect(checkInteger(text, 0, 300)).toEqual({
      ok: false,
      error: "Enter a whole number from 0 to 300.",
    });
  });

  it("applies the range it is given", () => {
    expect(checkInteger("100", 0, 100)).toEqual({ ok: true, value: 100 });
    expect(checkInteger("101", 0, 100)).toEqual({
      ok: false,
      error: "Enter a whole number from 0 to 100.",
    });
  });

  it("returns a zero that serialises as 0", () => {
    const checked = checkInteger("-0", 0, 300);
    expect(checked.ok && JSON.stringify(checked.value)).toBe("0");
  });
});

describe("checkMacAddress", () => {
  it.each([
    "02:00:00:00:00:01",
    "02:00:00:00:00:AB",
    "0A:bc:De:f0:12:34",
    "12:34:56:78:9a:bc",
    "fe:ff:ff:ff:ff:ff",
  ])("accepts %s", (mac) => {
    expect(checkMacAddress(mac)).toEqual({ ok: true, value: mac });
  });

  it("trims, and an empty field passes as unset", () => {
    expect(checkMacAddress(` ${MAC} `)).toEqual({ ok: true, value: MAC });
    expect(checkMacAddress("")).toEqual({ ok: true, value: "" });
  });

  it.each([
    // The I/G bit is set: a multicast address, which Proxmox refuses.
    "03:00:00:00:00:01",
    "01:00:5e:00:00:01",
    "ff:ff:ff:ff:ff:ff",
    // Only the colon form is one.
    "02-00-00-00-00-01",
    "0200.0000.0001",
    "020000000001",
    "02:00:00:00:00",
    "02:00:00:00:00:01:02",
    "02:00:00:00:00:0g",
    "02:00:00:00:0:01",
    "not a mac",
  ])("refuses %s", (mac) => {
    expect(checkMacAddress(mac).ok).toBe(false);
  });
});

describe("checkInterfaceName", () => {
  it.each([
    "vmbr0",
    "eth0",
    "bond0.10",
    "eth0:1",
    "VMBR0",
    "en_p1",
    "ab",
    // 21 characters: a letter and twenty more.
    "a".repeat(21),
    // Perl's \d is any Unicode digit, so Proxmox takes a suffix of Arabic-Indic
    // ones (٣ is 3, ١٠ is 10) and this must not refuse what it accepts.
    "eth0.\u0661\u0660",
    "eth0:\u0663",
    "bond0.\u0967\u0968",
  ])("accepts %s", (name) => {
    expect(checkInterfaceName(name)).toEqual({ ok: true, value: name });
  });

  it.each([
    "a",
    "1eth",
    "en-p1",
    "a".repeat(22),
    "eth0.",
    "eth0.x",
    "eth 0",
    "eth0:1:2",
    "_eth",
  ])("refuses %s", (name) => {
    expect(checkInterfaceName(name).ok).toBe(false);
  });

  it("passes empty as unset", () => {
    expect(checkInterfaceName("  ")).toEqual({ ok: true, value: "" });
  });
});

describe("checkIPv4Address", () => {
  it.each([
    "192.0.2.255",
    "255.255.255.255",
    "0.0.0.0",
    "10.1.2.3",
    "1.2.3.4",
    "249.250.99.100",
  ])("accepts %s", (address) => {
    expect(checkIPv4Address(address)).toEqual({ ok: true, value: address });
  });

  it.each([
    "256.0.0.1",
    "01.2.3.4",
    "1.2.3",
    "1.2.3.4.5",
    "1.2.3.04",
    "1.2.3.",
    "a.b.c.d",
    "192.0.2.255/24",
    "::1",
    " 1.2.3.4 x",
  ])("refuses %s", (address) => {
    expect(checkIPv4Address(address).ok).toBe(false);
  });

  it("passes empty as unset", () => {
    expect(checkIPv4Address("")).toEqual({ ok: true, value: "" });
  });
});

describe("checkCoordinate", () => {
  it.each([
    "0",
    "12.5",
    "-45.25",
    "+7",
    "1.",
    ".5",
    "-.5",
    "1e1",
    "1E1",
    "89.999999",
    "90",
    "-90",
    "90.0",
    "5e-1",
  ])("accepts %s as a latitude", (text) => {
    expect(checkCoordinate(text, "latitude", 90)).toEqual({
      ok: true,
      value: text,
    });
  });

  it.each(["91", "-90.0001", "1e3", "90.00001", "1e999"])(
    "refuses %s as a latitude: out of range",
    (text) => {
      expect(checkCoordinate(text, "latitude", 90)).toEqual({
        ok: false,
        error: "Enter the latitude as a number from -90 to 90.",
      });
    },
  );

  it.each(["0x10", " 1 2", "Infinity", "NaN", "1,5", "abc", "1e", "e1", "."])(
    "refuses %s as a latitude: not a number",
    (text) => {
      expect(checkCoordinate(text, "latitude", 90).ok).toBe(false);
    },
  );

  it("trims, and an empty field passes (a dialog decides if it is needed)", () => {
    expect(checkCoordinate(" 12.5 ", "latitude", 90)).toEqual({
      ok: true,
      value: "12.5",
    });
    expect(checkCoordinate("", "latitude", 90)).toEqual({
      ok: true,
      value: "",
    });
  });

  it("holds a longitude to 180", () => {
    expect(checkCoordinate("180", "longitude", 180).ok).toBe(true);
    expect(checkCoordinate("-180", "longitude", 180).ok).toBe(true);
    expect(checkCoordinate("-45.25", "longitude", 180).ok).toBe(true);
    expect(checkCoordinate("180.5", "longitude", 180)).toEqual({
      ok: false,
      error: "Enter the longitude as a number from -180 to 180.",
    });
  });

  it("leaves a digit JavaScript cannot read to Proxmox rather than refusing it", () => {
    // is_number's \d matches any Unicode digit, so Proxmox may accept this one.
    expect(checkCoordinate("٣٠", "latitude", 90).ok).toBe(true);
  });
});

describe("checkLocationName", () => {
  it("accepts 128 characters, counted as code points", () => {
    expect(checkLocationName("😀".repeat(128)).ok).toBe(true);
    expect(checkLocationName("a".repeat(128)).ok).toBe(true);
  });

  it("refuses 129", () => {
    expect(checkLocationName("😀".repeat(129))).toEqual({
      ok: false,
      error: "The name can be at most 128 characters.",
    });
    expect(checkLocationName("a".repeat(129)).ok).toBe(false);
  });

  it("refuses a comma, which a property string cannot carry", () => {
    expect(checkLocationName("Site A, rack01")).toEqual({
      ok: false,
      error: "The name cannot contain a comma.",
    });
  });

  it("accepts every other character, and trims", () => {
    expect(checkLocationName(' Site A = "1" ')).toEqual({
      ok: true,
      value: 'Site A = "1"',
    });
    expect(checkLocationName("")).toEqual({ ok: true, value: "" });
  });
});

describe("checkNotes", () => {
  it("accepts 65536 characters and refuses 65537", () => {
    expect(checkNotes("é".repeat(NOTES_MAX_LENGTH)).ok).toBe(true);
    expect(checkNotes("é".repeat(NOTES_MAX_LENGTH + 1))).toEqual({
      ok: false,
      // The schema's own limit, and not a promise: an older node can refuse a
      // long note sooner (the request size cap is lower before PVE 8.4).
      error:
        "Notes can be at most 65536 characters; on Proxmox VE before 8.4 a long note can be refused sooner.",
    });
  });

  it("counts code points, not UTF-16 units", () => {
    // 32769 emoji are 65538 UTF-16 units but 32769 characters.
    expect(checkNotes("😀".repeat(32769)).ok).toBe(true);
    expect(checkNotes("😀".repeat(NOTES_MAX_LENGTH + 1)).ok).toBe(false);
  });

  it("returns the text untouched, newlines and all", () => {
    expect(checkNotes("a\nb  ")).toEqual({ ok: true, value: "a\nb  " });
  });
});

describe("characterCount", () => {
  it("counts a code point once", () => {
    expect(characterCount("")).toBe(0);
    expect(characterCount("abc")).toBe(3);
    expect(characterCount("😀")).toBe(1);
    expect(characterCount("é😀")).toBe(2);
  });
});
