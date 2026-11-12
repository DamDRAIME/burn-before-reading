-- Define a trigger to automatically update the last_updated_at column before any update operation
CREATE OR REPLACE FUNCTION set_last_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.last_updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Printers Table

CREATE TABLE printers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    model VARCHAR(50) NOT NULL,

    recipient_name VARCHAR(20) NOT NULL,

    quiet_start TIME,
    quiet_end TIME,
    timezone TEXT,

    error TEXT,

    token_hash BYTEA UNIQUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at TIMESTAMPTZ,

    CONSTRAINT printers_quiet_hours_check
        CHECK ((quiet_start IS NULL) = (quiet_end IS NULL))
);

CREATE TRIGGER trg_printers_last_updated_at
    BEFORE UPDATE ON printers
    FOR EACH ROW
    WHEN ( -- To prevent unnecessary updates to last_updated_at at each heartbeat
        (OLD.model, OLD.recipient_name, OLD.quiet_start, OLD.quiet_end, OLD.timezone) 
        IS DISTINCT FROM 
        (NEW.model, NEW.recipient_name, NEW.quiet_start, NEW.quiet_end, NEW.timezone)
    )
    EXECUTE FUNCTION set_last_updated_at();

-- Users Table

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    name VARCHAR(20) NOT NULL,

    invite_hash BYTEA UNIQUE,
    invite_expires_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT users_invite_check
        CHECK ((invite_hash IS NULL) = (invite_expires_at IS NULL))
);

-- Users-Printers Table

CREATE TABLE user_printers (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    printer_id UUID NOT NULL REFERENCES printers(id) ON DELETE CASCADE,

    PRIMARY KEY (user_id, printer_id)
);

-- Users-Sessions Table

CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    token_hash BYTEA NOT NULL UNIQUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

-- Messages Table

CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence_number BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,

    sender_id UUID REFERENCES users(id), -- NULL for admin messages
    sender_ip INET NOT NULL,
    source VARCHAR(20) NOT NULL DEFAULT 'web',

    content TEXT NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    moderated_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    processed_at TIMESTAMPTZ,
    printed_at TIMESTAMPTZ,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    attempts SMALLINT NOT NULL DEFAULT 0,
    last_error TEXT,

    printer_id UUID NOT NULL REFERENCES printers(id),

    CONSTRAINT messages_status_check CHECK (
        status IN (
            'awaiting_approval',
            'pending',
            'claimed',
            'processed',
            'printed',
            'rejected',
            'failed'
        )
    ),

    CONSTRAINT messages_source_check CHECK (
        source IN (
            'admin',
            'web'
        )
    )
);

CREATE INDEX idx_messages_pending
    ON messages (printer_id, created_at)
    WHERE status = 'pending';

CREATE TRIGGER trg_messages_last_updated_at
    BEFORE UPDATE ON messages
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE FUNCTION set_last_updated_at();