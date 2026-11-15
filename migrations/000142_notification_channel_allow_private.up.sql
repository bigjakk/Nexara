-- allow_private_address records that an operator confirmed a channel whose
-- target (a webhook URL, a chat webhook, an SMTP host) resolves to a private
-- network address: RFC 1918 or IPv6 unique-local. Every send checks it again
-- against what the name resolves to then. It relaxes only those ranges:
-- loopback, link-local and unspecified addresses stay refused for every
-- channel. Existing channels were never confirmed and keep refusing private
-- addresses, as they did before this column.
ALTER TABLE notification_channels
    ADD COLUMN IF NOT EXISTS allow_private_address BOOLEAN NOT NULL DEFAULT false;
