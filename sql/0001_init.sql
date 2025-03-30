-- +goose Up
-- +goose StatementBegin
CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    first_name TEXT,
    last_name TEXT,
    email TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at TIMESTAMPTZ
);

CREATE TABLE outbox_events (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    payload JSONB NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE accounts;
DROP TABLE outbox_events;
-- +goose StatementEnd
