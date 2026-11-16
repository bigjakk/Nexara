-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds the directory entry an LDAP account is bound to: the configuration it was bound
-- under (ldap_config_id) and the entry's stable identifier in one canonical text form
-- (ldap_entry_id: "entryUUID:<uuid>" on OpenLDAP and 389, "objectGUID:<guid>" on
-- Active Directory). Every existing row gets NULL and '', which means "not bound yet";
-- an LDAP account binds itself at its next sign-in or sync, found by its address as
-- before (handlers.ldapAccountForEntry).
--
-- It matches review triggers #5 (a UNIQUE index) and #6 (a foreign key with ON DELETE)
-- and violates neither on existing rows: the index is partial, over rows bound to a
-- configuration, and after the ADD COLUMN no row is; every ldap_config_id is NULL, so
-- the foreign key and the CHECK hold on every existing row. Deleting a configuration
-- sets its accounts' ldap_config_id to NULL: they read as not bound, and bind again
-- under the configuration they next sign in or sync through.
-- Recovery if interrupted: the migration is one transaction; it either applied whole
-- or not at all, and re-running it is safe (IF NOT EXISTS throughout).
ALTER TABLE users ADD COLUMN IF NOT EXISTS ldap_config_id UUID REFERENCES ldap_configs (id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS ldap_entry_id TEXT NOT NULL DEFAULT '';

-- Bound only with an identifier, and only on an LDAP account.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_ldap_entry_check;
ALTER TABLE users ADD CONSTRAINT users_ldap_entry_check CHECK (
    ldap_config_id IS NULL OR (ldap_entry_id <> '' AND auth_source = 'ldap'));

-- One directory entry names at most one account per configuration.
CREATE UNIQUE INDEX IF NOT EXISTS users_ldap_entry_key ON users (ldap_config_id, ldap_entry_id)
    WHERE ldap_config_id IS NOT NULL;
