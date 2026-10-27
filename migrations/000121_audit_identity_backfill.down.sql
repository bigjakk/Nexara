-- No-op: this migration only re-files existing audit rows under the identity
-- rows now use, and the old spellings it replaced (a per-node inventory row id
-- for a storage, a datastore's bare name for a PBS server's action, a node's
-- name or a file's for a storage) were the defect. Each re-filed row keeps its
-- original type, id and action in details.rekeyed_from, so nothing is lost, but
-- restoring them here would split every object's History again.
SELECT 1;
