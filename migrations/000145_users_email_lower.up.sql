-- Serves the case-insensitive half of the account lookup by address (GetUserByEmail,
-- GetUserByEmailAndSource, EmailTakenIgnoringCase). Not unique: an existing install may
-- hold two accounts whose addresses differ only by case, and both are kept.
CREATE INDEX IF NOT EXISTS idx_users_email_lower ON users (lower(email));
