import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { DatastoreOverview } from "./DatastoreOverview";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// A read that failed is not an answer of "none": the strip shows a dash for
// what it could not find out, and each chart says it could not be loaded.
it("does not report nothing for what could not be read", async () => {
  const user = setupUser();
  vi.mocked(apiClient.list).mockRejectedValue(new Error("unreachable"));
  vi.mocked(apiClient.get).mockRejectedValue(new Error("unreachable"));
  renderWithProviders(
    <DatastoreOverview
      pbsId="p1"
      datastores={[{ store: "datastore01", total: 100, used: 10, avail: 90 }]}
      canManage={false}
    />,
  );

  expect(
    await screen.findByText("Usage could not be loaded."),
  ).toBeInTheDocument();
  expect(screen.queryByText("None")).toBeNull();
  expect(screen.queryByText("No verify job")).toBeNull();

  await user.click(screen.getByRole("button", { name: "Forecast" }));
  expect(
    screen.getByText(/could not be loaded, so there is nothing to project/),
  ).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "I/O" }));
  expect(
    await screen.findByText("I/O could not be loaded from the server."),
  ).toBeInTheDocument();
});
