import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";

import { apiClient } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import type {
  NodeDNSResponse,
  NodeTimeResponse,
} from "../../api/cluster-queries";
import { EditDNSDialog, EditTimezoneDialog } from "./NodeSettingsDialogs";

/**
 * The hold on the Edit button, as read() itself keeps it.
 *
 * The button is held with the `disabled` attribute, which browsers and React
 * both honour: a click on it never reaches the handler, so no test that clicks
 * the real Button can tell read()'s own `if (reading || saving) return` from
 * the attribute. This file's Button is held the other way, with aria-disabled,
 * which takes clicks, as a button would be if the attribute were ever swapped
 * for it. A press that gets through anyway must still start nothing.
 */

// A Button whose hold is aria-disabled, so that a click on it gets through.
vi.mock("@/components/ui/button", async () => {
  const actual = await vi.importActual<typeof import("@/components/ui/button")>(
    "@/components/ui/button",
  );
  const { createElement, forwardRef } = await import("react");
  type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
    variant?: unknown;
    size?: unknown;
    asChild?: unknown;
  };
  // What the real Button takes for itself, and what is held by attribute.
  const NOT_FOR_THE_DOM = new Set(["variant", "size", "asChild", "disabled"]);
  const Button = forwardRef<HTMLButtonElement, Props>(
    function Button(props, ref) {
      const dom = Object.fromEntries(
        Object.entries(props).filter(([name]) => !NOT_FOR_THE_DOM.has(name)),
      ) as ButtonHTMLAttributes<HTMLButtonElement>;
      return createElement("button", {
        ref,
        type: "button",
        "aria-disabled": props.disabled === true ? true : undefined,
        ...dom,
      });
    },
  );
  return { ...actual, Button };
});

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      get: vi.fn(),
      list: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);

const CLUSTER = "cccccccc-0000-0000-0000-000000000009";
const NODE = "pve-01";

interface Kind {
  ui: ReactNode;
  url: string;
  button: string;
  dialog: string;
  field: string;
  stored: NodeDNSResponse | NodeTimeResponse;
  typed: string;
}

const KINDS: [name: string, kind: Kind][] = [
  [
    "DNS",
    {
      ui: <EditDNSDialog clusterId={CLUSTER} nodeName={NODE} />,
      url: `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/dns`,
      button: "Edit DNS settings",
      dialog: `Edit DNS Configuration - ${NODE}`,
      field: "DNS Server 1",
      stored: {
        search: "corp.example.com",
        dns1: "192.0.2.53",
        dns2: "192.0.2.54",
        dns3: "192.0.2.55",
      },
      typed: "192.0.2.77",
    },
  ],
  [
    "timezone",
    {
      ui: <EditTimezoneDialog clusterId={CLUSTER} nodeName={NODE} />,
      url: `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/time`,
      button: "Edit timezone",
      dialog: `Edit Timezone - ${NODE}`,
      field: "Timezone",
      stored: {
        timezone: "Europe/London",
        time: 1_767_225_600,
        localtime: 1_767_225_600,
      },
      typed: "Etc/UTC",
    },
  ],
];

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

async function flush(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/** How many times `url` was read. */
function reads(url: string): number {
  return mockedGet.mock.calls.filter(([path]) => path === url).length;
}

beforeEach(() => {
  mockedGet.mockReset();
  mockedPut.mockReset();
  mockedPut.mockResolvedValue({ status: "ok" });
});

describe.each(KINDS)(
  "the %s Edit button, held with aria-disabled",
  (_, kind) => {
    function renderKind() {
      mockedGet.mockImplementation((path: string) =>
        path === kind.url
          ? Promise.resolve(kind.stored)
          : Promise.reject(new Error(`unexpected GET ${path}`)),
      );
      render(
        <QueryClientProvider client={createAppQueryClient()}>
          {kind.ui}
        </QueryClientProvider>,
      );
    }

    it("starts no second read for a press while its read is out", async () => {
      const user = userEvent.setup();
      renderKind();
      const edit = await screen.findByRole("button", { name: kind.button });
      const held = deferred<unknown>();
      mockedGet.mockReturnValueOnce(held.promise);
      await user.click(edit);

      // Held, and still clickable: this is a press that reaches the handler.
      expect(edit).toHaveAttribute("aria-disabled", "true");
      expect(edit).toBeEnabled();
      expect(reads(kind.url)).toBe(2);
      await user.click(edit);
      await flush();

      expect(reads(kind.url)).toBe(2);
      held.resolve(kind.stored);
      expect(
        await screen.findByRole("dialog", { name: kind.dialog }),
      ).toBeInTheDocument();
      expect(reads(kind.url)).toBe(2);
    });

    it("starts no read for a press while a save of the setting is in flight, even with its dialog gone", async () => {
      const user = userEvent.setup();
      renderKind();
      await user.click(
        await screen.findByRole("button", { name: kind.button }),
      );
      const dialog = await screen.findByRole("dialog", { name: kind.dialog });
      const input = within(dialog).getByLabelText(kind.field);
      await user.clear(input);
      await user.type(input, kind.typed);
      const heldPut = deferred<unknown>();
      mockedPut.mockReturnValueOnce(heldPut.promise);
      await user.click(within(dialog).getByRole("button", { name: "Save" }));
      await waitFor(() => {
        expect(mockedPut).toHaveBeenCalledTimes(1);
      });
      await user.keyboard("{Escape}");
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).toBeNull();
      });

      const edit = await screen.findByRole("button", { name: kind.button });
      expect(edit).toHaveAttribute("aria-disabled", "true");
      expect(edit).toBeEnabled();
      const before = reads(kind.url);
      await user.click(edit);
      await flush();

      expect(reads(kind.url)).toBe(before);
      expect(screen.queryByRole("dialog")).toBeNull();
      // Control: once the write has landed, the same press reads, and opens.
      heldPut.resolve({ status: "ok" });
      await waitFor(() => {
        expect(edit).not.toHaveAttribute("aria-disabled");
      });
      await user.click(edit);
      expect(
        await screen.findByRole("dialog", { name: kind.dialog }),
      ).toBeInTheDocument();
      expect(reads(kind.url)).toBeGreaterThan(before);
    });
  },
);
