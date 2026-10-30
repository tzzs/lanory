-- 0007_reason_codes.sql
--
-- The reason a device is in its state, and the reason an anomaly was raised,
-- used to be an English sentence baked into the database. That made the text
-- untranslatable: the frontend could not render it in the user's language,
-- because the language was never part of the data.
--
-- This migration adds the translatable form alongside the existing text:
--
--   state_reason_code / reason_code  a stable machine code plus its
--                                     parameters, as JSON
--
-- The English text stays where it is. It is what logs, bug reports and API
-- consumers outside the web UI read, and it remains the fallback for any row
-- whose code could not be determined.
--
-- The backfill below recognises the reason strings the state machine and the
-- link observer produce. The parameters are recovered from the text by the
-- fixed markers each template uses, so no regular expression is needed and
-- the extraction cannot drift when a number changes width.
--
-- A row whose message matches no known pattern keeps an empty code and is
-- rendered as the original English text: nothing is lost, and no row is
-- shown with the wrong reason.

ALTER TABLE devices ADD COLUMN state_reason_code TEXT NOT NULL DEFAULT '';

ALTER TABLE network_events ADD COLUMN reason_code TEXT NOT NULL DEFAULT '';

ALTER TABLE probe_targets ADD COLUMN state_reason_code TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- Parameter extraction
-- ---------------------------------------------------------------------------
--
-- The templates share a shape, so the same expressions apply to all of them:
--
--   failures   the number between "unreachable for " and " consecutive"
--   source     the probe source between " from " and " via "
--   protocol   the probe method right after " via "
--   address    the neighbour table entry between "still lists " and ":"
--
-- Each is cut out with instr() on its own marker, so a two digit failure
-- count or a multi digit address is handled as well as a single digit one.

-- ---------------------------------------------------------------------------
-- Backfill: devices
-- ---------------------------------------------------------------------------
--
-- A device's state_reason is the transition reason with the detection method
-- appended ("... (detected by icmp probe from lanory)"), so the patterns end
-- in a wildcard. The patterns are mutually exclusive: each template has a
-- distinct continuation after "via <protocol>", so no message can match two.

