-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds the identity a single sign-on account is bound to at its provider: the ID
-- token's issuer (iss) and subject (sub). Every existing row gets '' for both, which
-- means "not linked yet"; an SSO account links itself at its next sign-in, found by its
-- address as before (handlers.OIDCHandler.accountForIdentity).
--
-- It matches review trigger #5 (a UNIQUE index) and cannot be violated by existing
-- rows: the index is partial, over rows whose subject is set, and after the ADD COLUMN
-- no row has one. The CHECKs hold on every existing row for the same reason.
-- Recovery if interrupted: the migration is one transaction; it either applied whole
-- or not at all, and re-running it is safe (IF NOT EXISTS throughout).
ALTER TABLE users ADD COLUMN IF NOT EXISTS oidc_issuer TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS oidc_subject TEXT NOT NULL DEFAULT '';

-- Both or neither, and only on an SSO account: a local or LDAP account is never bound
-- to a provider identity.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_oidc_identity_check;
ALTER TABLE users ADD CONSTRAINT users_oidc_identity_check CHECK (
    (oidc_issuer = '') = (oidc_subject = '')
    AND (oidc_subject = '' OR auth_source = 'oidc'));

-- One provider identity names at most one account.
CREATE UNIQUE INDEX IF NOT EXISTS users_oidc_identity_key ON users (oidc_issuer, oidc_subject)
    WHERE oidc_subject <> '';
