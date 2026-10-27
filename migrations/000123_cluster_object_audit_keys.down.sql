-- No-op: this migration only re-files existing audit rows under the key rows
-- now use, and the old spellings it replaced (a CIDR for a subnet id, "MyNet"
-- for "mynet", "/vms//100/" for "/vms/100", "network" for a firewall rule) were
-- the defect. There is no meaningful inverse: a row's old shape is not
-- recorded, and restoring it would split every object's History again.
SELECT 1;
