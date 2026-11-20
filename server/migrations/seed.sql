-- ============================================================
-- Seed data for local development
-- Run top to bottom on a freshly migrated database.
-- ============================================================

-- -- Reset (optional) -----------------------------------------
-- Wipes all data and restarts messages.sequence_number at 1.

TRUNCATE messages, user_sessions, user_printers, users, printers, printer_join_tokens RESTART IDENTITY CASCADE;

-- -- Printers -------------------------------------------------
-- Fixed IDs so the rows below (and manual API calls) can reference them.
--   Alice: no quiet hours, so claims always work (use it to test the printer flow)
--   Bob:   overnight quiet hours (22:00-07:00, crosses midnight)
--   Carol: same-day quiet hours (12:00-13:00), silent for two weeks and reporting an error
-- token_hash is sha256('bbr_printer_dev_<name>')

INSERT INTO printers (id, model, recipient_name, quiet_start, quiet_end, timezone, error, created_at, last_heartbeat_at, token_hash) VALUES
    ('11111111-1111-1111-1111-111111111111', 'Epson TM-T20III', 'Alice and Dave', NULL,    NULL,   NULL, NULL,        NOW() - INTERVAL '30 days', NOW() - INTERVAL '10 seconds', sha256('bbr_printer_dev_alice')),
    ('22222222-2222-2222-2222-222222222222', 'Epson TM-T20III', 'Eve Office',           '22:00', '07:00', 'Europe/Brussels', NULL,        NOW() - INTERVAL '20 days', NOW() - INTERVAL '8 hours',    sha256('bbr_printer_dev_bob')),
    ('33333333-3333-3333-3333-333333333333', 'Star TSP143',     'Frank',         '12:00', '13:00', 'Europe/Brussels', 'paper jam', NOW() - INTERVAL '60 days', NOW() - INTERVAL '14 days',    sha256('bbr_printer_dev_carol')),
    ('44444444-4444-4444-4444-444444444444', 'Rongta RT-20',    'Marc',          '12:00', '13:00', 'Europe/Brussels', NULL,        NOW() - INTERVAL '60 days', NOW() - INTERVAL '14 days',    sha256('bbr_printer_dev_marc'));

-- -- Users ----------------------------------------------------
--   Dave:  active session, may send to his printer and Marc's printer
--   Eve:   active session, may send to her printer, Alice/Dave's, and Marc's printer
--   Frank: one revoked session, may send to his printer and Marc's printer
--   Grace: invited, invite not claimed yet (claim it with bbr_invite_dev_grace), may send to Alice/Dave's printer
--   Marc:  active session, may send to his printer only
--   Heidi: active session, member of Alice/Dave's printer only (removing her revokes her sessions);
--          she signed up with a join token from Dave
-- Names are unique per printer (case-insensitive), not globally.

INSERT INTO users (id, name, is_admin, invite_hash, invite_expires_at, created_at) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', 'Dave',  TRUE, NULL,                            NULL,                      NOW() - INTERVAL '25 days'),
    ('aaaaaaaa-0000-0000-0000-000000000002', 'Eve',   FALSE, NULL,                            NULL,                      NOW() - INTERVAL '15 days'),
    ('aaaaaaaa-0000-0000-0000-000000000003', 'Frank', FALSE, NULL,                            NULL,                      NOW() - INTERVAL '40 days'),
    ('aaaaaaaa-0000-0000-0000-000000000004', 'Grace', FALSE, sha256('bbr_invite_dev_grace'),  NOW() + INTERVAL '2 days', NOW() - INTERVAL '1 hour'),
    ('aaaaaaaa-0000-0000-0000-000000000005', 'Marc',  FALSE, NULL,                            NULL,                      NOW() - INTERVAL '1 hour'),
    ('aaaaaaaa-0000-0000-0000-000000000006', 'Heidi', FALSE, NULL,                            NULL,                      NOW() - INTERVAL '2 hours');

