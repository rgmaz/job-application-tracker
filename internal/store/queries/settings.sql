-- Key/value settings (timezone, password hash, session secret) and the
-- per-status stale thresholds. Terminal statuses intentionally have no
-- threshold row; see applications.sql.

-- name: GetSetting :one
SELECT value FROM settings WHERE key = @key;

-- name: ListSettings :many
SELECT * FROM settings ORDER BY key;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value)
VALUES (@key, @value)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: ListThresholds :many
SELECT * FROM status_thresholds ORDER BY days DESC, status ASC;

-- name: UpsertThreshold :exec
INSERT INTO status_thresholds (status, days)
VALUES (@status, @days)
ON CONFLICT(status) DO UPDATE SET days = excluded.days;
