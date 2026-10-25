import { describe, expect, it } from "vitest";
import type { ConsoleTarget } from "../types/console";
import {
  consoleSessionHref,
  parseConsoleSessionTarget,
  wantsNewTab,
} from "./console-session";

function parse(href: string): ConsoleTarget | null {
  return parseConsoleSessionTarget(
    new URL(href, "https://nexara.example.com").searchParams,
  );
}

const guest = {
  clusterID: "cluster01",
  node: "pve-01",
  vmid: 101,
  label: "VNC: linux01",
  resourceId: "resource-01",
} as const;

describe("console session links", () => {
  // Out through the launchers' href and back as the page reads it. The label is
  // the page's own (the guest's name arrives with its query), and the kind
  // follows from the type, so neither rides in the link.
  it.each<[string, ConsoleTarget, ConsoleTarget]>([
    [
      "node shell",
      {
        clusterID: "cluster01",
        node: "pve-01",
        type: "node_shell",
        label: "Shell: pve-01",
      },
      {
        clusterID: "cluster01",
        node: "pve-01",
        type: "node_shell",
        label: "Shell: pve-01",
      },
    ],
    [
      "VM VNC console",
      { ...guest, type: "vm_vnc", kind: "vm" },
      { ...guest, type: "vm_vnc", kind: "vm", label: "VNC: 101" },
    ],
    [
      "container console",
      { ...guest, vmid: 201, type: "ct_vnc", kind: "ct" },
      { ...guest, vmid: 201, type: "ct_vnc", kind: "ct", label: "CT: 201" },
    ],
    [
      "guest console without a resource id",
      {
        clusterID: "cluster01",
        node: "pve-01",
        vmid: 101,
        type: "vm_vnc",
        label: "VNC: linux01",
      },
      {
        clusterID: "cluster01",
        node: "pve-01",
        vmid: 101,
        type: "vm_vnc",
        label: "VNC: 101",
      },
    ],
  ])("round-trips a %s", (_, target, parsed) => {
    expect(parse(consoleSessionHref(target))).toEqual(parsed);
  });

  it.each([
    ["no type", "/console/session?cluster=cluster01&node=pve-01"],
    [
      "an unknown type",
      "/console/session?cluster=cluster01&node=pve-01&type=spice&vmid=101",
    ],
    // An inherited property is not a console type (an `in` check would say it is).
    [
      "a type named after an inherited property",
      "/console/session?cluster=cluster01&node=pve-01&type=constructor&vmid=101",
    ],
    ["no cluster", "/console/session?node=pve-01&type=node_shell"],
    ["no node", "/console/session?cluster=cluster01&type=node_shell"],
    [
      "a guest console without a VMID",
      "/console/session?cluster=cluster01&node=pve-01&type=vm_vnc",
    ],
    [
      "a VMID that is not a whole positive number",
      "/console/session?cluster=cluster01&node=pve-01&type=vm_vnc&vmid=10.5",
    ],
  ])("names no console with %s", (_, href) => {
    expect(parse(href)).toBeNull();
  });
});

describe("wantsNewTab", () => {
  it.each([
    ["floating", false, false, false],
    ["tab", false, false, true],
    ["floating", true, false, true],
    ["floating", false, true, true],
    // A value this build does not know, from storage or the server.
    ["window", false, false, false],
  ])(
    "preference %s, Ctrl %s, ⌘ %s → new tab %s",
    (mode, ctrlKey, metaKey, expected) => {
      expect(wantsNewTab(mode, { ctrlKey, metaKey })).toBe(expected);
    },
  );
});
