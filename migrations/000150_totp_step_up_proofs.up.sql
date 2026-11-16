-- A new table and nothing else: safe on an existing install, with nothing to backfill.
--
-- totp_step_up_proofs holds the proof that the person at a browser has just signed in at
-- the identity provider again, as the account of the session that asked: what a single
-- sign-on account shows before it may begin setting up two-factor authentication. One
-- row per session and purpose (today only 'totp_setup'), written by the SSO callback of a
-- step-up, valid until expires_at by the database's clock, and deleted by the setup that
-- uses it. A row goes with its session or its user (ON DELETE CASCADE); an unused one is
-- simply never valid again once expires_at passes. binding is the record of the secret
-- the callback put in the browser's cookie ("1:" or "0:" for a Secure cookie or not, then
-- the secret's SHA-256): the setup must come from the browser that made the round trip,
-- not only from its session, whose token can be stolen.
CREATE TABLE IF NOT EXISTS totp_step_up_proofs (
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     TEXT NOT NULL,
    binding     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, purpose)
);
