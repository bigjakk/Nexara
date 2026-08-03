DROP INDEX IF EXISTS idx_task_history_cluster_vmid;

ALTER TABLE task_history DROP COLUMN IF EXISTS vmid;
