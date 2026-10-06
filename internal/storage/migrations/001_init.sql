CREATE TABLE persons (
    id            BIGSERIAL PRIMARY KEY,
    login         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    contacts      TEXT        NOT NULL DEFAULT '',
    role          TEXT        NOT NULL CHECK (role IN ('organizer', 'participant')),
    failed_logins INT         NOT NULL DEFAULT 0,
    locked_until  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- В таблице хранится только SHA-256 от токена (D-01).
CREATE TABLE sessions (
    token_hash BYTEA       PRIMARY KEY,
    person_id  BIGINT      NOT NULL REFERENCES persons (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_person_id_idx ON sessions (person_id);

CREATE TABLE events (
    id           BIGSERIAL PRIMARY KEY,
    organizer_id BIGINT      NOT NULL REFERENCES persons (id),
    title        TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    starts_at    TIMESTAMPTZ NOT NULL,
    capacity     INT         NOT NULL CHECK (capacity > 0),
    free_seats   INT         NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Последняя линия защиты от переполнения (D-03).
    CONSTRAINT free_seats_range CHECK (free_seats >= 0 AND free_seats <= capacity)
);

CREATE INDEX events_organizer_id_idx ON events (organizer_id);

CREATE TABLE registrations (
    id           BIGSERIAL PRIMARY KEY,
    event_id     BIGINT      NOT NULL REFERENCES events (id),
    person_id    BIGINT      NOT NULL REFERENCES persons (id),
    status       TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancelled_at TIMESTAMPTZ
);

-- Одна активная запись человека на мероприятие (SR-06, D-03).
CREATE UNIQUE INDEX registrations_one_active_idx
    ON registrations (event_id, person_id) WHERE status = 'active';

CREATE INDEX registrations_person_id_idx ON registrations (person_id);
