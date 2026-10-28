CREATE TABLE printers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(20) NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'active',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ,
    last_job_at TIMESTAMPTZ,

    last_error TEXT,

    CONSTRAINT printers_status_check CHECK (
        status IN (
            'active',
            'inactive',
            'sleeping'
        )
    )
);


CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    sender_name VARCHAR(20) NOT NULL,
    sender_ip INET NOT NULL,
    source VARCHAR(20) NOT NULL DEFAULT 'web',

    content TEXT NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    printed_at TIMESTAMPTZ,

    attempts SMALLINT NOT NULL DEFAULT 0,
    last_error TEXT,

    printer_id UUID REFERENCES printers(id),

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