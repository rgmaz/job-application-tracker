package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/rgmaz/job-application-tracker/internal/files"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

const eventDocumentAdded = "document_added"

// DocumentKindOption is a document kind offered by the UI.
type DocumentKindOption struct {
	Value string
	Label string
}

// DocumentKinds are the kinds accepted by the documents table.
var DocumentKinds = []DocumentKindOption{
	{Value: "job_ad", Label: "Job ad"},
	{Value: "cv", Label: "CV"},
	{Value: "cover_letter", Label: "Cover letter"},
	{Value: "email", Label: "Email"},
	{Value: "other", Label: "Other"},
}

// ValidDocumentKind reports whether raw is a known document kind.
func ValidDocumentKind(raw string) bool {
	for _, option := range DocumentKinds {
		if option.Value == raw {
			return true
		}
	}
	return false
}

func documentKindLabel(raw string) string {
	for _, option := range DocumentKinds {
		if option.Value == raw {
			return option.Label
		}
	}
	return raw
}

// DocumentService owns the file/row split for uploads. Files are written
// first, then the row and its timeline event commit together; a failed commit
// removes the file so the database stays authoritative.
type DocumentService struct {
	db     *sql.DB
	q      *storedb.Queries
	clock  Clock
	files  *files.Store
	logger *slog.Logger
}

// NewDocuments returns a document service backed by db and store.
func NewDocuments(db *sql.DB, clock Clock, store *files.Store, logger *slog.Logger) *DocumentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &DocumentService{
		db:     db,
		q:      storedb.New(db),
		clock:  clock,
		files:  store,
		logger: logger,
	}
}

// MaxUploadBytes is the per-file limit enforced by the store.
func (s *DocumentService) MaxUploadBytes() int64 { return s.files.MaxBytes() }

// Upload stores the stream and records the document plus a document_added
// event in one transaction.
func (s *DocumentService) Upload(ctx context.Context, applicationID int64, rawKind, originalName string, r io.Reader) (storedb.Document, error) {
	kind := strings.TrimSpace(rawKind)
	if !ValidDocumentKind(kind) {
		return storedb.Document{}, &ValidationError{
			Fields: map[string]string{"kind": "Choose a document kind."},
		}
	}

	if _, err := s.q.GetApplication(ctx, applicationID); err != nil {
		return storedb.Document{}, fmt.Errorf("get application %d: %w", applicationID, err)
	}

	saved, err := s.files.Save(ctx, r, originalName)
	if err != nil {
		return storedb.Document{}, s.uploadError(err)
	}

	keepFile := false
	defer func() {
		if !keepFile {
			s.deleteOrphan(saved.Key)
		}
	}()

	now := s.clock.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storedb.Document{}, fmt.Errorf("begin document upload: %w", err)
	}
	defer rollback(tx)

	q := s.q.WithTx(tx)
	doc, err := q.CreateDocument(ctx, storedb.CreateDocumentParams{
		ApplicationID: applicationID,
		Kind:          kind,
		OriginalName:  saved.OriginalName,
		Mime:          saved.MIME,
		Size:          saved.Size,
		Sha256:        saved.SHA256,
		StorageKey:    saved.Key,
		UploadedAt:    now.Unix(),
	})
	if err != nil {
		return storedb.Document{}, fmt.Errorf("create document for application %d: %w", applicationID, err)
	}

	body := fmt.Sprintf("Added %s: %s", documentKindLabel(kind), saved.OriginalName)
	if _, err := q.CreateEvent(ctx, storedb.CreateEventParams{
		ApplicationID: applicationID,
		Type:          eventDocumentAdded,
		Body:          &body,
		OccurredAt:    now.Unix(),
		CreatedAt:     now.Unix(),
	}); err != nil {
		return storedb.Document{}, fmt.Errorf("create document event for application %d: %w", applicationID, err)
	}

	if err := tx.Commit(); err != nil {
		return storedb.Document{}, fmt.Errorf("commit document upload for application %d: %w", applicationID, err)
	}
	keepFile = true
	return doc, nil
}

// List returns an application's documents, newest first.
func (s *DocumentService) List(ctx context.Context, applicationID int64) ([]storedb.Document, error) {
	docs, err := s.q.ListDocuments(ctx, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list documents for application %d: %w", applicationID, err)
	}
	return docs, nil
}

// Get returns one document row, or sql.ErrNoRows.
func (s *DocumentService) Get(ctx context.Context, id int64) (storedb.Document, error) {
	doc, err := s.q.GetDocument(ctx, id)
	if err != nil {
		return storedb.Document{}, fmt.Errorf("get document %d: %w", id, err)
	}
	return doc, nil
}

// Open returns the document metadata and its stored file. The caller closes
// the file.
func (s *DocumentService) Open(ctx context.Context, id int64) (storedb.Document, *os.File, error) {
	doc, err := s.Get(ctx, id)
	if err != nil {
		return storedb.Document{}, nil, err
	}
	file, err := s.files.Open(doc.StorageKey)
	if err != nil {
		return storedb.Document{}, nil, fmt.Errorf("open document %d: %w", id, err)
	}
	return doc, file, nil
}

// Delete removes the row first and the file second, logging a file that could
// not be removed as an orphan.
func (s *DocumentService) Delete(ctx context.Context, id int64) (storedb.Document, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storedb.Document{}, fmt.Errorf("begin delete document: %w", err)
	}
	defer rollback(tx)

	q := s.q.WithTx(tx)
	doc, err := q.GetDocument(ctx, id)
	if err != nil {
		return storedb.Document{}, fmt.Errorf("get document %d: %w", id, err)
	}

	deleted, err := q.DeleteDocument(ctx, id)
	if err != nil {
		return storedb.Document{}, fmt.Errorf("delete document %d: %w", id, err)
	}
	if deleted != 1 {
		return storedb.Document{}, fmt.Errorf("delete document %d: deleted %d rows", id, deleted)
	}

	if err := tx.Commit(); err != nil {
		return storedb.Document{}, fmt.Errorf("commit delete document %d: %w", id, err)
	}

	s.deleteOrphan(doc.StorageKey)
	return doc, nil
}

// DeleteStored removes files whose rows were already deleted, such as when an
// application cascade takes its documents with it.
func (s *DocumentService) DeleteStored(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return
		}
		s.deleteOrphan(key)
	}
}

func (s *DocumentService) uploadError(err error) error {
	switch {
	case errors.Is(err, files.ErrTooLarge):
		return &ValidationError{Fields: map[string]string{
			"file": fmt.Sprintf("File must be %d MB or smaller.", s.files.MaxBytes()>>20),
		}}
	case errors.Is(err, files.ErrEmpty):
		return &ValidationError{Fields: map[string]string{"file": "Choose a file to upload."}}
	case errors.Is(err, files.ErrRejectedType):
		return &ValidationError{Fields: map[string]string{"file": "That file type is not allowed."}}
	default:
		return fmt.Errorf("store upload: %w", err)
	}
}

func (s *DocumentService) deleteOrphan(key string) {
	if err := s.files.Delete(key); err != nil {
		s.logger.Warn("document file cleanup failed", "key", key, "error", err)
	}
}
