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

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT printers_quiet_hours_check
        CHECK ((quiet_start IS NULL) = (quiet_end IS NULL))
);

CREATE TRIGGER trg_printers_last_seen_at
    BEFORE UPDATE ON printers
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE FUNCTION set_last_seen_at();

-- Messages Table

CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence_number BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,

    sender_name VARCHAR(20) NOT NULL,
    sender_ip INET NOT NULL,
    source VARCHAR(20) NOT NULL DEFAULT 'web',

    content TEXT NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    moderated_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
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
            'printing',
            'printed',
            'rejected',
            'failed'
        )
    ),

    CONSTRAINT messages_source_check CHECK (
        source IN (
            'web',
            'admin',
            'direct'
        )
    )
);

CREATE INDEX idx_messages_pending
    ON messages (created_at)
    WHERE status = 'pending';

CREATE INDEX idx_messages_printer_id
    ON messages (printer_id);

CREATE TRIGGER trg_messages_last_updated_at
    BEFORE UPDATE ON messages
    FOR EACH ROW
    WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE FUNCTION set_last_updated_at();