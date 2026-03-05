-- 000009_scheduled_tasks.down.sql
DROP TRIGGER IF EXISTS trg_scheduled_tasks_updated_at ON scheduled_tasks;
DROP TABLE IF EXISTS scheduled_tasks;
