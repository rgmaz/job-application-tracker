package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/files"
	"github.com/rgmaz/job-application-tracker/internal/service"
	"github.com/rgmaz/job-application-tracker/internal/store"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

type documentFixture struct {
	apps     *service.ApplicationService
	docs     *service.DocumentService
	db       *sql.DB
	clock    *fixedClock
	filesDir string
}

func newDocumentFixture(t *testing.T, now time.Time) *documentFixture {
	t.Helper()

	dir := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}

	filesDir := filepath.Join(dir, "files")
	fileStore, err := files.NewStore(filesDir, 1<<20)
	if err != nil {
		t.Fatalf("files.NewStore: %v", err)
	}

	clock := &fixedClock{now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return &documentFixture{
		apps:     service.NewApplications(db, clock),
		docs:     service.NewDocuments(db, clock, fileStore, logger),
		db:       db,
		clock:    clock,
		filesDir: filesDir,
	}
}

func (f *documentFixture) createApp(t *testing.T) storedb.Application {
	t.Helper()

	app, err := f.apps.Create(context.Background(), service.CreateApplicationInput{
		ApplicationFields: validInput(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return app
}

func (f *documentFixture) fileCount(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(f.filesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	return len(entries)
}

func testPDF() []byte {
	return []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

func TestDocumentUpload(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	f := newDocumentFixture(t, now)
	ctx := context.Background()
	app := f.createApp(t)
	content := testPDF()

	doc, err := f.docs.Upload(ctx, app.ID, "cv", "My CV.pdf", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if doc.ApplicationID != app.ID || doc.Kind != "cv" {
		t.Errorf("document = %+v, want application %d kind cv", doc, app.ID)
	}
	if doc.OriginalName != "My CV.pdf" {
		t.Errorf("original name = %q, want My CV.pdf", doc.OriginalName)
	}
	if doc.Mime != "application/pdf" {
		t.Errorf("mime = %q, want application/pdf", doc.Mime)
	}
	if doc.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", doc.Size, len(content))
	}
	if doc.Sha256 == "" {
		t.Error("sha256 is empty")
	}
	if doc.StorageKey == "" {
		t.Error("storage key is empty")
	}
	if doc.UploadedAt != now.Unix() {
		t.Errorf("uploaded_at = %d, want %d", doc.UploadedAt, now.Unix())
	}
	if got := f.fileCount(t); got != 1 {
		t.Errorf("stored files = %d, want 1", got)
	}

	stored, err := os.ReadFile(filepath.Join(f.filesDir, doc.StorageKey))
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Error("stored bytes differ from the uploaded bytes")
	}

	opened, file, err := f.docs.Open(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Errorf("close: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read open file: %v", err)
	}
	if opened.ID != doc.ID || !bytes.Equal(got, content) {
		t.Errorf("Open returned document %d with %d bytes, want %d with %d", opened.ID, len(got), doc.ID, len(content))
	}

	list, err := f.docs.List(ctx, app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != doc.ID {
		t.Errorf("List = %+v, want the uploaded document", list)
	}

	events, err := f.apps.ListEvents(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Type != "document_added" || events[0].Body == nil || !strings.Contains(*events[0].Body, "My CV.pdf") {
		t.Errorf("newest event = %+v, want a document_added event naming the file", events[0])
	}
}

func TestDocumentUploadValidation(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	f := newDocumentFixture(t, now)
	ctx := context.Background()
	app := f.createApp(t)

	tests := []struct {
		name       string
		kind       string
		original   string
		content    string
		wantFields []string
	}{
		{
			name:       "invalid kind",
			kind:       "passport",
			original:   "cv.pdf",
			content:    string(testPDF()),
			wantFields: []string{"kind"},
		},
		{
			name:       "empty file",
			kind:       "cv",
			original:   "cv.pdf",
			content:    "",
			wantFields: []string{"file"},
		},
		{
			name:       "html",
			kind:       "other",
			original:   "notes.html",
			content:    "<!doctype html><html><body>hi</body></html>",
			wantFields: []string{"file"},
		},
		{
			name:       "oversize",
			kind:       "other",
			original:   "big.bin",
			content:    strings.Repeat("x", (1<<20)+1),
			wantFields: []string{"file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.fileCount(t)

			_, err := f.docs.Upload(ctx, app.ID, tt.kind, tt.original, strings.NewReader(tt.content))
			fields := validationFields(t, err)
			if got := sortedKeys(fields); len(got) != len(tt.wantFields) || got[0] != tt.wantFields[0] {
				t.Errorf("fields = %v, want %v", got, tt.wantFields)
			}
			if after := f.fileCount(t); after != before {
				t.Errorf("stored files = %d, want %d", after, before)
			}
		})
	}

	if _, err := f.docs.Upload(ctx, app.ID+1000, "cv", "cv.pdf", bytes.NewReader(testPDF())); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Upload(missing application) error = %v, want sql.ErrNoRows", err)
	}
	if got := f.fileCount(t); got != 0 {
		t.Errorf("stored files after failed uploads = %d, want 0", got)
	}
}

func TestDocumentUploadRemovesFileWhenInsertFails(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	f := newDocumentFixture(t, now)
	ctx := context.Background()
	app := f.createApp(t)

	if _, err := f.db.ExecContext(ctx, "DROP TABLE documents"); err != nil {
		t.Fatalf("drop documents table: %v", err)
	}

	if _, err := f.docs.Upload(ctx, app.ID, "cv", "cv.pdf", bytes.NewReader(testPDF())); err == nil {
		t.Fatal("Upload succeeded with the documents table missing")
	}
	if got := f.fileCount(t); got != 0 {
		t.Errorf("stored files after failed insert = %d, want 0", got)
	}
}

func TestDocumentListOrder(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	f := newDocumentFixture(t, now)
	ctx := context.Background()
	app := f.createApp(t)

	first, err := f.docs.Upload(ctx, app.ID, "cv", "cv.pdf", bytes.NewReader(testPDF()))
	if err != nil {
		t.Fatalf("Upload first: %v", err)
	}

	f.clock.now = now.Add(time.Hour)
	second, err := f.docs.Upload(ctx, app.ID, "job_ad", "ad.pdf", bytes.NewReader(testPDF()))
	if err != nil {
		t.Fatalf("Upload second: %v", err)
	}

	list, err := f.docs.List(ctx, app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Errorf("List order = %+v, want newest first", list)
	}
}

func TestDocumentDelete(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	f := newDocumentFixture(t, now)
	ctx := context.Background()
	app := f.createApp(t)

	doc, err := f.docs.Upload(ctx, app.ID, "cv", "cv.pdf", bytes.NewReader(testPDF()))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	deleted, err := f.docs.Delete(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted.ID != doc.ID {
		t.Errorf("deleted = %+v, want document %d", deleted, doc.ID)
	}

	if _, err := f.docs.Get(ctx, doc.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get after delete error = %v, want sql.ErrNoRows", err)
	}
	if got := f.fileCount(t); got != 0 {
		t.Errorf("stored files after delete = %d, want 0", got)
	}
	if _, err := f.docs.Delete(ctx, doc.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Delete missing error = %v, want sql.ErrNoRows", err)
	}
}

func TestDocumentDeleteStored(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	f := newDocumentFixture(t, now)
	ctx := context.Background()
	app := f.createApp(t)

	doc, err := f.docs.Upload(ctx, app.ID, "cv", "cv.pdf", bytes.NewReader(testPDF()))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	f.docs.DeleteStored(ctx, []string{doc.StorageKey})
	if got := f.fileCount(t); got != 0 {
		t.Errorf("stored files after DeleteStored = %d, want 0", got)
	}

	// Unknown and malformed keys are logged, not fatal, so application delete
	// can finish even if a file was already removed by hand.
	f.docs.DeleteStored(ctx, []string{"not-a-key"})
}
