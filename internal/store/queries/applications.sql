-- Queries for the applications aggregate.
--
-- Optional list filters use empty-string / zero sentinels ("" and 0) instead
-- of nullable parameters: sqlc's SQLite engine types sqlc.narg as interface{},
-- which would push type assertions into every caller. Explicit CASTs keep the
-- generated params concrete (string / int64).
--
-- Staleness is derived, never stored. A status with no status_thresholds row
-- (every terminal status) yields t.days IS NULL and therefore is_stale = 0.

-- name: CreateApplication :one
INSERT INTO applications (
    company, role, location, job_url, source, applied_on, status,
    status_changed_on, status_changed_at, follow_up_on,
    contact_name, contact_email, email_subject, email_link, notes,
    created_at, updated_at
) VALUES (
    @company, @role, @location, @job_url, @source, @applied_on, @status,
    @status_changed_on, @status_changed_at, @follow_up_on,
    @contact_name, @contact_email, @email_subject, @email_link, @notes,
    @created_at, @updated_at
)
RETURNING *;

-- name: GetApplication :one
SELECT * FROM applications WHERE id = @id;

-- name: ListApplications :many
SELECT
    a.*,
    COALESCE(a.follow_up_on, date(a.status_changed_on, '+' || t.days || ' days')) AS due_on,
    CASE
        WHEN t.days IS NOT NULL
         AND COALESCE(a.follow_up_on, date(a.status_changed_on, '+' || t.days || ' days')) <= CAST(@today AS TEXT)
        THEN 1 ELSE 0
    END AS is_stale
FROM applications a
LEFT JOIN status_thresholds t ON t.status = a.status
WHERE (CAST(@status_filter AS TEXT) = '' OR a.status = CAST(@status_filter AS TEXT))
  AND (CAST(@source_filter AS TEXT) = '' OR a.source = CAST(@source_filter AS TEXT))
  AND (CAST(@q AS TEXT) = ''
       OR instr(lower(a.company || ' ' || a.role || ' ' || COALESCE(a.notes, '')),
                lower(CAST(@q AS TEXT))) > 0)
  AND (CAST(@stale_only AS INTEGER) = 0
       OR (t.days IS NOT NULL
           AND COALESCE(a.follow_up_on, date(a.status_changed_on, '+' || t.days || ' days')) <= CAST(@today AS TEXT)))
ORDER BY a.created_at DESC, a.id DESC
LIMIT @limit OFFSET @offset;

-- name: UpdateApplication :one
UPDATE applications
SET company = @company,
    role = @role,
    location = @location,
    job_url = @job_url,
    source = @source,
    applied_on = @applied_on,
    follow_up_on = @follow_up_on,
    contact_name = @contact_name,
    contact_email = @contact_email,
    email_subject = @email_subject,
    email_link = @email_link,
    notes = @notes,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: UpdateStatus :one
UPDATE applications
SET status = @status,
    status_changed_on = @status_changed_on,
    status_changed_at = @status_changed_at,
    updated_at = @updated_at
WHERE id = @id
RETURNING *;

-- name: DeleteApplication :execrows
DELETE FROM applications WHERE id = @id;

-- name: DashboardCounts :one
SELECT
    COUNT(*) AS total,
    CAST(COALESCE(SUM(CASE WHEN a.status NOT IN ('accepted', 'rejected', 'withdrawn', 'ghosted')
                           THEN 1 ELSE 0 END), 0) AS INTEGER) AS active,
    CAST(COALESCE(SUM(CASE WHEN a.status = 'interviewing' THEN 1 ELSE 0 END), 0) AS INTEGER) AS interviewing,
    CAST(COALESCE(SUM(CASE WHEN a.status = 'offer' THEN 1 ELSE 0 END), 0) AS INTEGER) AS offers,
    CAST(COALESCE(SUM(CASE WHEN t.days IS NOT NULL
                           AND COALESCE(a.follow_up_on, date(a.status_changed_on, '+' || t.days || ' days')) <= CAST(@today AS TEXT)
                          THEN 1 ELSE 0 END), 0) AS INTEGER) AS stale
FROM applications a
LEFT JOIN status_thresholds t ON t.status = a.status;
