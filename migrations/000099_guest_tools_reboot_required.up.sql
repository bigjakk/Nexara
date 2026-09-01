-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- 000099_guest_tools_reboot_required.up.sql
-- Records the one guest tools outcome that is neither success nor failure.
--
-- The virtio-win installer returns 3010 (ERROR_SUCCESS_REBOOT_REQUIRED) when it
-- installed cleanly but could not finish replacing a driver Windows still had
-- open — the documented behaviour for an in-use boot or network driver. The
-- update did work; the guest simply needs one more restart to pick it up.
--
-- Without somewhere to put this, the notice was folded into last_error on a row
-- whose stage said 'succeeded', so the UI rendered it as a red error under a
-- success badge and the operator had no signal that anything was outstanding.
--
-- Purely additive — one column with a DEFAULT.

ALTER TABLE guest_tools_state
    ADD COLUMN IF NOT EXISTS reboot_required BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN guest_tools_state.reboot_required IS 'Installer returned 3010: the update is installed but a driver that was in use is only replaced at the guest''s next restart. Not an error, and not fully finished either';
