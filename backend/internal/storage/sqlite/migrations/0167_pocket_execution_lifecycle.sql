-- +goose Up
-- +goose StatementBegin
-- Deterministic Pocket validation commands are task/execution policy facts.
-- The lifecycle watermark prevents upgrade-time reconciliation from turning
-- historical AO Chat turns into Pocket tasks unless a later retry explicitly
-- pulls one of those turns into a new attempt lineage.
ALTER TABLE pocket_validation_requirements
    ADD COLUMN command TEXT NOT NULL DEFAULT '';

CREATE TABLE pocket_lifecycle_state (
    singleton              INTEGER PRIMARY KEY CHECK (singleton = 1),
    automation_started_at  TIMESTAMP NOT NULL
);

INSERT INTO pocket_lifecycle_state (singleton, automation_started_at)
VALUES (1, CURRENT_TIMESTAMP);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE pocket_lifecycle_state;
ALTER TABLE pocket_validation_requirements DROP COLUMN command;
-- +goose StatementEnd
