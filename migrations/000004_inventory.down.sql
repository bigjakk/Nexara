-- 000004_inventory.down.sql
-- Drop in reverse FK order

DROP TRIGGER IF EXISTS trg_storage_pools_updated_at ON storage_pools;
DROP TRIGGER IF EXISTS trg_vms_updated_at ON vms;
DROP TRIGGER IF EXISTS trg_nodes_updated_at ON nodes;

DROP TABLE IF EXISTS storage_pools;
DROP TABLE IF EXISTS vms;
DROP TABLE IF EXISTS nodes;
