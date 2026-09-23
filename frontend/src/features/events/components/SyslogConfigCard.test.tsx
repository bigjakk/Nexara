import { useLayoutEffect, useRef } from "react";
import { describe, it, expect, vi, beforeEach, type Mock } from "vitest";
import { fireEvent, screen, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { ApiClientError } from "@/lib/api-client";
import type { ApiError } from "@/types/api";
import { SyslogConfigCard } from "./SyslogConfigCard";
import {
  useSyslogConfig,
  useSaveSyslogConfig,
  useTestSyslog,
  type SyslogConfig,
} from "../api/events-queries";

vi.mock("../api/events-queries", () => ({
  useSyslogConfig: vi.fn(),
  useSaveSyslogConfig: vi.fn(),
  useTestSyslog: vi.fn(),
}));

type Send = Mock<(cfg: SyslogConfig) => void>;

// What GET returns before anything has been saved (defaultSyslogConfig in
// internal/api/handlers/audit.go). Forwarding is off, so the card starts
// collapsed.
const unsaved: SyslogConfig = {
  enabled: false,
  host: "",
  port: 514,
  protocol: "udp",
  facility: 16,
  tls_skip_verify: false,
};

const forwarding: SyslogConfig = {
  enabled: true,
  host: "syslog.example.com",
  port: 514,
  protocol: "udp",
  facility: 16,
  tls_skip_verify: false,
};

/** How a mutation's last call ended: with a reply, or with an error. */
type Outcome = { reply: unknown } | { error: Error };

/**
 * A mutation hook, recording what it was asked to send. Given an `outcome`, it
 * is one whose last call ended that way; otherwise it is parked idle.
 */
function mutation(mutate: Send, outcome?: Outcome) {
  const reply = outcome && "reply" in outcome ? outcome.reply : undefined;
  const error = outcome && "error" in outcome ? outcome.error : null;
  return {
    mutate,
    isPending: false,
    isSuccess: reply !== undefined,
    isError: error !== null,
    error,
    data: reply,
  };
}

/** A syslog-config query result: settled, idle and holding no data unless told. */
function queryResult(state: Record<string, unknown>) {
  return {
    data: undefined,
    isLoading: false,
    isError: false,
    isSuccess: false,
    isPaused: false,
    error: null,
    errorUpdatedAt: 0,
    fetchStatus: "idle",
    refetch: vi.fn(),
    ...state,
  } as unknown as ReturnType<typeof useSyslogConfig>;
}

/**
 * Serves `config` as the saved config. Each call hands out a new object, so
 * calling it again and re-rendering is how a test stages a refetch that
 * brought back different data. `ended` stages a Save or a Test that has
 * already come back.
 */
function mockHooks(
  config: SyslogConfig,
  ended: { save?: Outcome; test?: Outcome } = {},
) {
  const save: Send = vi.fn();
  const test: Send = vi.fn();
  vi.mocked(useSyslogConfig).mockReturnValue(
    queryResult({ data: { ...config }, isSuccess: true }),
  );
  vi.mocked(useSaveSyslogConfig).mockReturnValue(
    mutation(save, ended.save) as unknown as ReturnType<
      typeof useSaveSyslogConfig
    >,
  );
  vi.mocked(useTestSyslog).mockReturnValue(
    mutation(test, ended.test) as unknown as ReturnType<typeof useTestSyslog>,
  );
  return { save, test };
}

/**
 * Serves a syslog-config query that holds no config, in `state`. Returns the
 * save mock, which no test of that state should ever see called.
 */
function mockUnread(state: Record<string, unknown>) {
  const save: Send = vi.fn();
  vi.mocked(useSyslogConfig).mockReturnValue(queryResult(state));
  vi.mocked(useSaveSyslogConfig).mockReturnValue(
    mutation(save) as unknown as ReturnType<typeof useSaveSyslogConfig>,
  );
  vi.mocked(useTestSyslog).mockReturnValue(
    mutation(vi.fn()) as unknown as ReturnType<typeof useTestSyslog>,
  );
  return save;
}

/** Renders the card over a loaded config, opened if it starts collapsed. */
async function renderCard(user: UserEvent, config: SyslogConfig) {
  const sent = mockHooks(config);
  const { rerender } = renderWithProviders(<SyslogConfigCard />);
  if (!config.enabled) {
    await user.click(screen.getByRole("button", { name: /Configure/ }));
  }
  return { ...sent, rerender };
}

/** The body of the one call a mutation received. */
function onlyBody(mutate: Send): SyslogConfig | undefined {
  expect(mutate).toHaveBeenCalledTimes(1);
  return mutate.mock.calls[0]?.[0];
}

const portField = () => screen.getByLabelText<HTMLInputElement>("Port");
const protocolField = () => screen.getByLabelText("Protocol");
const facilityField = () => screen.getByLabelText("Facility");
const saveButton = () => screen.getByRole("button", { name: /^Save$/ });
const testButton = () =>
  screen.getByRole("button", { name: /Test Connection/ });

/**
 * Types over the port field's whole value the way an operator selecting it
 * would — without clearing it first, so the field is never blank on the way.
 */
async function typePort(user: UserEvent, text: string) {
  const field = portField();
  await user.type(field, text, {
    initialSelectionStart: 0,
    initialSelectionEnd: field.value.length,
  });
}

/**
 * Edits the port field the way a browser reports text a number input cannot
 * convert: a value of "" with validity.badInput set (or cleared, once the text
 * is gone). jsdom never sets badInput itself, so it is stubbed on the element.
 */
function editPortUnreadable(bad: boolean) {
  const field = portField();
  Object.defineProperty(field, "validity", {
    configurable: true,
    value: { badInput: bad },
  });
  fireEvent.input(field, { target: { value: "" } });
}

/**
 * Renders the card and records what the facility select shows after each
 * commit in which this wrapper itself rendered — the card's own re-renders add
 * nothing. A layout effect runs after the commit's DOM update and before that
 * commit's passive effects, the card's load effect among them, so it sees the
 * render between a refetch and the load effect — which no assertion made after
 * act() can see.
 */
function FacilityProbe({ seen }: { seen: (string | undefined)[] }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    seen.push(
      ref.current?.querySelector<HTMLSelectElement>("#syslog-facility")?.value,
    );
  });
  return (
    <div ref={ref}>
      <SyslogConfigCard />
    </div>
  );
}

