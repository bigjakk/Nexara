DROP TRIGGER IF EXISTS trg_vms_node_delete ON vms;
DROP TRIGGER IF EXISTS trg_vms_node_change ON vms;
DROP TRIGGER IF EXISTS trg_vms_node_insert ON vms;
DROP FUNCTION IF EXISTS keep_guest_node_last_seen();
DROP FUNCTION IF EXISTS record_guest_node_change();
DROP TABLE IF EXISTS guest_node_changes;
DROP TABLE IF EXISTS guest_node_state;
