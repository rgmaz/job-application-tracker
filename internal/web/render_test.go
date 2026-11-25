package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/timeutil"
	webassets "github.com/rgmaz/job-application-tracker/web"
)

func testClock(t *testing.T) *timeutil.Clock {
	t.Helper()

	loc, err := time.LoadLocation("Africa/Harare")
	if err != nil {
		t.Fatalf("load Africa/Harare: %v", err)
	}
	return timeutil.New(loc)
}

func TestTemplatesParse(t *testing.T) {
	t.Parallel()

	renderer, err := NewRenderer(testClock(t))
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	rec := httptest.NewRecorder()
	data := PageData{Title: "Test", Flash: "Saved", Nav: "applications"}
	if err := renderer.Render(rec, http.StatusOK, "layout", data); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"<title>Test · Job Application Tracker</title>",
		`href="/static/pico.min.css"`,
		`href="/static/app.css"`,
		`src="/static/htmx.min.js"`,
		`src="/static/app.js"`,
		`href="/applications" aria-current="page"`,
		`role="status">Saved</article>`,
		"Nothing to show yet.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	if err := renderer.Render(rec, http.StatusOK, "layout", PageData{Title: "No flash"}); err != nil {
		t.Fatalf("Render without flash: %v", err)
	}
	if strings.Contains(rec.Body.String(), `class="flash"`) {
		t.Error("body contains a flash element when Flash is empty")
	}

	if err := renderer.Render(httptest.NewRecorder(), http.StatusOK, "missing.html", PageData{}); err == nil {
		t.Error("Render(unknown page) error = nil, want error")
	}
}

func TestTemplateFuncs(t *testing.T) {
	t.Parallel()

	clock := testClock(t)
	funcs := templateFuncs(clock)

	fmtDate := funcs["fmtDate"].(func(any) string)
	fmtDateTime := funcs["fmtDateTime"].(func(any) string)
	statusLabel := funcs["statusLabel"].(func(any) string)
	sourceLabel := funcs["sourceLabel"].(func(any) string)
	eventLabel := funcs["eventLabel"].(func(any) string)
	isTerminal := funcs["isTerminal"].(func(any) bool)
	documentKindLabel := funcs["documentKindLabel"].(func(any) string)
	fileSize := funcs["fileSize"].(func(any) string)

	date := "2026-11-23"
	if got := fmtDate(date); got != "23 Nov 2026" {
		t.Errorf("fmtDate(%q) = %q, want 23 Nov 2026", date, got)
	}
	if got := fmtDate(&date); got != "23 Nov 2026" {
		t.Errorf("fmtDate(*string) = %q, want 23 Nov 2026", got)
	}
	if got := fmtDate(""); got != "" {
		t.Errorf("fmtDate(\"\") = %q, want empty", got)
	}
	if got := fmtDate("not-a-date"); got != "not-a-date" {
		t.Errorf("fmtDate(invalid) = %q, want the raw value", got)
	}

	moment := time.Date(2026, 11, 23, 9, 30, 0, 0, clock.Location())
	if got := fmtDateTime(moment.Unix()); got != "23 Nov 2026 09:30" {
		t.Errorf("fmtDateTime(unix) = %q, want 23 Nov 2026 09:30", got)
	}
	if got := fmtDateTime(moment); got != "23 Nov 2026 09:30" {
		t.Errorf("fmtDateTime(time.Time) = %q, want 23 Nov 2026 09:30", got)
	}
	if got := fmtDateTime("2026-11-23 09:30"); got != "23 Nov 2026 09:30" {
		t.Errorf("fmtDateTime(datetime string) = %q, want 23 Nov 2026 09:30", got)
	}
	if got := fmtDateTime(date); got != "23 Nov 2026" {
		t.Errorf("fmtDateTime(date string) = %q, want 23 Nov 2026", got)
	}
	if got := fmtDateTime(nil); got != "" {
		t.Errorf("fmtDateTime(nil) = %q, want empty", got)
	}

	tests := []struct {
		status   string
		label    string
		terminal bool
	}{
		{status: "applied", label: "Applied", terminal: false},
		{status: "interviewing", label: "Interviewing", terminal: false},
		{status: "rejected", label: "Rejected", terminal: true},
		{status: "ghosted", label: "Ghosted", terminal: true},
		{status: "unknown", label: "unknown", terminal: false},
	}
	for _, tt := range tests {
		if got := statusLabel(tt.status); got != tt.label {
			t.Errorf("statusLabel(%q) = %q, want %q", tt.status, got, tt.label)
		}
		if got := isTerminal(tt.status); got != tt.terminal {
			t.Errorf("isTerminal(%q) = %v, want %v", tt.status, got, tt.terminal)
		}
	}

	sources := []struct {
		source string
		label  string
	}{
		{source: "linkedin", label: "LinkedIn"},
		{source: "company_site", label: "Company site"},
		{source: "other", label: "Other"},
		{source: "unknown", label: "unknown"},
	}
	for _, tt := range sources {
		if got := sourceLabel(tt.source); got != tt.label {
			t.Errorf("sourceLabel(%q) = %q, want %q", tt.source, got, tt.label)
		}
	}

	events := []struct {
		eventType string
		label     string
	}{
		{eventType: "status_change", label: "Status"},
		{eventType: "note", label: "Note"},
		{eventType: "email_sent", label: "Email sent"},
		{eventType: "email_received", label: "Email received"},
		{eventType: "document_added", label: "Document"},
		{eventType: "custom", label: "custom"},
	}
	for _, tt := range events {
		if got := eventLabel(tt.eventType); got != tt.label {
			t.Errorf("eventLabel(%q) = %q, want %q", tt.eventType, got, tt.label)
		}
	}

	kinds := []struct {
		kind  string
		label string
	}{
		{kind: "job_ad", label: "Job ad"},
		{kind: "cv", label: "CV"},
		{kind: "cover_letter", label: "Cover letter"},
		{kind: "email", label: "Email"},
		{kind: "other", label: "Other"},
		{kind: "passport", label: "passport"},
	}
	for _, tt := range kinds {
		if got := documentKindLabel(tt.kind); got != tt.label {
			t.Errorf("documentKindLabel(%q) = %q, want %q", tt.kind, got, tt.label)
		}
	}

	sizes := []struct {
		size int64
		want string
	}{
		{size: 0, want: "0 B"},
		{size: 512, want: "512 B"},
		{size: 1536, want: "1.5 KB"},
		{size: 2 << 20, want: "2.0 MB"},
	}
	for _, tt := range sizes {
		if got := fileSize(tt.size); got != tt.want {
			t.Errorf("fileSize(%d) = %q, want %q", tt.size, got, tt.want)
		}
	}
}

func TestStaticAssetsEmbedded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "htmx.min.js", content: "htmx"},
		{name: "pico.min.css", content: "--pico-"},
		{name: "app.css", content: ".flash"},
		{name: "app.js", content: "htmx:confirm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := fs.ReadFile(webassets.FS, "static/"+tt.name)
			if err != nil {
				t.Fatalf("read static/%s: %v", tt.name, err)
			}
			if !strings.Contains(string(data), tt.content) {
				t.Errorf("static/%s does not contain %q", tt.name, tt.content)
			}
		})
	}
}
