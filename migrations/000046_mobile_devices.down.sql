-- 000046_mobile_devices.down.sql
DROP INDEX IF EXISTS idx_mobile_devices_device;
DROP INDEX IF EXISTS idx_mobile_devices_user;
DROP TABLE IF EXISTS mobile_devices;
