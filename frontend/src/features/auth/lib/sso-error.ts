/**
 * The words for each code a refused single sign-on comes back with
 * (`/login?error=<code>`, the closed set in internal/api/handlers/oidc.go).
 * Anything else gets one generic sentence: the query's own text is never
 * shown, so a crafted link cannot put words on the sign-in page.
 */
const SSO_ERRORS: Record<string, string> = {
  could_not_complete: "ssoErrorCouldNotComplete",
  provider_error: "ssoErrorProvider",
  not_this_browser: "ssoErrorNotThisBrowser",
  not_configured: "ssoErrorNotConfigured",
  config_error: "ssoErrorConfig",
  provider_unreachable: "ssoErrorProviderUnreachable",
  email_not_verified: "ssoErrorEmailNotVerified",
  domain_not_allowed: "ssoErrorDomainNotAllowed",
  authentication_failed: "ssoErrorAuthenticationFailed",
  identity_mismatch: "ssoErrorIdentityMismatch",
  provisioning_failed: "ssoErrorProvisioningFailed",
};

/** The auth-namespace key of the sentence for a refusal code. */
export function ssoErrorKey(code: string): string {
  return Object.hasOwn(SSO_ERRORS, code)
    ? (SSO_ERRORS[code] ?? "ssoErrorUnknown")
    : "ssoErrorUnknown";
}
