-- 0005_cleanup_zero_mac_addresses.sql
--
-- The ARP collector used to treat the kernel's all-zero lladdr
-- (00:00:00:00:00:00) as a device identity. Every unresolved neighbour entry
-- then shared one MAC bucket, and the batch wrote all of those addresses onto
-- a single device. The result was one device carrying hundreds of ARP
-- addresses and the name "Unknown 000000".
--
-- The collector no longer produces those entries (see
-- internal/discovery/arp/collector.go), but the rows are still in the
-- database. The evidence table records the raw observation for each address,
-- including the all-zero lladdr, so it is the reliable way to tell a ghost
-- address from a real one: a name is a derived string that changes, the
-- evidence is the kernel's own record.
--
-- An address is a ghost when every ARP observation of it carries the all-zero
-- lladdr. An address that was ever seen with a real lladdr, or that another
-- collector reported (a DHCP lease, an NDP neighbour), is real and stays.
-- The hidden phantom devices themselves are left in place: migration 0004 hid
-- them and they remain there for audit.
--
-- The cached primary address is repaired by migration 0006.

DELETE FROM device_addresses
WHERE source = 'arp'
  AND id IN (
      SELECT a.id
      FROM device_addresses a
      WHERE NOT EXISTS (
          SELECT 1
          FROM discovery_evidence e2
          WHERE e2.subject_type = 'device'
            AND e2.subject_id = a.device_id
            AND e2.collector = 'arp'
            AND e2.evidence_type = 'neighbor'
            AND e2.raw_data LIKE a.address || ' dev %lladdr %'
            AND e2.raw_data NOT LIKE '%lladdr 00:00:00:00:00:00%'
      )
      AND EXISTS (
          SELECT 1
          FROM discovery_evidence e
          WHERE e.subject_type = 'device'
            AND e.subject_id = a.device_id
            AND e.collector = 'arp'
            AND e.evidence_type = 'neighbor'
            AND e.raw_data LIKE a.address || ' dev %lladdr 00:00:00:00:00:00%'
      )
  );