describe("SyslogConfigCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("port", () => {
    it("suggests the default port of whichever protocol is picked", async () => {
      const user = userEvent.setup();
      await renderCard(user, unsaved);
      expect(portField()).toHaveValue(514);

      const steps = [
        ["tls", 6514],
        ["tcp", 514],
        ["tls", 6514],
        ["udp", 514],
      ] as const;
      for (const [protocol, port] of steps) {
        await user.selectOptions(protocolField(), protocol);
        expect(portField(), `after switching to ${protocol}`).toHaveValue(port);
      }
    });

    it.each([
      { typed: "1514", why: "a custom port" },
      { typed: "514", why: "udp's own default, typed anyway" },
      { typed: "6514", why: "tls's default, typed while on udp" },
    ])(
      "keeps a typed port through protocol switches: $why",
      async ({ typed }) => {
        const user = userEvent.setup();
        await renderCard(user, unsaved);
        await typePort(user, typed);
        expect(portField()).toHaveValue(Number(typed));

        // Passing through tls and out again matters for 6514: a rule that
        // asked "is the port the current protocol's default?" instead of "did
        // the operator pick it?" would drop it on the first switch away from
        // tls.
        for (const protocol of ["tls", "tcp", "udp", "tls"]) {
          await user.selectOptions(protocolField(), protocol);
          expect(portField(), `after switching to ${protocol}`).toHaveValue(
            Number(typed),
          );
        }
      },
    );

    it.each([
      { protocol: "udp", port: 1514, switches: ["tls", "udp"] },
      // What the form used to store for tls, since it kept 514 on the switch:
      // indistinguishable from a deliberate 514, so it is not "corrected".
      { protocol: "tls", port: 514, switches: ["udp", "tls"] },
      { protocol: "udp", port: 6514, switches: ["tls", "udp"] },
    ])(
      "keeps a saved $protocol port of $port through protocol switches",
      async ({ protocol, port, switches }) => {
        const user = userEvent.setup();
        await renderCard(user, { ...unsaved, protocol, port });
        expect(portField()).toHaveValue(port);

        for (const next of switches) {
          await user.selectOptions(protocolField(), next);
          expect(portField(), `after switching to ${next}`).toHaveValue(port);
        }
      },
    );

    it.each([
      { protocol: "udp", port: 514, next: "tls", suggested: 6514 },
      { protocol: "tcp", port: 514, next: "tls", suggested: 6514 },
      { protocol: "tls", port: 6514, next: "udp", suggested: 514 },
      // The API only lower-cases the protocol of an enabled config, so a
      // disabled one can come back as "TLS"; its 6514 is still tls's default.
      { protocol: "TLS", port: 6514, next: "tcp", suggested: 514 },
    ])(
      "lets a saved $protocol default of $port follow a switch to $next",
      async ({ protocol, port, next, suggested }) => {
        const user = userEvent.setup();
        await renderCard(user, { ...unsaved, protocol, port });
        await user.selectOptions(protocolField(), next);
        expect(portField()).toHaveValue(suggested);
      },
    );

    it("re-reads whether the port was chosen when the saved config changes", async () => {
      // Clear a saved custom port and save: the API stores the protocol's
      // default, and the refetch brings that back. It must follow the protocol
      // again from there, or a switch to tls would keep 514 — the bug this
      // rule exists for, by another route.
      const user = userEvent.setup();
      const { save, rerender } = await renderCard(user, {
        ...forwarding,
        port: 1514,
      });
      await user.clear(portField());
      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 0,
        protocol: "udp",
        facility: 16,
        tls_skip_verify: false,
      });

      mockHooks({ ...forwarding, port: 514 });
      rerender(<SyslogConfigCard />);
      expect(portField()).toHaveValue(514);

      await user.selectOptions(protocolField(), "tls");
      expect(portField()).toHaveValue(6514);
    });

    it("leaves a cleared port blank and sends 0, for the API to fill in the protocol's default", async () => {
      const user = userEvent.setup();
      const { save, test } = await renderCard(user, forwarding);

      await user.clear(portField());
      expect(portField()).toHaveValue(null);
      expect(portField()).toHaveAttribute("placeholder", "514");

      // Blank already means "the protocol's default", so a switch leaves it
      // blank, and the placeholder follows to say which default that is.
      await user.selectOptions(protocolField(), "tls");
      expect(portField()).toHaveValue(null);
      expect(portField()).toHaveAttribute("placeholder", "6514");

      const expected: SyslogConfig = {
        enabled: true,
        host: "syslog.example.com",
        port: 0,
        protocol: "tls",
        facility: 16,
        tls_skip_verify: false,
      };
      await user.click(saveButton());
      expect(onlyBody(save)).toEqual(expected);
      // The probe reads the same form, so it asks for the same default.
      await user.click(testButton());
      expect(onlyBody(test)).toEqual(expected);
    });

    it.each([
      { from: "a saved port", clearFirst: false },
      // Here the value goes from "" to "", which React does not report as a
      // change at all.
      { from: "a blank field", clearFirst: true },
    ])(
      "holds Save and Test while the port field holds text that is not a number, starting from $from",
      async ({ clearFirst }) => {
        const user = userEvent.setup();
        const { save } = await renderCard(user, forwarding);
        if (clearFirst) {
          await user.clear(portField());
        }
        expect(saveButton()).toBeEnabled();
        expect(testButton()).toBeEnabled();

        editPortUnreadable(true);
        expect(screen.getByText("Port must be a number")).toBeInTheDocument();
        expect(saveButton()).toBeDisabled();
        expect(testButton()).toBeDisabled();

        // Deleting the text leaves a blank field, which is a port the API can
        // fill in.
        editPortUnreadable(false);
        expect(
          screen.queryByText("Port must be a number"),
        ).not.toBeInTheDocument();
        await user.click(saveButton());
        expect(onlyBody(save)).toEqual({
          enabled: true,
          host: "syslog.example.com",
          port: 0,
          protocol: "udp",
          facility: 16,
          tls_skip_verify: false,
        });
      },
    );

    it("ties the port error to the field and announces it", async () => {
      const user = userEvent.setup();
      await renderCard(user, forwarding);
      expect(portField()).toHaveAttribute("aria-invalid", "false");
      // No message, so no reference to one: an id that names nothing is a
      // broken reference, not an empty description.
      expect(portField()).not.toHaveAttribute("aria-describedby");
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();

      editPortUnreadable(true);

      expect(portField()).toHaveAttribute("aria-invalid", "true");
      expect(portField()).toHaveAccessibleDescription("Port must be a number");
      expect(screen.getByRole("alert").textContent).toEqual(
        "Port must be a number",
      );
    });

    it.each(["-5", "-1", "65536", "70000"])(
      "holds Save and Test on a typed port of %s, outside 1-65535",
      async (typed) => {
        // The API answers each of these with a 400; the card says so before
        // anything is sent.
        const user = userEvent.setup();
        const { save } = await renderCard(user, forwarding);
        await typePort(user, typed);
        expect(portField()).toHaveValue(Number(typed));

        expect(screen.getByRole("alert").textContent).toEqual(
          "Port must be between 1 and 65535",
        );
        expect(portField()).toHaveAttribute("aria-invalid", "true");
        expect(portField()).toHaveAccessibleDescription(
          "Port must be between 1 and 65535",
        );
        expect(saveButton()).toBeDisabled();
        expect(testButton()).toBeDisabled();

        await typePort(user, "1514");
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        await user.click(saveButton());
        expect(onlyBody(save)).toEqual({
          enabled: true,
          host: "syslog.example.com",
          port: 1514,
          protocol: "udp",
          facility: 16,
          tls_skip_verify: false,
        });
      },
    );

    it.each(["1", "65535"])(
      "accepts a typed port of %s, the edge of the range",
      async (typed) => {
        const user = userEvent.setup();
        const { save } = await renderCard(user, forwarding);
        await typePort(user, typed);

        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        expect(portField()).toHaveAttribute("aria-invalid", "false");
        await user.click(saveButton());
        expect(onlyBody(save)).toEqual({
          enabled: true,
          host: "syslog.example.com",
          port: Number(typed),
          protocol: "udp",
          facility: 16,
          tls_skip_verify: false,
        });
      },
    );

    it("drops the port error when the saved config changes underneath", async () => {
      // The saved port replaces the unreadable text, so an error left standing
      // would hold Save over a field that shows a good port.
      const user = userEvent.setup();
      const { rerender } = await renderCard(user, forwarding);
      editPortUnreadable(true);
      expect(saveButton()).toBeDisabled();

      mockHooks({ ...forwarding, port: 1514 });
      rerender(<SyslogConfigCard />);

      expect(portField()).toHaveValue(1514);
      expect(
        screen.queryByText("Port must be a number"),
      ).not.toBeInTheDocument();
      expect(saveButton()).toBeEnabled();
    });

    it("drops the port error when the card is closed and reopened", async () => {
      // Closing unmounts the field and the text in it. It reopens blank —
      // a port the API can fill in — so an error left standing would hold
      // Save and Test over a field with nothing wrong in it.
      const user = userEvent.setup();
      const { save } = await renderCard(user, forwarding);
      editPortUnreadable(true);
      expect(saveButton()).toBeDisabled();

      await user.click(screen.getByRole("button", { name: /Hide/ }));
      await user.click(screen.getByRole("button", { name: /Configure/ }));

      expect(portField()).toHaveValue(null);
      expect(
        screen.queryByText("Port must be a number"),
      ).not.toBeInTheDocument();
      expect(testButton()).toBeEnabled();
      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 0,
        protocol: "udp",
        facility: 16,
        tls_skip_verify: false,
      });
    });

    it("saves the port it suggests", async () => {
      const user = userEvent.setup();
      const { save } = await renderCard(user, forwarding);

      await user.selectOptions(protocolField(), "tls");
      await user.click(saveButton());

      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 6514,
        protocol: "tls",
        facility: 16,
        tls_skip_verify: false,
      });
    });

    it("saves a typed port, not the suggestion, after a protocol switch", async () => {
      const user = userEvent.setup();
      const { save } = await renderCard(user, forwarding);

      await typePort(user, "1514");
      await user.selectOptions(protocolField(), "tls");
      await user.click(saveButton());

      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 1514,
        protocol: "tls",
        facility: 16,
        tls_skip_verify: false,
      });
    });
  });

  describe("facility", () => {
    it("does not offer kern, which the API would store as local0", async () => {
      const user = userEvent.setup();
      await renderCard(user, unsaved);

      const offered = within(facilityField())
        .getAllByRole("option")
        .map((o) => o.getAttribute("value"));
      expect(offered).toEqual([
        "1",
        "2",
        "3",
        "4",
        "5",
        "10",
        "16",
        "17",
        "18",
        "19",
        "20",
        "21",
        "22",
        "23",
      ]);
    });

    it("shows a saved facility the list does not name, and saves it back unchanged", async () => {
      // The API stores any facility from 1 to 23; 9 is one the list leaves out.
      const user = userEvent.setup();
      const { save } = await renderCard(user, { ...forwarding, facility: 9 });

      expect(facilityField()).toHaveValue("9");
      expect(facilityField()).toHaveDisplayValue("other (9)");
      const offered = within(facilityField())
        .getAllByRole("option")
        .map((o) => o.getAttribute("value"));
      expect(offered).toEqual([
        "1",
        "2",
        "3",
        "4",
        "5",
        "9",
        "10",
        "16",
        "17",
        "18",
        "19",
        "20",
        "21",
        "22",
        "23",
      ]);

      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 514,
        protocol: "udp",
        facility: 9,
        tls_skip_verify: false,
      });
    });

    it("shows the form's facility in the render before a refetch reaches the form", () => {
      // The other reason the options follow the form's facility and not only
      // the saved one: for one render after a refetch, the form still holds
      // the old value while the saved config already has the new one.
      const seen: (string | undefined)[] = [];
      mockHooks({ ...forwarding, facility: 9 });
      const { rerender } = renderWithProviders(<FacilityProbe seen={seen} />);
      expect(facilityField()).toHaveValue("9");

      seen.length = 0;
      mockHooks({ ...forwarding, facility: 16 });
      rerender(<FacilityProbe seen={seen} />);

      expect(seen).toEqual(["9"]);
      expect(facilityField()).toHaveValue("16");
    });

    it("keeps offering that saved facility after another is picked, so it can be picked back", async () => {
      const user = userEvent.setup();
      const { save } = await renderCard(user, { ...forwarding, facility: 9 });

      await user.selectOptions(facilityField(), "16");
      expect(facilityField()).toHaveDisplayValue("local0 (16)");
      await user.selectOptions(facilityField(), "9");
      expect(facilityField()).toHaveDisplayValue("other (9)");

      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 514,
        protocol: "udp",
        facility: 9,
        tls_skip_verify: false,
      });
    });

    it("shows a saved facility of 0 as local0 (16), which is what the API stores for it", async () => {
      const user = userEvent.setup();
      const { save } = await renderCard(user, { ...forwarding, facility: 0 });

      expect(facilityField()).toHaveDisplayValue("local0 (16)");
      // Nothing is offered for the 0 itself, which would only name the same
      // facility a second way.
      const offered = within(facilityField())
        .getAllByRole("option")
        .map((o) => o.getAttribute("value"));
      expect(offered).toEqual([
        "1",
        "2",
        "3",
        "4",
        "5",
        "10",
        "16",
        "17",
        "18",
        "19",
        "20",
        "21",
        "22",
        "23",
      ]);

      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: true,
        host: "syslog.example.com",
        port: 514,
        protocol: "udp",
        facility: 16,
        tls_skip_verify: false,
      });
    });
  });

  describe("protocol", () => {
    it("reads a stored protocol case-insensitively: TLS shows as TLS, with its checkbox, and saves as tls", async () => {
      // The API lower-cases the protocol only when it saves an enabled
      // config, so a disabled one can come back as "TLS".
      const user = userEvent.setup();
      const { save } = await renderCard(user, {
        ...unsaved,
        protocol: "TLS",
        port: 6514,
      });

      expect(protocolField()).toHaveValue("tls");
      expect(protocolField()).toHaveDisplayValue("TLS");
      expect(
        screen.getByLabelText("Skip TLS certificate verification"),
      ).toBeInTheDocument();

      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: false,
        host: "",
        port: 6514,
        protocol: "tls",
        facility: 16,
        tls_skip_verify: false,
      });
    });

    it("shows a stored protocol the select does not offer as it is, and saves it back unchanged", async () => {
      // The API checks the protocol only when it saves an enabled config.
      const user = userEvent.setup();
      const { save } = await renderCard(user, { ...unsaved, protocol: "sctp" });

      expect(protocolField()).toHaveValue("sctp");
      expect(protocolField()).toHaveDisplayValue("other (sctp)");
      const offered = within(protocolField())
        .getAllByRole("option")
        .map((o) => o.getAttribute("value"));
      expect(offered).toEqual(["udp", "tcp", "tls", "sctp"]);

      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: false,
        host: "",
        port: 514,
        protocol: "sctp",
        facility: 16,
        tls_skip_verify: false,
      });
    });

    it("keeps offering that protocol after another is picked, so it can be picked back", async () => {
      const user = userEvent.setup();
      const { save } = await renderCard(user, { ...unsaved, protocol: "sctp" });

      await user.selectOptions(protocolField(), "tcp");
      expect(protocolField()).toHaveDisplayValue("TCP");
      await user.selectOptions(protocolField(), "sctp");
      expect(protocolField()).toHaveDisplayValue("other (sctp)");

      await user.click(saveButton());
      expect(onlyBody(save)).toEqual({
        enabled: false,
        host: "",
        port: 514,
        protocol: "sctp",
        facility: 16,
        tls_skip_verify: false,
      });
    });
  });

  describe("what came back", () => {
    const refused = "dial tcp 192.0.2.10:514: connect: connection refused";

    it.each<{
      what: string;
      ended: { save?: Outcome; test?: Outcome };
      shown: string;
    }>([
      { what: "nothing yet", ended: {}, shown: "" },
      // UpdateSyslogConfig's clean reply is the stored config.
      {
        what: "a clean save",
        ended: { save: { reply: { ...forwarding } } },
        shown: "Saved",
      },
      {
        what: "a save the forwarder could not connect for",
        ended: {
          save: {
            reply: {
              saved: true,
              warning: `Config saved but forwarder failed to connect: ${refused}`,
            },
          },
        },
        shown: `Config saved but forwarder failed to connect: ${refused}`,
      },
      {
        what: "a save the API refused",
        ended: {
          save: {
            error: new ApiClientError(400, {
              error: "bad_request",
              message: "Host is required when enabled",
            }),
          },
        },
        shown: "Host is required when enabled",
      },
      // TestSyslog's reply to a probe that got through.
      {
        what: "a test that got through",
        ended: { test: { reply: { success: true } } },
        shown: "Test message sent",
      },
      {
        // TestSyslog's reply to a probe that did not: a 400 whose body is
        // {success: false, error} with no message, built here the way the
        // API client builds it — from the body as it arrived.
        what: "a test that could not connect",
        ended: {
          test: {
            error: new ApiClientError(400, {
              success: false,
              error: `connect to tcp://192.0.2.10:514: ${refused}`,
            } as unknown as ApiError),
          },
        },
        shown: `connect to tcp://192.0.2.10:514: ${refused}`,
      },
      {
        what: "a test the API refused",
        ended: {
          test: {
            error: new ApiClientError(403, {
              error: "forbidden",
              message: "Insufficient permissions",
            }),
          },
        },
        shown: "Insufficient permissions",
      },
    ])("shows exactly what came back after $what", ({ ended, shown }) => {
      mockHooks(forwarding, ended);
      renderWithProviders(<SyslogConfigCard />);

      expect(screen.getByRole("status").textContent).toEqual(shown);
    });
  });

  describe("a config the card could not read", () => {
    it.each([
      {
        what: "a failed read",
        error: new ApiClientError(500, {
          error: "internal",
          message: "Failed to read syslog config",
        }),
      },
      // A Viewer holds view:audit but not the manage:audit this read needs.
      {
        what: "a 403",
        error: new ApiClientError(403, {
          error: "forbidden",
          message: "Insufficient permissions",
        }),
      },
    ])(
      "shows $what as a setting it could not read, with Retry, and no form to save",
      async ({ error }) => {
        const refetch = vi.fn();
        const save = mockUnread({
          isError: true,
          error,
          errorUpdatedAt: 1,
          refetch,
        });
        const user = userEvent.setup();
        renderWithProviders(<SyslogConfigCard />);

        expect(
          screen.getByText("Could not load the syslog forwarding settings."),
        ).toBeInTheDocument();
        expect(screen.getByText(error.message)).toBeInTheDocument();
        // Nothing that reads as "forwarding is off", and nothing to save.
        expect(
          screen.queryByLabelText("Enable syslog forwarding"),
        ).not.toBeInTheDocument();
        expect(
          screen.queryByRole("button", { name: /Configure/ }),
        ).not.toBeInTheDocument();
        expect(
          screen.queryByRole("button", { name: /^Save$/ }),
        ).not.toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: "Retry" }));
        expect(refetch).toHaveBeenCalledTimes(1);
        expect(save).not.toHaveBeenCalled();
      },
    );

    it("says a paused read is paused, with no Retry that could not run, and no form", () => {
      // A retry TanStack paused in a background tab: not loading, not failed,
      // no data. A refetch() cannot restart it, so no Retry is offered.
      mockUnread({ isPaused: true, fetchStatus: "paused" });
      renderWithProviders(<SyslogConfigCard />);

      expect(
        screen.getByText(/^Loading the syslog forwarding settings is paused\./),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: /Retry/ }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByLabelText("Enable syslog forwarding"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: /^Save$/ }),
      ).not.toBeInTheDocument();
    });

    it("shows nothing during the first read", () => {
      mockUnread({ isLoading: true, fetchStatus: "fetching" });
      const { container } = renderWithProviders(<SyslogConfigCard />);

      expect(container).toBeEmptyDOMElement();
    });

    it("keeps a failure on screen while its Retry is in flight", () => {
      const error = new ApiClientError(500, {
        error: "internal",
        message: "Failed to read syslog config",
      });
      mockUnread({ isError: true, error, errorUpdatedAt: 1 });
      const { rerender } = renderWithProviders(<SyslogConfigCard />);

      // Refetching a query with no data sends TanStack back to loading with
      // the error cleared; only errorUpdatedAt remembers the failure.
      mockUnread({
        isLoading: true,
        fetchStatus: "fetching",
        errorUpdatedAt: 1,
      });
      rerender(<SyslogConfigCard />);

      expect(
        screen.getByText("Could not load the syslog forwarding settings."),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Retrying..." }),
      ).toBeDisabled();
    });
  });
});
