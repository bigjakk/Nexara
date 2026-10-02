/**
 * The page to go to once someone has signed in: the login URL's returnTo, which
 * ProtectedRoute puts there and anyone can edit. Only an in-app path is followed.
 *
 * A path is a slash and then anything but another slash or a backslash: a
 * browser reads "\" as "/", so "//host" and "/\host" both name another origin,
 * and navigating to either throws — which leaves a signed-in user stuck on the
 * login page. The URL parser also drops a tab or newline wherever it is, so
 * "/<tab>/host" is "//host" too; no control character is accepted.
 */
export function sanitizeReturnTo(value: string | null): string {
  if (value === null || !/^\/(?![/\\])/.test(value)) return "/";
  for (const ch of value) {
    const code = ch.charCodeAt(0);
    if (code < 0x20 || code === 0x7f) return "/";
  }
  return value;
}
