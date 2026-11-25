package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func documentPDF() []byte {
	return []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

func (f *webFixture) upload(t *testing.T, target, kind, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if kind != "" {
		if err := writer.WriteField("kind", kind); err != nil {
			t.Fatalf("write kind field: %v", err)
		}
	}
	if filename != "" {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *webFixture) documentFiles(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(f.filesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	return len(entries)
}

func TestDocumentUploadAndDownload(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")
	content := documentPDF()

	rec := f.get(t, "/applications/"+itoa(app.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Documents",
		"No documents yet.",
		`enctype="multipart/form-data"`,
		`action="/applications/` + itoa(app.ID) + `/documents"`,
		`name="kind"`,
		`name="file"`,
		"Job ad",
		"Cover letter",
		"Upload document",
		"Up to 1 MB",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page does not contain %q", want)
		}
	}

	rec = f.upload(t, "/applications/"+itoa(app.ID)+"/documents", "cv", "My CV.pdf", content)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); !strings.Contains(got, "flash=document") {
		t.Errorf("Location = %q, want flash=document", got)
	}

	docs, err := f.docs.List(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("documents = %d, want 1", len(docs))
	}
	doc := docs[0]

	rec = f.get(t, rec.Header().Get("Location"))
	body = rec.Body.String()
	for _, want := range []string{
		"My CV.pdf",
		"/documents/" + itoa(doc.ID) + "/download",
		"CV",
		"Document uploaded.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page after upload does not contain %q", want)
		}
	}

	rec = f.get(t, "/documents/"+itoa(doc.ID)+"/download")
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Error("downloaded bytes differ from the uploaded bytes")
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", got)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment;") || !strings.Contains(disposition, "My CV.pdf") {
		t.Errorf("Content-Disposition = %q, want an attachment named My CV.pdf", disposition)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}
}

func TestDocumentUploadValidation(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")
	target := "/applications/" + itoa(app.ID) + "/documents"

	tests := []struct {
		name       string
		kind       string
		filename   string
		content    []byte
		wantStatus int
		wantError  string
	}{
		{
			name:       "html is rejected",
			kind:       "other",
			filename:   "notes.html",
			content:    []byte("<!doctype html><html><body>hi</body></html>"),
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "That file type is not allowed.",
		},
		{
			name:       "missing file",
			kind:       "cv",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "Choose a file to upload.",
		},
		{
			name:       "invalid kind",
			kind:       "passport",
			filename:   "cv.pdf",
			content:    documentPDF(),
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "Choose a document kind.",
		},
		{
			name:       "oversize",
			kind:       "other",
			filename:   "big.pdf",
			content:    bytes.Repeat([]byte("x"), (1<<20)+1),
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "File must be 1 MB or smaller.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.upload(t, target, tt.kind, tt.filename, tt.content)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantError) {
				t.Errorf("body does not contain %q", tt.wantError)
			}
		})
	}

	docs, err := f.docs.List(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("documents after failed uploads = %d, want 0", len(docs))
	}
	if got := f.documentFiles(t); got != 0 {
		t.Errorf("stored files after failed uploads = %d, want 0", got)
	}

	if rec := f.upload(t, "/applications/9999/documents", "cv", "cv.pdf", documentPDF()); rec.Code != http.StatusNotFound {
		t.Errorf("missing application upload status = %d, want 404", rec.Code)
	}
}

func TestDocumentDelete(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rec := f.upload(t, "/applications/"+itoa(app.ID)+"/documents", "cv", "cv.pdf", documentPDF())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	docs, err := f.docs.List(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("documents = %d, want 1", len(docs))
	}

	rec = f.post(t, "/documents/"+itoa(docs[0].ID)+"/delete", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	want := "/applications/" + itoa(app.ID) + "?flash=document_deleted"
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if got := f.documentFiles(t); got != 0 {
		t.Errorf("stored files after delete = %d, want 0", got)
	}
	if rec := f.get(t, rec.Header().Get("Location")); !strings.Contains(rec.Body.String(), "Document deleted.") {
		t.Error("detail page does not show the document deleted flash")
	}

	if rec := f.post(t, "/documents/9999/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing document delete status = %d, want 404", rec.Code)
	}
	if rec := f.post(t, "/documents/abc/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("invalid document delete status = %d, want 404", rec.Code)
	}
}

func TestDocumentDownloadMissing(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	for _, target := range []string{"/documents/9999/download", "/documents/abc/download"} {
		if rec := f.get(t, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, rec.Code)
		}
	}
}

func TestApplicationDeleteRemovesDocumentFiles(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rec := f.upload(t, "/applications/"+itoa(app.ID)+"/documents", "cv", "cv.pdf", documentPDF())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := f.documentFiles(t); got != 1 {
		t.Fatalf("stored files = %d, want 1", got)
	}

	if rec := f.post(t, "/applications/"+itoa(app.ID)+"/delete", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := f.documentFiles(t); got != 0 {
		t.Errorf("stored files after application delete = %d, want 0", got)
	}
}
