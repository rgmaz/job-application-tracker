// Package files stores uploaded document bytes under a data directory.
//
// Storage keys are random and never derived from user input, so an upload
// cannot choose its own path. Names are kept only as metadata for display and
// download filenames.
package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

const (
	keyBytes   = 16
	sniffBytes = 512
	nameLimit  = 255
)

var (
	// ErrTooLarge reports a stream that exceeds the configured size limit.
	ErrTooLarge = errors.New("file exceeds size limit")
	// ErrEmpty reports a zero-byte upload.
	ErrEmpty = errors.New("file is empty")
	// ErrRejectedType reports content that could execute in a browser.
	ErrRejectedType = errors.New("file type is not allowed")
	// ErrInvalidKey reports a storage key that is not a generated key.
	ErrInvalidKey = errors.New("invalid storage key")
)

// Saved describes a file that was written to the store.
type Saved struct {
	Key          string
	OriginalName string
	Size         int64
	SHA256       string
	MIME         string
}

// Store writes uploads into one flat directory. Every operation takes a key
// returned by Save; callers never supply a path.
type Store struct {
	dir      string
	maxBytes int64
}

// NewStore creates dir if needed and returns a store capped at maxBytes per
// file.
func NewStore(dir string, maxBytes int64) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("files: directory is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("files: size limit must be positive")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("files: create directory %q: %w", dir, err)
	}
	return &Store{dir: dir, maxBytes: maxBytes}, nil
}

// MaxBytes returns the per-file size limit.
func (s *Store) MaxBytes() int64 { return s.maxBytes }

// Save streams r into the store. It returns the generated key and the metadata
// the caller records in the database. Oversized, empty, and browser-executable
// content is rejected and leaves no file behind.
func (s *Store) Save(ctx context.Context, r io.Reader, originalName string) (Saved, error) {
	name := sanitizeName(originalName)

	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return Saved{}, fmt.Errorf("files: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	hash := sha256.New()
	head := &headBuffer{limit: sniffBytes}

	n, err := io.Copy(io.MultiWriter(tmp, hash, head), io.LimitReader(&contextReader{ctx: ctx, r: r}, s.maxBytes+1))
	if err != nil {
		cleanup()
		return Saved{}, fmt.Errorf("files: write %q: %w", name, err)
	}
	if n > s.maxBytes {
		cleanup()
		return Saved{}, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, s.maxBytes)
	}
	if n == 0 {
		cleanup()
		return Saved{}, ErrEmpty
	}

	mimeType := http.DetectContentType(head.Bytes())
	if err := checkAllowed(mimeType, head.Bytes()); err != nil {
		cleanup()
		return Saved{}, err
	}

	if err := tmp.Sync(); err != nil {
		cleanup()
		return Saved{}, fmt.Errorf("files: sync %q: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return Saved{}, fmt.Errorf("files: close %q: %w", name, err)
	}

	key, err := s.allocateKey()
	if err != nil {
		_ = os.Remove(tmpName)
		return Saved{}, err
	}
	if err := os.Rename(tmpName, s.path(key)); err != nil {
		_ = os.Remove(tmpName)
		return Saved{}, fmt.Errorf("files: store %q: %w", key, err)
	}

	return Saved{
		Key:          key,
		OriginalName: name,
		Size:         n,
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		MIME:         mimeType,
	}, nil
}

// Open returns the stored file for reading. The caller closes it.
func (s *Store) Open(key string) (*os.File, error) {
	if !validKey(key) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	file, err := os.Open(s.path(key))
	if err != nil {
		return nil, fmt.Errorf("files: open %q: %w", key, err)
	}
	return file, nil
}

// Delete removes a stored file. A missing file is not an error so cleanup is
// idempotent.
func (s *Store) Delete(key string) error {
	if !validKey(key) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("files: delete %q: %w", key, err)
	}
	return nil
}

func (s *Store) path(key string) string {
	return path.Join(s.dir, key)
}

// allocateKey retries in the vanishingly unlikely event of a collision.
func (s *Store) allocateKey() (string, error) {
	for range 5 {
		key, err := newKey()
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(s.path(key)); errors.Is(err, os.ErrNotExist) {
			return key, nil
		}
	}
	return "", errors.New("files: could not allocate a unique storage key")
}

func newKey() (string, error) {
	buf := make([]byte, keyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("files: generate key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func validKey(key string) bool {
	if len(key) != keyBytes*2 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

// sanitizeName reduces an uploaded filename to a safe display value. The
// storage key, not this name, decides where bytes are written.
func sanitizeName(raw string) string {
	raw = strings.ReplaceAll(raw, `\`, "/")
	name := path.Base(strings.TrimSpace(raw))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "upload"
	}
	if len(name) > nameLimit {
		name = name[len(name)-nameLimit:]
	}
	return name
}

// checkAllowed rejects types a browser could execute when served from this
// origin. XML is allowed only when it is not XHTML or SVG.
func checkAllowed(mimeType string, head []byte) error {
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		mediaType = mimeType
	}

	switch mediaType {
	case "text/html", "application/xhtml+xml", "image/svg+xml":
		return fmt.Errorf("%w: %s", ErrRejectedType, mediaType)
	case "text/xml", "application/xml":
		lower := bytes.ToLower(head)
		if bytes.Contains(lower, []byte("<html")) || bytes.Contains(lower, []byte("<svg")) {
			return fmt.Errorf("%w: %s", ErrRejectedType, mediaType)
		}
	}

	lower := bytes.ToLower(trimBOM(bytes.TrimSpace(head)))
	if bytes.HasPrefix(lower, []byte("<svg")) {
		return fmt.Errorf("%w: image/svg+xml", ErrRejectedType)
	}
	return nil
}

func trimBOM(buf []byte) []byte {
	return bytes.TrimPrefix(buf, []byte{0xef, 0xbb, 0xbf})
}

// headBuffer captures the first limit bytes written to it.
type headBuffer struct {
	buf   []byte
	limit int
}

func (h *headBuffer) Write(p []byte) (int, error) {
	if remaining := h.limit - len(h.buf); remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		h.buf = append(h.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (h *headBuffer) Bytes() []byte { return h.buf }

// contextReader stops a long upload when the request is cancelled.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
