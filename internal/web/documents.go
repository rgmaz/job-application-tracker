package web

import (
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/service"
)

const (
	multipartMemory   = 8 << 20
	multipartOverhead = 1 << 20
)

// uploadDocument accepts a multipart upload, stores the bytes, and redirects
// back to the detail page. Validation failures re-render the page so the form
// and its error stay together.
func (s *server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	maxBytes := s.docs.MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+multipartOverhead)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.renderDetailError(w, r, id, http.StatusRequestEntityTooLarge, map[string]string{
				"file": fmt.Sprintf("File must be %d MB or smaller.", maxBytes>>20),
			})
			return
		}
		http.Error(w, "invalid upload", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		s.renderDetailError(w, r, id, http.StatusUnprocessableEntity, map[string]string{
			"file": "Choose a file to upload.",
		})
		return
	}
	defer func() { _ = file.Close() }()

	if _, err := s.docs.Upload(r.Context(), id, r.PostFormValue("kind"), header.Filename, file); err != nil {
		var invalid *service.ValidationError
		if errors.As(err, &invalid) {
			s.renderDetailError(w, r, id, http.StatusUnprocessableEntity, invalid.Fields)
			return
		}
		writeLoadError(w, r, err)
		return
	}

	http.Redirect(w, r, flashURL(id, "document"), http.StatusSeeOther)
}

// downloadDocument streams the stored bytes as an attachment. The type comes
// from the upload-time sniff, and nosniff stops a browser from reinterpreting
// the bytes as something executable.
func (s *server) downloadDocument(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	doc, file, err := s.docs.Open(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "could not open document", http.StatusInternalServerError)
		return
	}
	defer func() { _ = file.Close() }()

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": doc.OriginalName})
	if disposition == "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", doc.Mime)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, doc.OriginalName, time.Unix(doc.UploadedAt, 0), file)
}

func (s *server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	doc, err := s.docs.Delete(r.Context(), id)
	if err != nil {
		writeLoadError(w, r, err)
		return
	}

	http.Redirect(w, r, applicationURL(doc.ApplicationID)+"?flash=document_deleted", http.StatusSeeOther)
}

func (s *server) renderDetailError(w http.ResponseWriter, r *http.Request, id int64, status int, errs map[string]string) {
	app, events, docs, err := s.loadDetail(r, id)
	if err != nil {
		writeLoadError(w, r, err)
		return
	}
	s.renderDetail(w, r, status, app, events, docs, errs)
}
