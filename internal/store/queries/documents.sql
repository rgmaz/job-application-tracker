-- Uploaded file metadata. Bytes live under DATA_DIR/files/<storage_key>; this
-- table is the authoritative list, so deletes remove the row first and the
-- file second.

-- name: CreateDocument :one
INSERT INTO documents (
    application_id, kind, original_name, mime, size, sha256, storage_key, uploaded_at
) VALUES (
    @application_id, @kind, @original_name, @mime, @size, @sha256, @storage_key, @uploaded_at
)
RETURNING *;

-- name: ListDocuments :many
SELECT * FROM documents
WHERE application_id = @application_id
ORDER BY uploaded_at DESC, id DESC;

-- name: GetDocument :one
SELECT * FROM documents WHERE id = @id;

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = @id;
