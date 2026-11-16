import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { OIDCCallbackPage } from "./OIDCCallbackPage";

describe("OIDCCallbackPage", () => {
  // Like the login page, it shows a refusal's code in its own words only.
  it("never shows the query's own text", async () => {
    const crafted = "Your account is locked. Call the help desk.";

    renderWithProviders(<OIDCCallbackPage />, {
      router: {
        initialEntries: [`/oidc-callback?error=${encodeURIComponent(crafted)}`],
      },
    });

    expect(
      await screen.findByText(/single sign-on did not complete/),
    ).toBeInTheDocument();
    expect(document.body.textContent).not.toContain(crafted);
  });
});
