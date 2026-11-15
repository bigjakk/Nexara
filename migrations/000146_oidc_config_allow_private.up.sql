-- allow_private_address records that an operator confirmed an OIDC
-- configuration whose issuer resolves to a private network address: RFC 1918,
-- IPv6 unique-local or carrier-grade NAT, as a provider on the LAN does. Every
-- contact with the provider (discovery, signing keys, token) checks it again
-- against what the name resolves to then. It relaxes only those ranges:
-- loopback, link-local (the cloud metadata addresses included), unspecified and
-- zoned addresses stay refused for every configuration. Existing configurations
-- were never confirmed and keep refusing private issuers, as they did before
-- this column.
ALTER TABLE oidc_configs
    ADD COLUMN IF NOT EXISTS allow_private_address BOOLEAN NOT NULL DEFAULT false;
