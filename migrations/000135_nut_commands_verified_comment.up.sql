-- 000135_nut_commands_verified_comment.up.sql
-- Corrects nut_servers.commands_verified_at's comment, written by 000134 before
-- two changes to what moves the mark: only a command counts, and a refused
-- command leaves it alone. The new comment says only what the column is; what
-- sets and clears it is in queries/ups_commands.sql and queries/nut.sql. A
-- comment only; no data changes.

COMMENT ON COLUMN nut_servers.commands_verified_at IS 'When upsd last took a command from the stored account: upsd checks an account only when a command runs, so this is the only proof it may run them';
