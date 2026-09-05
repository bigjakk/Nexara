import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { VirtioWinSourceCard } from "./VirtioWinSourceCard";
import {
  useVirtioWinMirror,
  useUpdateVirtioWinMirror,
} from "../api/virtio-win-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { ApiClientError } from "@/lib/api-client";
import type {
  VirtioWinMirror,
  VirtioWinMirrorRequest,
} from "../types/virtio-win";

vi.mock("../api/virtio-win-queries", () => ({
  useVirtioWinMirror: vi.fn(),
  useUpdateVirtioWinMirror: vi.fn(),
}));
vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: vi.fn(),
}));

const mirror: VirtioWinMirror = {
  base_url: "",
  effective_url: "https://fedorapeople.org/groups/virt/virtio-win",
  upstream_url: "https://fedorapeople.org/groups/virt/virtio-win",
};

type MutateOptions = {
  onSuccess?: (data: VirtioWinMirror) => void;
  onError?: (err: unknown) => void;
};

/** Answers the next mutate() with `err`, the way the gate reaches the card. */
function rejectingMutate(err: unknown) {
  return vi.fn((_body: VirtioWinMirrorRequest, opts?: MutateOptions) => {
    opts?.onError?.(err);
  });
}

function gate(code: string, message: string, details = {}) {
  return new ApiClientError(422, { error: code, message, details });
}

/** The request body of the nth mutate() call, typed rather than indexed. */
function callBody(
  mutate: ReturnType<typeof vi.fn>,
  n: number,
): VirtioWinMirrorRequest {
  const call = mutate.mock.calls[n];
  expect(call).toBeDefined();
  return (call as unknown[])[0] as VirtioWinMirrorRequest;
}

function mockHooks(mutate: ReturnType<typeof vi.fn>) {
  vi.mocked(useVirtioWinMirror).mockReturnValue({
    data: mirror,
    isLoading: false,
  } as unknown as ReturnType<typeof useVirtioWinMirror>);
  vi.mocked(useUpdateVirtioWinMirror).mockReturnValue({
    mutate,
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useUpdateVirtioWinMirror>);
  vi.mocked(usePermissions).mockReturnValue({
    canManage: () => true,
  } as unknown as ReturnType<typeof usePermissions>);
}

/** Types a source URL in and clicks Save, so a gate has something to refuse. */
async function saveUrl(user: ReturnType<typeof userEvent.setup>, url: string) {
  await user.type(screen.getByLabelText("Base URL"), url);
  await user.click(screen.getByRole("button", { name: "Save source" }));
}

describe("VirtioWinSourceCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("sends no acknowledgement on a first save", async () => {
    const user = userEvent.setup();
    const mutate = vi.fn();
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "https://mirror.invalid/virtio-win");

    expect(callBody(mutate, 0)).toEqual({
      base_url: "https://mirror.invalid/virtio-win",
    });
  });

  it("prompts with the gate's own message when the source is plain http", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      gate("insecure_source_confirm_required", "This source uses plain HTTP."),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "http://mirror.invalid/virtio-win");

    expect(
      screen.getByText("This source uses plain HTTP."),
    ).toBeInTheDocument();
  });

  it("names the resolved IP in the private-address prompt", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      gate("private_address_confirm_required", "", { ip: "10.0.0.5" }),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "https://mirror.internal/virtio-win");

    expect(screen.getByText(/\(10\.0\.0\.5\)/)).toBeInTheDocument();
  });

  it("confirming http acknowledges ONLY the scheme", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      gate("insecure_source_confirm_required", "This source uses plain HTTP."),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "http://mirror.invalid/virtio-win");
    await user.click(screen.getByRole("button", { name: "Use it anyway" }));

    // A private address is a separate judgement the operator has not made.
    expect(callBody(mutate, 1)).toEqual({
      base_url: "http://mirror.invalid/virtio-win",
      allow_insecure: true,
    });
  });

  it("confirming a private address carries the http acknowledgement too", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      gate("private_address_confirm_required", "", { ip: "10.0.0.5" }),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "http://mirror.internal/virtio-win");
    await user.click(screen.getByRole("button", { name: "Use it anyway" }));

    // An internal mirror on plain http trips the scheme gate first and the
    // address gate second; one click answers the URL, not one gate.
    expect(callBody(mutate, 1)).toEqual({
      base_url: "http://mirror.internal/virtio-win",
      allow_private_address: true,
      allow_insecure: true,
    });
  });

  it("shows one prompt at a time, replacing the answered one", async () => {
    const user = userEvent.setup();
    const errors = [
      gate("insecure_source_confirm_required", "This source uses plain HTTP."),
      gate("private_address_confirm_required", "", { ip: "10.0.0.5" }),
    ];
    const mutate = vi.fn(
      (_body: VirtioWinMirrorRequest, opts?: MutateOptions) => {
        opts?.onError?.(errors.shift());
      },
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "http://mirror.internal/virtio-win");
    await user.click(screen.getByRole("button", { name: "Use it anyway" }));

    expect(screen.getByText(/\(10\.0\.0\.5\)/)).toBeInTheDocument();
    expect(
      screen.queryByText("This source uses plain HTTP."),
    ).not.toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: "Use it anyway" }),
    ).toHaveLength(1);
  });

  it("clears the prompt when the URL it was raised for is edited", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      gate("insecure_source_confirm_required", "This source uses plain HTTP."),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "http://mirror.invalid/virtio-win");
    expect(
      screen.getByText("This source uses plain HTTP."),
    ).toBeInTheDocument();

    // An acknowledgement is given for a URL, not for a card.
    await user.type(screen.getByLabelText("Base URL"), "/nested");

    expect(
      screen.queryByText("This source uses plain HTTP."),
    ).not.toBeInTheDocument();
  });

  it("leaves a failure that is not a gate to the mutation's own toast", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      new ApiClientError(500, { error: "internal", message: "boom" }),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "https://mirror.invalid/virtio-win");

    expect(
      screen.queryByRole("button", { name: "Use it anyway" }),
    ).not.toBeInTheDocument();
  });

  it("offers no confirmation for a 422 gate it does not know", async () => {
    const user = userEvent.setup();
    const mutate = rejectingMutate(
      gate("some_other_confirm_required", "Something else needs a nod."),
    );
    mockHooks(mutate);
    renderWithProviders(<VirtioWinSourceCard />);

    await saveUrl(user, "https://mirror.invalid/virtio-win");

    // Pins the call site, not the lookup: the card asks
    // confirmRequiredFromError for a specific whitelist rather than accepting
    // any 422 gate, so a code it has no confirmation for raises no prompt.
    expect(
      screen.queryByRole("button", { name: "Use it anyway" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/resolves to a private address/),
    ).not.toBeInTheDocument();
  });
});
