import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { ClusterFirewallTab } from "./ClusterFirewallTab";

const CLUSTER = "c1";
const FW = `/api/v1/clusters/${CLUSTER}/firewall`;

// Two of everything, so a delete of the wrong row is caught.
const LISTS = {
  [`${FW}/rules`]: listOf([]),
  [`${FW}/aliases`]: listOf([
    { name: "net01", cidr: "192.0.2.0/25", comment: "" },
    { name: "net02", cidr: "192.0.2.128/25", comment: "" },
  ]),
  [`${FW}/ipset`]: listOf([
    { name: "set01", comment: "" },
    { name: "set02", comment: "" },
  ]),
  [`${FW}/ipset/set02/entries`]: listOf([
    { cidr: "192.0.2.0/24", comment: "" },
    { cidr: "192.0.2.77", nomatch: 1, comment: "" },
  ]),
  [`${FW}/groups`]: listOf([
    { group: "grp01", comment: "" },
    { group: "grp02", comment: "" },
  ]),
};

let api: ReturnType<typeof stubApi>;

afterEach(() => {
  vi.unstubAllGlobals();
});

async function renderOnTab(tab: string) {
  api = stubApi(LISTS);
  const user = userEvent.setup();
  renderWithProviders(<ClusterFirewallTab clusterId={CLUSTER} />);
  await user.click(await screen.findByRole("tab", { name: tab }));
  return user;
}

type User = ReturnType<typeof userEvent.setup>;

/**
 * The three things every site must do: the button only asks (naming the
 * item), Cancel sends nothing, and the confirm button sends exactly `request`.
 */
async function expectConfirmedDelete(
  user: User,
  opts: {
    button: string;
    heading: string;
    text: string;
    confirm: string;
    request: string;
    /** Run after every step, open and closed: what must stay true throughout. */
    invariant?: () => void;
  },
) {
  const invariant = opts.invariant ?? (() => undefined);
  await user.click(await screen.findByRole("button", { name: opts.button }));
  let dialog = await screen.findByRole("alertdialog");
  expect(
    within(dialog).getByRole("heading", { name: opts.heading }),
  ).toBeInTheDocument();
  expect(dialog).toHaveTextContent(opts.text);
  expect(api.writes()).toEqual([]);
  invariant();

  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await waitFor(() => {
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
  expect(api.writes()).toEqual([]);
  invariant();

  await user.click(screen.getByRole("button", { name: opts.button }));
  dialog = await screen.findByRole("alertdialog");
  expect(api.writes()).toEqual([]);
  invariant();
  await user.click(within(dialog).getByRole("button", { name: opts.confirm }));
  await waitFor(() => {
    expect(api.writes()).toEqual([opts.request]);
  });
  invariant();
}

describe("ClusterFirewallTab — every delete asks first", () => {
  it("alias", async () => {
    const user = await renderOnTab("Aliases");
    await expectConfirmedDelete(user, {
      button: "Delete alias net02",
      heading: "Delete alias net02?",
      text: "Proxmox removes the alias net02 (192.0.2.128/25) from the cluster firewall at once, without checking whether a rule or IP set still refers to it.",
      confirm: "Delete Alias",
      request: `DELETE ${FW}/aliases/net02`,
    });
  });

  it("IP set", async () => {
    const user = await renderOnTab("IP Sets");
    await expectConfirmedDelete(user, {
      button: "Delete IPSet set02",
      heading: "Delete IP set set02?",
      text: "Proxmox deletes the IP set set02 only if it has no entries left",
      confirm: "Delete IP Set",
      request: `DELETE ${FW}/ipset/set02`,
      // Neither the button nor the dialog's own clicks select the row they
      // sit over (a row click opens its entries card, as the next test
      // relies on). Checked after every step: two clicks toggle it back.
      invariant: () => {
        expect(screen.queryByText(/Entries in/)).not.toBeInTheDocument();
      },
    });
  });

  it("IP set entry", async () => {
    const user = await renderOnTab("IP Sets");
    await user.click(await screen.findByText("set02"));
    await expectConfirmedDelete(user, {
      button: "Delete entry 192.0.2.0/24",
      heading: "Remove 192.0.2.0/24 from IP set set02?",
      text: "Proxmox removes 192.0.2.0/24 from the IP set set02 at once; unless another entry of the set covers them, its addresses stop matching the set",
      confirm: "Remove Entry",
      request: `DELETE ${FW}/ipset/set02/entries/192.0.2.0%2F24`,
      invariant: () => {
        expect(screen.queryByText(/lifts the exclusion/)).toBeNull();
      },
    });
  });

  // A nomatch entry is an exclusion: removing it makes the set match MORE.
  it("nomatch IP set entry", async () => {
    const user = await renderOnTab("IP Sets");
    await user.click(await screen.findByText("set02"));
    await expectConfirmedDelete(user, {
      button: "Delete entry 192.0.2.77",
      heading: "Remove !192.0.2.77 from IP set set02?",
      text: "This is a nomatch entry: it excludes 192.0.2.77 from the IP set set02. Proxmox removes it at once, which lifts the exclusion — addresses in 192.0.2.77 that another entry of the set covers start matching the set, and rules that use the set start applying to them",
      confirm: "Remove Entry",
      request: `DELETE ${FW}/ipset/set02/entries/192.0.2.77`,
      invariant: () => {
        expect(screen.queryByText(/stop matching/)).not.toBeInTheDocument();
      },
    });
  });

  it("security group", async () => {
    const user = await renderOnTab("Security Groups");
    await expectConfirmedDelete(user, {
      button: "Delete security group grp02",
      heading: "Delete security group grp02?",
      text: "Proxmox deletes the security group grp02 only if it has no rules left — delete its rules first. It does not check whether any rule still refers to the group.",
      confirm: "Delete Group",
      request: `DELETE ${FW}/groups/grp02`,
    });
  });
});
