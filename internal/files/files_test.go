package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgmaz/job-application-tracker/internal/files"
)

func newStore(t *testing.T, maxBytes int64) (*files.Store, string) {
	t.Helper()

	dir := t.TempDir()
	store, err := files.NewStore(dir, maxBytes)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, dir
}

func pdfBytes() []byte {
	return []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

func TestSaveOpenDelete(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t, 1<<20)
	content := pdfBytes()

	saved, err := store.Save(context.Background(), bytes.NewReader(content), "cv.pdf")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if len(saved.Key) != 32 {
		t.Errorf("key = %q, want 32 hex characters", saved.Key)
	}
	if _, err := hex.DecodeString(saved.Key); err != nil {
		t.Errorf("key = %q, want hex: %v", saved.Key, err)
	}
	if saved.OriginalName != "cv.pdf" {
		t.Errorf("original name = %q, want cv.pdf", saved.OriginalName)
	}
	if saved.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", saved.Size, len(content))
	}
	if saved.MIME != "application/pdf" {
		t.Errorf("mime = %q, want application/pdf", saved.MIME)
	}
	sum := sha256.Sum256(content)
	if saved.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %q, want %q", saved.SHA256, hex.EncodeToString(sum[:]))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("files in dir = %d, want 1", len(entries))
	}
	if entries[0].Name() != saved.Key {
		t.Errorf("stored name = %q, want the generated key %q", entries[0].Name(), saved.Key)
	}

	file, err := store.Open(saved.Key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Errorf("close: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("stored bytes differ from the uploaded bytes")
	}

	if err := store.Delete(saved.Key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Open(saved.Key); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open after delete error = %v, want os.ErrNotExist", err)
	}
	if err := store.Delete(saved.Key); err != nil {
		t.Errorf("Delete missing key = %v, want nil", err)
	}
}

func TestSaveRejectsOversize(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t, 8)

	_, err := store.Save(context.Background(), strings.NewReader("more than eight bytes"), "big.txt")
	if !errors.Is(err, files.ErrTooLarge) {
		t.Fatalf("Save error = %v, want ErrTooLarge", err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("files left behind after oversize upload = %d, want 0", len(entries))
	}
}

func TestSaveRejectsEmpty(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 1<<20)

	if _, err := store.Save(context.Background(), strings.NewReader(""), "empty.txt"); !errors.Is(err, files.ErrEmpty) {
		t.Errorf("Save error = %v, want ErrEmpty", err)
	}
}

func TestSaveSanitizesTraversalName(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t, 1<<20)

	saved, err := store.Save(context.Background(), bytes.NewReader(pdfBytes()), "../../etc/passwd")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.OriginalName != "passwd" {
		t.Errorf("original name = %q, want passwd", saved.OriginalName)
	}
	if strings.ContainsAny(saved.Key, `/\`) || strings.Contains(saved.Key, "..") {
		t.Errorf("key = %q, want an opaque name", saved.Key)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != saved.Key {
		t.Errorf("storage dir entries = %v, want only %q", entries, saved.Key)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "etc", "passwd")); err == nil {
		t.Error("upload escaped the storage directory")
	}
}

func TestSaveRejectsDangerousTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "html", content: "<!doctype html><html><body><script>alert(1)</script></body></html>"},
		{name: "xhtml", content: `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body>hi</body></html>`},
		{name: "svg", content: `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
		{name: "svg with xml prolog", content: `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, dir := newStore(t, 1<<20)
			_, err := store.Save(context.Background(), strings.NewReader(tt.content), "upload")
			if !errors.Is(err, files.ErrRejectedType) {
				t.Fatalf("Save error = %v, want ErrRejectedType", err)
			}

			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatalf("ReadDir: %v", readErr)
			}
			if len(entries) != 0 {
				t.Errorf("files left behind after rejected upload = %d, want 0", len(entries))
			}
		})
	}
}

func TestRejectsInvalidKeys(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 1<<20)

	for _, key := range []string{"", "../app.db", "abc", strings.Repeat("z", 32)} {
		if _, err := store.Open(key); !errors.Is(err, files.ErrInvalidKey) {
			t.Errorf("Open(%q) error = %v, want ErrInvalidKey", key, err)
		}
		if err := store.Delete(key); !errors.Is(err, files.ErrInvalidKey) {
			t.Errorf("Delete(%q) error = %v, want ErrInvalidKey", key, err)
		}
	}
}
