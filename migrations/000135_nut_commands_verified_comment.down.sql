-- 000135_nut_commands_verified_comment.down.sql
-- Puts back the comment 000134 wrote.

COMMENT ON COLUMN nut_servers.commands_verified_at IS 'When upsd last took a command or variable set from the stored account. upsd checks an account only when a command runs, so this is the only proof it may run them; cleared when upsd refuses it, and when the address, username or password changes';
