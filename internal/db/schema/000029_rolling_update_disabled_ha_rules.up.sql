-- Store HA rules that were temporarily disabled during drain (JSON array of rule IDs).
ALTER TABLE rolling_update_nodes
    ADD COLUMN IF NOT EXISTS disabled_ha_rules JSONB NOT NULL DEFAULT '[]';