-- "unreachable for N consecutive probes from S via P with no recent success"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.unreachable_consecutive',
    'params', json_object(
        'failures', CAST(substr(state_reason, 16, instr(state_reason, ' consecutive') - 16) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'unreachable for % consecutive probes from % via % with no recent success%'
  AND state_reason_code = '';

-- "unreachable for N consecutive probes from S via P, but the probe source itself lost connectivity: this says nothing about the device"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.unreachable_source_down',
    'params', json_object(
        'failures', CAST(substr(state_reason, 16, instr(state_reason, ' consecutive') - 16) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'unreachable for % consecutive probes from % via %, but the probe source itself lost connectivity: this says nothing about the device%'
  AND state_reason_code = '';

-- "unreachable for N consecutive probes from S via P, but the neighbour table still lists T: the device is on the segment and does not answer this probe method"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.unreachable_still_present',
    'params', json_object(
        'failures', CAST(substr(state_reason, 16, instr(state_reason, ' consecutive') - 16) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:'),
        'address', substr(state_reason, instr(state_reason, 'still lists ') + 12, instr(state_reason, ': the device is on the segment') - instr(state_reason, 'still lists ') - 12)
    )
)
WHERE state_reason LIKE 'unreachable for % consecutive probes from % via %, but the neighbour table still lists %: the device is on the segment and does not answer this probe method%'
  AND state_reason_code = '';

-- "unreachable for N consecutive probes from S via P, but the device resumed after an earlier gap of T failures: power saving, not absence"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.unreachable_power_cycling',
    'params', json_object(
        'failures', CAST(substr(state_reason, 16, instr(state_reason, ' consecutive') - 16) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:'),
        'gap', CAST(substr(state_reason, instr(state_reason, 'earlier gap of ') + 15, instr(state_reason, ' failures: power saving') - instr(state_reason, 'earlier gap of ') - 15) AS INTEGER)
    )
)
WHERE state_reason LIKE 'unreachable for % consecutive probes from % via %, but the device resumed after an earlier gap of % failures: power saving, not absence%'
  AND state_reason_code = '';

-- "unreachable for N consecutive probes from S via P; the device was already sleeping and answered A, so this is a deeper sleep rather than absence"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.unreachable_deeper_sleep',
    'params', json_object(
        'failures', CAST(substr(state_reason, 16, instr(state_reason, ' consecutive') - 16) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:'),
        'last_success', substr(state_reason, instr(state_reason, 'answered ') + 9, instr(state_reason, ', so this is a deeper sleep') - instr(state_reason, 'answered ') - 9)
    )
)
WHERE state_reason LIKE 'unreachable for % consecutive probes from % via %; the device was already sleeping and answered %, so this is a deeper sleep rather than absence%'
  AND state_reason_code = '';

-- "intermittent reachability from S via P: X of the last Y probes failed"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.intermittent',
    'params', json_object(
        'failed', CAST(substr(state_reason, instr(state_reason, ': ') + 2, instr(substr(state_reason, instr(state_reason, ': ') + 2), ' of') - 1) AS INTEGER),
        'total', CAST(substr(state_reason, instr(state_reason, ' of the last ') + 13, instr(substr(state_reason, instr(state_reason, ' of the last ') + 13), ' ') - 1) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'intermittent reachability from % via %: % of the last % probes failed%'
  AND state_reason_code = '';

-- "confirming recovery from S via P: X of Y required successes"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.confirming_recovery',
    'params', json_object(
        'successes', CAST(substr(state_reason, instr(state_reason, ': ') + 2, instr(substr(state_reason, instr(state_reason, ': ') + 2), ' of') - 1) AS INTEGER),
        'required', CAST(substr(state_reason, instr(state_reason, ' of ') + 4, instr(substr(state_reason, instr(state_reason, ' of ') + 4), ' ') - 1) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'confirming recovery from % via %: % of % required successes%'
  AND state_reason_code = '';

-- "N consecutive failure(s) below the threshold of T from S via P: not enough evidence to change state"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.below_threshold',
    'params', json_object(
        'failures', CAST(substr(state_reason, 1, instr(state_reason, ' ') - 1) AS INTEGER),
        'threshold', CAST(substr(state_reason, instr(state_reason, 'threshold of ') + 13, instr(substr(state_reason, instr(state_reason, 'threshold of ') + 13), ' from ') - 1) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE '% consecutive failure(s) below the threshold of % from % via %: not enough evidence to change state%'
  AND state_reason_code = '';

-- "no successful probe yet from S via P (N failure(s), threshold T)"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.never_succeeded',
    'params', json_object(
        'failures', CAST(substr(state_reason, instr(state_reason, '(') + 1, instr(substr(state_reason, instr(state_reason, '(') + 1), ' ') - 1) AS INTEGER),
        'threshold', CAST(substr(state_reason, instr(state_reason, 'threshold ') + 10, instr(substr(state_reason, instr(state_reason, 'threshold ') + 10), ')') - 1) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'no successful probe yet from % via % (% failure(s), threshold %)%'
  AND state_reason_code = '';

-- "reachable from S via P"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.reachable',
    'params', json_object(
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'reachable from % via %%'
  AND state_reason_code = '';

-- "never probed from S: no evidence either way"
UPDATE devices SET state_reason_code = json_object(
    'code', 'probe.never_probed',
    'params', json_object('source', substr(state_reason, instr(state_reason, 'never probed from ') + 18, instr(state_reason, ': no evidence either way') - instr(state_reason, 'never probed from ') - 18))
)
WHERE state_reason LIKE 'never probed from %: no evidence either way%'
  AND state_reason_code = '';

-- The reasons that discovery itself records.
UPDATE devices SET state_reason_code = json_object('code', 'device.awaiting_first_check', 'params', json_object())
WHERE state_reason = 'waiting for first reachability check'
  AND state_reason_code = '';

UPDATE devices SET state_reason_code = json_object('code', 'device.not_yet_probed', 'params', json_object())
WHERE state_reason = 'not yet probed'
  AND state_reason_code = '';

-- ---------------------------------------------------------------------------
-- Backfill: probe targets
-- ---------------------------------------------------------------------------
--
-- A probe row stores the transition reason without the detection suffix, so
-- the same templates apply without the trailing wildcard. The probe's device
-- carries the same verdict, so the device's code is copied when there is
-- one; otherwise the templates are matched directly.
--
-- The probe's own text is matched first, because it is the more specific
-- source: a probe that has never run says "awaiting first probe" while its
-- device says "waiting for first reachability check", and the probe's wording
-- is the one that describes the probe.

UPDATE probe_targets SET state_reason_code = json_object(
    'code', 'probe.reachable',
    'params', json_object(
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'reachable from % via %'
  AND state_reason_code = '';

UPDATE probe_targets SET state_reason_code = json_object(
    'code', 'probe.unreachable_consecutive',
    'params', json_object(
        'failures', CAST(substr(state_reason, 16, instr(state_reason, ' consecutive') - 16) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'unreachable for % consecutive probes from % via % with no recent success%'
  AND state_reason_code = '';

UPDATE probe_targets SET state_reason_code = json_object(
    'code', 'probe.intermittent',
    'params', json_object(
        'failed', CAST(substr(state_reason, instr(state_reason, ': ') + 2, instr(substr(state_reason, instr(state_reason, ': ') + 2), ' of') - 1) AS INTEGER),
        'total', CAST(substr(state_reason, instr(state_reason, ' of the last ') + 13, instr(substr(state_reason, instr(state_reason, ' of the last ') + 13), ' ') - 1) AS INTEGER),
        'source', substr(state_reason, instr(state_reason, ' from ') + 6, instr(state_reason, ' via ') - instr(state_reason, ' from ') - 6),
        'protocol', rtrim(substr(state_reason, instr(state_reason, ' via ') + 5, CASE WHEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') > 0 THEN instr(substr(state_reason, instr(state_reason, ' via ') + 5), ' ') - 1 ELSE length(state_reason) - instr(state_reason, ' via ') - 4 END), ',;:')
    )
)
WHERE state_reason LIKE 'intermittent reachability from % via %: % of the last % probes failed%'
  AND state_reason_code = '';

UPDATE probe_targets SET state_reason_code = json_object('code', 'probe.awaiting_first', 'params', json_object())
WHERE state_reason = 'awaiting first probe'
  AND state_reason_code = '';

UPDATE probe_targets SET state_reason_code = json_object('code', 'probe.awaiting_first', 'params', json_object())
WHERE state_reason = 'waiting for first probe'
  AND state_reason_code = '';

-- The probe's own text did not match any template, so the device's code is
-- the fallback: both describe the same verdict, and the device's wording is
-- the only one left.
UPDATE probe_targets SET state_reason_code = (
    SELECT d.state_reason_code FROM devices d
    WHERE d.id = probe_targets.device_id AND d.state_reason_code != ''
)
WHERE state_reason_code = ''
  AND device_id IN (SELECT id FROM devices WHERE state_reason_code != '');

-- ---------------------------------------------------------------------------
-- Backfill: network events
-- ---------------------------------------------------------------------------
--
-- An event message carries the same templates plus the suffix naming the
-- detection method. The suffix is appended by the monitoring scheduler, so
-- the templates are matched with it included.

-- "unreachable for N consecutive probes from S via P with no recent success"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.unreachable_consecutive',
    'params', json_object(
        'failures', CAST(substr(message, 16, instr(message, ' consecutive') - 16) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE 'unreachable for % consecutive probes from % via % with no recent success (detected by % probe from %)'
  AND reason_code = '';

-- "unreachable for N consecutive probes from S via P, but the probe source itself lost connectivity: this says nothing about the device"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.unreachable_source_down',
    'params', json_object(
        'failures', CAST(substr(message, 16, instr(message, ' consecutive') - 16) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE 'unreachable for % consecutive probes from % via %, but the probe source itself lost connectivity: this says nothing about the device (detected by % probe from %)'
  AND reason_code = '';

-- "unreachable for N consecutive probes from S via P, but the neighbour table still lists T: the device is on the segment and does not answer this probe method"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.unreachable_still_present',
    'params', json_object(
        'failures', CAST(substr(message, 16, instr(message, ' consecutive') - 16) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:'),
        'address', substr(message, instr(message, 'still lists ') + 12, instr(message, ': the device is on the segment') - instr(message, 'still lists ') - 12)
    )
)
WHERE message LIKE 'unreachable for % consecutive probes from % via %, but the neighbour table still lists %: the device is on the segment and does not answer this probe method (detected by % probe from %)'
  AND reason_code = '';

-- "unreachable for N consecutive probes from S via P, but the device resumed after an earlier gap of T failures: power saving, not absence"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.unreachable_power_cycling',
    'params', json_object(
        'failures', CAST(substr(message, 16, instr(message, ' consecutive') - 16) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:'),
        'gap', CAST(substr(message, instr(message, 'earlier gap of ') + 15, instr(message, ' failures: power saving') - instr(message, 'earlier gap of ') - 15) AS INTEGER)
    )
)
WHERE message LIKE 'unreachable for % consecutive probes from % via %, but the device resumed after an earlier gap of % failures: power saving, not absence (detected by % probe from %)'
  AND reason_code = '';

-- "unreachable for N consecutive probes from S via P; the device was already sleeping and answered A, so this is a deeper sleep rather than absence"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.unreachable_deeper_sleep',
    'params', json_object(
        'failures', CAST(substr(message, 16, instr(message, ' consecutive') - 16) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:'),
        'last_success', substr(message, instr(message, 'answered ') + 9, instr(message, ', so this is a deeper sleep') - instr(message, 'answered ') - 9)
    )
)
WHERE message LIKE 'unreachable for % consecutive probes from % via %; the device was already sleeping and answered %, so this is a deeper sleep rather than absence (detected by % probe from %)'
  AND reason_code = '';

-- "intermittent reachability from S via P: X of the last Y probes failed"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.intermittent',
    'params', json_object(
        'failed', CAST(substr(message, instr(message, ': ') + 2, instr(substr(message, instr(message, ': ') + 2), ' of') - 1) AS INTEGER),
        'total', CAST(substr(message, instr(message, ' of the last ') + 13, instr(substr(message, instr(message, ' of the last ') + 13), ' ') - 1) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE 'intermittent reachability from % via %: % of the last % probes failed (detected by % probe from %)'
  AND reason_code = '';

-- "confirming recovery from S via P: X of Y required successes"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.confirming_recovery',
    'params', json_object(
        'successes', CAST(substr(message, instr(message, ': ') + 2, instr(substr(message, instr(message, ': ') + 2), ' of') - 1) AS INTEGER),
        'required', CAST(substr(message, instr(message, ' of ') + 4, instr(substr(message, instr(message, ' of ') + 4), ' ') - 1) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE 'confirming recovery from % via %: % of % required successes (detected by % probe from %)'
  AND reason_code = '';

-- "N consecutive failure(s) below the threshold of T from S via P: not enough evidence to change state"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.below_threshold',
    'params', json_object(
        'failures', CAST(substr(message, 1, instr(message, ' ') - 1) AS INTEGER),
        'threshold', CAST(substr(message, instr(message, 'threshold of ') + 13, instr(substr(message, instr(message, 'threshold of ') + 13), ' from ') - 1) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE '% consecutive failure(s) below the threshold of % from % via %: not enough evidence to change state (detected by % probe from %)'
  AND reason_code = '';

-- "no successful probe yet from S via P (N failure(s), threshold T)"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.never_succeeded',
    'params', json_object(
        'failures', CAST(substr(message, instr(message, '(') + 1, instr(substr(message, instr(message, '(') + 1), ' ') - 1) AS INTEGER),
        'threshold', CAST(substr(message, instr(message, 'threshold ') + 10, instr(substr(message, instr(message, 'threshold ') + 10), ')') - 1) AS INTEGER),
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE 'no successful probe yet from % via % (% failure(s), threshold %) (detected by % probe from %)'
  AND reason_code = '';

-- "reachable from S via P"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.reachable',
    'params', json_object(
        'source', substr(message, instr(message, ' from ') + 6, instr(message, ' via ') - instr(message, ' from ') - 6),
        'protocol', rtrim(substr(message, instr(message, ' via ') + 5, CASE WHEN instr(substr(message, instr(message, ' via ') + 5), ' ') > 0 THEN instr(substr(message, instr(message, ' via ') + 5), ' ') - 1 ELSE length(message) - instr(message, ' via ') - 4 END), ',;:')
    )
)
WHERE message LIKE 'reachable from % via % (detected by % probe from %)'
  AND reason_code = '';

-- "never probed from S: no evidence either way"
UPDATE network_events SET reason_code = json_object(
    'code', 'probe.never_probed',
    'params', json_object('source', substr(message, instr(message, 'never probed from ') + 18, instr(message, ': no evidence either way') - instr(message, 'never probed from ') - 18))
)
WHERE message LIKE 'never probed from %: no evidence either way%'
  AND reason_code = '';

-- "eth2 carrier up -> down" carries the interface name in front of the marker
-- and the two states around the arrow. The parameters match what the running
-- code emits for a new link event, so a backfilled row and a fresh row render
-- the same way.
UPDATE network_events SET reason_code = json_object(
    'code', 'link.carrier_changed',
    'params', json_object(
        'interface', substr(message, 1, instr(message, ' carrier ') - 1),
        'old', trim(substr(message, instr(message, ' carrier ') + 9, instr(message, ' -> ') - instr(message, ' carrier ') - 9)),
        'new', trim(substr(message, instr(message, ' -> ') + 4, CASE WHEN instr(substr(message, instr(message, ' -> ') + 4), ' (') > 0 THEN instr(substr(message, instr(message, ' -> ') + 4), ' (') - 1 ELSE length(message) - instr(message, ' -> ') - 3 END))
    )
)
WHERE (message LIKE '% carrier up -> down' OR message LIKE '% carrier down -> up')
  AND reason_code = '';

-- "br-lan negotiated speed 1000 Mbps -> 10 Mbps" likewise: the interface name
-- precedes the marker and the two speeds sit around the arrow.
UPDATE network_events SET reason_code = json_object(
    'code', 'link.speed_changed',
    'params', json_object(
        'interface', substr(message, 1, instr(message, ' negotiated speed ') - 1),
        'old_mbps', CAST(substr(message, instr(message, ' negotiated speed ') + 18, instr(message, ' Mbps -> ') - instr(message, ' negotiated speed ') - 18) AS INTEGER),
        'new_mbps', CAST(substr(message, instr(message, ' Mbps -> ') + 9, instr(substr(message, instr(message, ' Mbps -> ') + 9), ' Mbps') - 1) AS INTEGER)
    )
)
WHERE message LIKE '% negotiated speed % -> % Mbps'
  AND reason_code = '';