INSERT INTO user_printers (user_id, printer_id, role) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', '11111111-1111-1111-1111-111111111111', 'owner'),
    ('aaaaaaaa-0000-0000-0000-000000000002', '11111111-1111-1111-1111-111111111111', 'member'),
    ('aaaaaaaa-0000-0000-0000-000000000002', '22222222-2222-2222-2222-222222222222', 'owner'),
    ('aaaaaaaa-0000-0000-0000-000000000003', '33333333-3333-3333-3333-333333333333', 'owner'),
    ('aaaaaaaa-0000-0000-0000-000000000004', '11111111-1111-1111-1111-111111111111', 'member'),
    ('aaaaaaaa-0000-0000-0000-000000000005', '44444444-4444-4444-4444-444444444444', 'owner'),
    ('aaaaaaaa-0000-0000-0000-000000000001', '44444444-4444-4444-4444-444444444444', 'member'),
    ('aaaaaaaa-0000-0000-0000-000000000002', '44444444-4444-4444-4444-444444444444', 'member'),
    ('aaaaaaaa-0000-0000-0000-000000000003', '44444444-4444-4444-4444-444444444444', 'member'),
    ('aaaaaaaa-0000-0000-0000-000000000006', '11111111-1111-1111-1111-111111111111', 'member');

-- token_hash is sha256('bbr_session_dev_<name>')
INSERT INTO user_sessions (id, user_id, device_name, token_hash, created_at, revoked_at) VALUES
    ('cccccccc-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000001', 'Dave''s phone',   sha256('bbr_session_dev_dave'),  NOW() - INTERVAL '25 days', NULL),
    ('cccccccc-0000-0000-0000-000000000002', 'aaaaaaaa-0000-0000-0000-000000000002', 'Eve''s laptop',   sha256('bbr_session_dev_eve'),   NOW() - INTERVAL '15 days', NULL),
    ('cccccccc-0000-0000-0000-000000000003', 'aaaaaaaa-0000-0000-0000-000000000003', 'Old tablet',      sha256('bbr_session_dev_frank'), NOW() - INTERVAL '40 days', NOW() - INTERVAL '10 days'),
    ('cccccccc-0000-0000-0000-000000000005', 'aaaaaaaa-0000-0000-0000-000000000005', 'Marc''s desktop', sha256('bbr_session_dev_marc'),  NOW() - INTERVAL '1 hour',  NULL),
    ('cccccccc-0000-0000-0000-000000000006', 'aaaaaaaa-0000-0000-0000-000000000006', 'Heidi''s phone',  sha256('bbr_session_dev_heidi'), NOW() - INTERVAL '2 hours', NULL);

-- -- Printer join tokens --------------------------------------
-- token_hash is sha256('bbr_join_dev_<name>'); created_by NULL = created by the admin.
--   marc:  pending, created by Marc for his printer (sign up with it, or redeem it from a session)
--   heidi: used by Heidi to sign up on Alice/Dave's printer, created by Dave (its owner)
--   eve:   revoked, created by the admin for Eve's office printer

INSERT INTO printer_join_tokens (id, printer_id, created_by, used_by, token_hash, created_at, expires_at, used_at, revoked_at) VALUES
    ('dddddddd-0000-0000-0000-000000000001', '44444444-4444-4444-4444-444444444444', 'aaaaaaaa-0000-0000-0000-000000000005', NULL,                                   sha256('bbr_join_dev_marc'),  NOW() - INTERVAL '10 minutes', NOW() + INTERVAL '2 days',            NULL,                       NULL),
    ('dddddddd-0000-0000-0000-000000000002', '11111111-1111-1111-1111-111111111111', 'aaaaaaaa-0000-0000-0000-000000000001', 'aaaaaaaa-0000-0000-0000-000000000006', sha256('bbr_join_dev_heidi'), NOW() - INTERVAL '3 hours',    NOW() + INTERVAL '2 days' - INTERVAL '3 hours', NOW() - INTERVAL '2 hours', NULL),
    ('dddddddd-0000-0000-0000-000000000003', '22222222-2222-2222-2222-222222222222', NULL,                                   NULL,                                   sha256('bbr_join_dev_eve'),   NOW() - INTERVAL '1 day',      NOW() + INTERVAL '1 day',             NULL,                       NOW() - INTERVAL '20 hours');

