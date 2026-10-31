-- 0004_cleanup_zero_mac_phantoms.sql
--
-- Older ARP collection treated 00:00:00:00:00:00 from /proc/net/arp as a
-- device identity. Those rows were address-only phantom devices named
-- "Unknown 000000". Hide only that exact generated name; hiding preserves the
-- rows for audit and avoids deleting addresses that may be useful evidence.
-- User-named devices and other unknown devices are untouched.
UPDATE devices
SET is_hidden = 1,
    updated_at = CURRENT_TIMESTAMP
WHERE lower(trim(name)) = 'unknown 000000'
  AND is_manual = 0
  AND is_locked = 0;
