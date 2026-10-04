-- +goose Up
ALTER TABLE model_usage_events ADD COLUMN native_turn_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_model_usage_events_native_turn ON model_usage_events(binding_id, native_turn_id)
    WHERE native_turn_id <> '';

-- +goose Down
DROP INDEX idx_model_usage_events_native_turn;
ALTER TABLE model_usage_events DROP COLUMN native_turn_id;