-- -- Messages -------------------------------------------------
-- sender_id is NULL for admin messages; the API derives sender_name from users.name.
-- Every web message targets a printer its sender is linked to in user_printers.
-- Timestamps follow the status lifecycle:
--   awaiting_approval -> pending (moderated_at) -> claimed (claimed_at) -> processed (processed_at) -> printed (printed_at)
--   awaiting_approval -> rejected (moderated_at)
--   claimed/processed -> pending (retry, attempts < 3) or failed (attempts >= 3), with last_error set
--
-- Note: the claimed and processed rows below become stale once they are older than claimTimeout
-- (2 minutes), so the API's claim reaper will release them shortly after it starts.

-- awaiting_approval: not moderated yet
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', '203.0.113.10', 'web', 'Happy birthday Alice!',         'awaiting_approval', NOW() - INTERVAL '5 minutes', '11111111-1111-1111-1111-111111111111'),
    ('aaaaaaaa-0000-0000-0000-000000000002', '203.0.113.11', 'web', 'Can you pick up milk tonight?', 'awaiting_approval', NOW() - INTERVAL '3 minutes', '22222222-2222-2222-2222-222222222222');

-- pending: approved (or sent by an admin) and waiting in the printer queue (oldest first)
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, moderated_at, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', '203.0.113.12', 'web',   'Good morning!',                'pending', NOW() - INTERVAL '40 minutes', NOW() - INTERVAL '38 minutes', '11111111-1111-1111-1111-111111111111'),
    ('aaaaaaaa-0000-0000-0000-000000000002', '203.0.113.13', 'web',   E'Dinner at 7?\nBring dessert', 'pending', NOW() - INTERVAL '20 minutes', NOW() - INTERVAL '19 minutes', '11111111-1111-1111-1111-111111111111'),
    (NULL,                                   '127.0.0.1',    'admin', 'Maintenance tonight at 22:00', 'pending', NOW() - INTERVAL '15 minutes', NULL,                         '22222222-2222-2222-2222-222222222222');

-- pending (retry): a previous print attempt failed but attempts < 3
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, claimed_at, processed_at, attempts, last_error, printer_id) VALUES
    (NULL, '127.0.0.1', 'admin', 'Retry me please', 'pending', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 hour', NOW() - INTERVAL '1 hour', 1, 'printer offline', '11111111-1111-1111-1111-111111111111');

-- claimed: picked up by the printer, not processed yet
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, moderated_at, claimed_at, attempts, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', '203.0.113.15', 'web', 'See you soon', 'claimed', NOW() - INTERVAL '50 minutes', NOW() - INTERVAL '49 minutes', NOW() - INTERVAL '1 minute', 1, '11111111-1111-1111-1111-111111111111');

-- processed: rendered by the printer, currently being printed
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, attempts, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000002', '203.0.113.16', 'web', 'Thinking of you', 'processed', NOW() - INTERVAL '1 hour', NOW() - INTERVAL '59 minutes', NOW() - INTERVAL '30 seconds', NOW() - INTERVAL '25 seconds', 1, '11111111-1111-1111-1111-111111111111');

-- printed: done
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, printed_at, attempts, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', '203.0.113.17', 'web',   'Welcome home!',        'printed', NOW() - INTERVAL '1 day',    NOW() - INTERVAL '1 day', NOW() - INTERVAL '23 hours', NOW() - INTERVAL '23 hours', NOW() - INTERVAL '23 hours', 1, '11111111-1111-1111-1111-111111111111'),
    (NULL,                                   '127.0.0.1',    'admin', 'Meeting moved to 3pm', 'printed', NOW() - INTERVAL '10 hours', NULL,                    NOW() - INTERVAL '9 hours',  NOW() - INTERVAL '9 hours',  NOW() - INTERVAL '9 hours',  2, '22222222-2222-2222-2222-222222222222');

-- rejected: refused at moderation
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, moderated_at, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000003', '198.51.100.20', 'web', 'BUY CHEAP STUFF NOW!!!', 'rejected', NOW() - INTERVAL '3 hours', NOW() - INTERVAL '2 hours', '33333333-3333-3333-3333-333333333333');

-- failed: exhausted retries (attempts = maxAttempts = 3); requeue it with PATCH /admin/messages/{id}/requeue
INSERT INTO messages (sender_id, sender_ip, source, content, status, created_at, moderated_at, claimed_at, processed_at, attempts, last_error, printer_id) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000003', '203.0.113.19', 'web', 'This one never made it', 'failed', NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days', 3, 'paper jam', '33333333-3333-3333-3333-333333333333');
