-- 000104_scheduled_task_last_upid.down.sql
--
-- Back to the vocabulary the previous release reads. It never wrote
-- 'dispatched' and renders every last_status other than NULL and 'success' as
-- Failed, so a row still waiting on its Proxmox task would show a failure that
-- has not happened. It recorded a dispatched run as 'success', which is what
-- such a row becomes here: exactly what that release would have written for it.
UPDATE scheduled_tasks SET last_status = 'success' WHERE last_status = 'dispatched';

ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS last_upid;
