DROP INDEX IF EXISTS users_oidc_identity_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_oidc_identity_check;
ALTER TABLE users DROP COLUMN IF EXISTS oidc_subject;
ALTER TABLE users DROP COLUMN IF EXISTS oidc_issuer;
