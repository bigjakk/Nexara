-- 000010_drs.down.sql
DROP TRIGGER IF EXISTS trg_drs_rules_updated_at ON drs_rules;
DROP TRIGGER IF EXISTS trg_drs_configs_updated_at ON drs_configs;
DROP TABLE IF EXISTS drs_history;
DROP TABLE IF EXISTS drs_rules;
DROP TABLE IF EXISTS drs_configs;
