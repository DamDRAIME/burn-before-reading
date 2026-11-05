-- ============================================================
-- Seed data for local development
-- Run top to bottom on a freshly migrated database.
-- ============================================================

-- -- Reset (optional) -----------------------------------------
-- Wipes all data and restarts messages.sequence_number at 1.

TRUNCATE messages, printers RESTART IDENTITY CASCADE;

-- -- Printers -------------------------------------------------
-- Fixed IDs so messages below (and manual API calls) can reference them.
--   kitchen: no quiet hours, so claims always work (use it to test the printer flow)
--   office:  overnight quiet hours (22:00-07:00, crosses midnight)
--   garage:  same-day quiet hours (12:00-13:00), silent for two weeks and reporting an error

INSERT INTO printers (id, model, recipient_name, quiet_start, quiet_end, timezone, error, created_at, last_heartbeat_at) VALUES
    ('11111111-1111-1111-1111-111111111111', 'Epson TM-T20III', 'Alice', NULL,    NULL,    'Europe/Brussels', NULL,        NOW() - INTERVAL '30 days', NOW() - INTERVAL '10 seconds'),
    ('22222222-2222-2222-2222-222222222222', 'Epson TM-T20III', 'Bob',   '22:00', '07:00', 'Europe/Brussels', NULL,        NOW() - INTERVAL '20 days', NOW() - INTERVAL '8 hours'),
    ('33333333-3333-3333-3333-333333333333', 'Star TSP143',     'Carol', '12:00', '13:00', 'Europe/Brussels', 'paper jam', NOW() - INTERVAL '60 days', NOW() - INTERVAL '14 days');

-- -- Messages -------------------------------------------------
-- Timestamps follow the status lifecycle:
--   awaiting_approval -> pending (moderated_at) -> claimed (claimed_at) -> processing (processed_at) -> printed (printed_at)
--   awaiting_approval -> rejected (moderated_at)
--   claimed/processing -> pending (retry, attempts < 3) or failed (attempts >= 3), with last_error set
--
-- Note: the claimed and processing rows below become stale once they are older than claimTimeout
-- (2 minutes), so the API's claim reaper will release them shortly after it starts.

-- awaiting_approval: not moderated yet
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, printer_id) VALUES
    ('Dave',  '203.0.113.10', 'web', 'Happy birthday Alice!',          'awaiting_approval', NOW() - INTERVAL '5 minutes', '11111111-1111-1111-1111-111111111111'),
    ('Eve',   '203.0.113.11', 'web', 'Can you pick up milk tonight?',  'awaiting_approval', NOW() - INTERVAL '3 minutes', '22222222-2222-2222-2222-222222222222');

-- pending: approved and waiting in the printer queue (oldest first)
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, printer_id) VALUES
    ('Frank', '203.0.113.12', 'web',    'Good morning!',                   'pending', NOW() - INTERVAL '40 minutes', NOW() - INTERVAL '38 minutes', '11111111-1111-1111-1111-111111111111'),
    ('Grace', '203.0.113.13', 'web',    'Dinner at 7?',                    'pending', NOW() - INTERVAL '20 minutes', NOW() - INTERVAL '19 minutes', '11111111-1111-1111-1111-111111111111'),
    ('Admin', '127.0.0.1',    'admin',  'Maintenance tonight at 22:00',    'pending', NOW() - INTERVAL '15 minutes', NOW() - INTERVAL '15 minutes', '22222222-2222-2222-2222-222222222222');

-- pending (retry): a previous print attempt failed but attempts < 3
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, attempts, last_error, printer_id) VALUES
    ('Heidi', '203.0.113.14', 'direct', 'Retry me please',                 'pending', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 hour', NOW() - INTERVAL '1 hour', 1, 'printer offline', '11111111-1111-1111-1111-111111111111');

-- claimed: picked up by the printer, not processing yet
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, claimed_at, attempts, printer_id) VALUES
    ('Ivan',  '203.0.113.15', 'web', 'See you soon',                       'claimed', NOW() - INTERVAL '50 minutes', NOW() - INTERVAL '49 minutes', NOW() - INTERVAL '1 minute', 1, '11111111-1111-1111-1111-111111111111');

-- processing: currently being printed
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, attempts, printer_id) VALUES
    ('Judy',  '203.0.113.16', 'web', 'Thinking of you',                    'processing', NOW() - INTERVAL '1 hour', NOW() - INTERVAL '59 minutes', NOW() - INTERVAL '30 seconds', NOW() - INTERVAL '25 seconds', 1, '11111111-1111-1111-1111-111111111111');

-- printed: done
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, printed_at, attempts, printer_id) VALUES
    ('Mallory', '203.0.113.17', 'web',    'Welcome home!',                 'printed', NOW() - INTERVAL '1 day',    NOW() - INTERVAL '1 day',    NOW() - INTERVAL '23 hours', NOW() - INTERVAL '23 hours', NOW() - INTERVAL '23 hours', 1, '11111111-1111-1111-1111-111111111111'),
    ('Niaj',    '203.0.113.18', 'direct', 'Meeting moved to 3pm',          'printed', NOW() - INTERVAL '10 hours', NOW() - INTERVAL '10 hours', NOW() - INTERVAL '9 hours',  NOW() - INTERVAL '9 hours',  NOW() - INTERVAL '9 hours',  2, '22222222-2222-2222-2222-222222222222');

-- rejected: refused at moderation
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, printer_id) VALUES
    ('Oscar', '198.51.100.20', 'web', 'BUY CHEAP STUFF NOW!!!',            'rejected', NOW() - INTERVAL '3 hours', NOW() - INTERVAL '2 hours', '11111111-1111-1111-1111-111111111111');

-- failed: exhausted retries (attempts = maxAttempts = 3)
INSERT INTO messages (sender_name, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, attempts, last_error, printer_id) VALUES
    ('Peggy', '203.0.113.19', 'web', 'This one never made it',             'failed', NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days', 3, 'paper jam', '33333333-3333-3333-3333-333333333333');
