DROP TABLE IF EXISTS node_pci_devices;
DROP TABLE IF EXISTS node_network_interfaces;
DROP TABLE IF EXISTS node_disks;

ALTER TABLE nodes
    DROP COLUMN IF EXISTS swap_total,
    DROP COLUMN IF EXISTS swap_used,
    DROP COLUMN IF EXISTS swap_free,
    DROP COLUMN IF EXISTS dns_servers,
    DROP COLUMN IF EXISTS dns_search,
    DROP COLUMN IF EXISTS timezone,
    DROP COLUMN IF EXISTS subscription_status,
    DROP COLUMN IF EXISTS subscription_level,
    DROP COLUMN IF EXISTS load_avg,
    DROP COLUMN IF EXISTS io_wait;
