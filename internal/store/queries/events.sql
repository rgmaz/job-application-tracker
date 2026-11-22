-- Timeline events. Every status change and document upload appends one row;
-- notes are user-authored events.

-- name: CreateEvent :one
INSERT INTO events (application_id, type, body, occurred_at, created_at)
VALUES (@application_id, @type, @body, @occurred_at, @created_at)
RETURNING *;

-- name: ListEvents :many
SELECT * FROM events
WHERE application_id = @application_id
ORDER BY occurred_at DESC, id DESC;

-- name: DeleteEvent :execrows
DELETE FROM events WHERE id = @id;
