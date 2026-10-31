-- 0006_repair_primary_addresses.sql
--
-- Migration 0005 removed the ghost addresses the zero-MAC bug wrote. The
-- devices table still caches the primary address in devices.primary_ipv4 and
-- devices.primary_ipv6, and those cached values can now point at an address
-- that no longer exists. The UI would show an address the device does not
-- have.
--
-- Clearing the stale cache is enough: refreshPrimaryAddresses recomputes it
-- from the surviving rows on the next discovery run.

UPDATE devices
SET primary_ipv4 = '', updated_at = CURRENT_TIMESTAMP
WHERE primary_ipv4 != ''
  AND NOT EXISTS (
      SELECT 1 FROM device_addresses a
      WHERE a.device_id = devices.id
        AND a.address = devices.primary_ipv4
        AND a.family = 'ipv4'
  );

UPDATE devices
SET primary_ipv6 = '', updated_at = CURRENT_TIMESTAMP
WHERE primary_ipv6 != ''
  AND NOT EXISTS (
      SELECT 1 FROM device_addresses a
      WHERE a.device_id = devices.id
        AND a.address = devices.primary_ipv6
        AND a.family = 'ipv6'
  );
