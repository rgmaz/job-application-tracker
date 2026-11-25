package web

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/files"
	"github.com/rgmaz/job-application-tracker/internal/service"
	"github.com/rgmaz/job-application-tracker/internal/store"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
	"github.com/rgmaz/job-application-tracker/internal/timeutil"
)

type webClock struct {
	now time.Time
}

func (c *webClock) Now() time.Time { return c.now }

func (c *webClock) Today() string { return c.now.Format(timeutil.DateLayout) }

type webFixture struct {
	router   http.Handler
	svc      *service.ApplicationService
	docs     *service.DocumentService
	db       *sql.DB
	clock    *webClock
	filesDir string
}

func newWebFixture(t *testing.T) *webFixture {
	t.Helper()

	loc, err := time.LoadLocation("Africa/Harare")
	if err != nil {
		t.Fatalf("load Africa/Harare: %v", err)
	}

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

	clock := &webClock{now: time.Date(2026, 11, 23, 9, 30, 0, 0, loc)}
	svc := service.NewApplications(db, clock)

	filesDir := filepath.Join(dir, "files")
	fileStore, err := files.NewStore(filesDir, 1<<20)
	if err != nil {
		t.Fatalf("files.NewStore: %v", err)
	}
	docs := service.NewDocuments(db, clock, fileStore, slog.New(slog.NewTextHandler(io.Discard, nil)))

	renderer, err := NewRenderer(timeutil.New(loc))
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	return &webFixture{
		router:   NewRouter(svc, docs, renderer),
		svc:      svc,
		docs:     docs,
		db:       db,
		clock:    clock,
		filesDir: filesDir,
	}
}

func (f *webFixture) seed(t *testing.T, company, role, source, appliedOn, status string) storedb.Application {
	t.Helper()

	app, err := f.svc.Create(context.Background(), service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company:   company,
			Role:      role,
			Source:    source,
			AppliedOn: appliedOn,
		},
		Status: status,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", company, err)
	}
	return app
}

func (f *webFixture) request(t *testing.T, method, target string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *webFixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, http.MethodGet, target, nil, nil)
}

func (f *webFixture) post(t *testing.T, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return f.request(t, http.MethodPost, target, form, nil)
}

func (f *webFixture) count(t *testing.T) int {
	t.Helper()

	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM applications").Scan(&n); err != nil {
		t.Fatalf("count applications: %v", err)
	}
	return n
}

func createForm() url.Values {
	return url.Values{
		"company":    {"Acme"},
		"role":       {"Backend Engineer"},
		"source":     {"linkedin"},
		"applied_on": {"2026-11-20"},
	}
}

func TestApplicationsList(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-01", "applied")
	f.seed(t, "Globex", "Frontend Engineer", "referral", "2026-11-20", "interviewing")

	rec := f.get(t, "/applications")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Applications",
		"New application",
		"Acme",
		"Backend Engineer",
		"Globex",
		"Interviewing",
		"LinkedIn",
		`hx-get="/applications/rows"`,
		`id="application-rows"`,
		`name="status"`,
		`name="source"`,
		`name="q"`,
		`name="stale"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("list page does not contain %q", want)
		}
	}

	tests := []struct {
		name       string
		target     string
		want       []string
		notWant    []string
		wantStatus int
	}{
		{
			name:   "status filter",
			target: "/applications?status=applied",
			want:   []string{"Acme"},
			// The filter select still offers every status label.
			notWant: []string{">Globex<"},
		},
		{
			name:    "source filter",
			target:  "/applications?source=referral",
			want:    []string{"Globex"},
			notWant: []string{">Acme<"},
		},
		{
			name:    "text filter is case insensitive",
			target:  "/applications?q=FRONTEND",
			want:    []string{"Globex"},
			notWant: []string{">Acme<"},
		},
		{
			name:    "stale only",
			target:  "/applications?stale=1",
			want:    []string{"Acme"},
			notWant: []string{">Globex<"},
		},
		{
			name:       "invalid status",
			target:     "/applications?status=hired",
			want:       []string{"Choose a valid status."},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "invalid source",
			target:     "/applications?source=monster",
			want:       []string{"Choose a valid source."},
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.get(t, tt.target)
			wantStatus := tt.wantStatus
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			if rec.Code != wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(body, notWant) {
					t.Errorf("body unexpectedly contains %q", notWant)
				}
			}
		})
	}

	t.Run("htmx rows partial", func(t *testing.T) {
		rec := f.request(t, http.MethodGet, "/applications/rows?status=interviewing", nil, map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "<!doctype html>") {
			t.Error("rows partial rendered the full layout")
		}
		if !strings.Contains(body, `id="application-rows"`) || !strings.Contains(body, "Globex") {
			t.Errorf("rows partial = %q, want the matching rows", body)
		}
		if strings.Contains(body, ">Acme<") {
			t.Error("rows partial contains a row filtered out by status")
		}
	})
}

func TestApplicationNewForm(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.get(t, "/applications/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"New application",
		`action="/applications"`,
		`name="company"`,
		`name="role"`,
		`name="applied_on"`,
		`name="status"`,
		"Create application",
		"LinkedIn",
		"Company site",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form does not contain %q", want)
		}
	}
}

func TestApplicationCreate(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.post(t, "/applications", createForm())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/applications/") || !strings.Contains(location, "flash=created") {
		t.Fatalf("Location = %q, want /applications/{id}?flash=created", location)
	}

	rows, err := f.svc.List(context.Background(), service.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("applications = %d, want 1", len(rows))
	}
	if rows[0].Company != "Acme" || rows[0].Role != "Backend Engineer" || rows[0].Source != "linkedin" {
		t.Errorf("created application = %+v", rows[0])
	}

	rec = f.get(t, location)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Application created.") {
		t.Error("detail page does not show the created flash message")
	}

	t.Run("validation re-renders with 422", func(t *testing.T) {
		form := url.Values{"company": {" "}, "role": {""}, "applied_on": {"23/11/2026"}}
		rec := f.post(t, "/applications", form)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{
			"Company is required.",
			"Role is required.",
			"Use a valid date (YYYY-MM-DD).",
			`action="/applications"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("validation page does not contain %q", want)
			}
		}
		if got := f.count(t); got != 1 {
			t.Errorf("applications after invalid create = %d, want 1", got)
		}
	})
}

func TestApplicationDetail(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rec := f.get(t, "/applications/"+itoa(app.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Acme",
		"Backend Engineer",
		"Applied",
		"Timeline",
		"Status set to Applied",
		"/applications/" + itoa(app.ID) + "/edit",
		"Delete",
		`action="/applications/` + itoa(app.ID) + `/status"`,
		`action="/applications/` + itoa(app.ID) + `/events"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page does not contain %q", want)
		}
	}

	for _, target := range []string{
		"/applications/9999",
		"/applications/not-a-number",
	} {
		rec := f.get(t, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, rec.Code)
		}
	}
}

func TestApplicationEdit(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rec := f.get(t, "/applications/"+itoa(app.ID)+"/edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Edit application",
		`value="Acme"`,
		`value="Backend Engineer"`,
		`action="/applications/` + itoa(app.ID) + `"`,
		"Save changes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("edit form does not contain %q", want)
		}
	}
	if strings.Contains(body, `name="status"`) {
		t.Error("edit form should not offer a status selector; status has its own form")
	}

	form := url.Values{
		"company":    {"Acme Corp"},
		"role":       {"Senior Backend Engineer"},
		"source":     {"referral"},
		"applied_on": {"2026-11-05"},
		"notes":      {"Referred by a friend"},
	}
	rec = f.post(t, "/applications/"+itoa(app.ID), form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); !strings.Contains(got, "flash=updated") {
		t.Errorf("Location = %q, want flash=updated", got)
	}

	updated, err := f.svc.Get(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Company != "Acme Corp" || updated.Role != "Senior Backend Engineer" || updated.Source != "referral" {
		t.Errorf("updated = %+v", updated)
	}
	if updated.Notes == nil || *updated.Notes != "Referred by a friend" {
		t.Errorf("notes = %v, want the submitted note", updated.Notes)
	}

	t.Run("validation re-renders with 422", func(t *testing.T) {
		rec := f.post(t, "/applications/"+itoa(app.ID), url.Values{"company": {""}, "role": {""}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Company is required.") {
			t.Error("validation page does not show the field error")
		}
	})

	t.Run("missing application", func(t *testing.T) {
		if rec := f.get(t, "/applications/9999/edit"); rec.Code != http.StatusNotFound {
			t.Errorf("edit missing status = %d, want 404", rec.Code)
		}
		if rec := f.post(t, "/applications/9999", createForm()); rec.Code != http.StatusNotFound {
			t.Errorf("update missing status = %d, want 404", rec.Code)
		}
	})
}

func TestApplicationStatusChange(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rec := f.post(t, "/applications/"+itoa(app.ID)+"/status", url.Values{"status": {"screening"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); !strings.Contains(got, "flash=status") {
		t.Errorf("Location = %q, want flash=status", got)
	}

	updated, err := f.svc.Get(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != "screening" {
		t.Errorf("status = %q, want screening", updated.Status)
	}

	rec = f.get(t, "/applications/"+itoa(app.ID))
	if !strings.Contains(rec.Body.String(), "Applied → Screening") {
		t.Error("detail page does not show the status transition event")
	}

	t.Run("invalid status re-renders with 422", func(t *testing.T) {
		rec := f.post(t, "/applications/"+itoa(app.ID)+"/status", url.Values{"status": {"hired"}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Choose a valid status.") {
			t.Error("detail page does not show the status error")
		}
	})

	if rec := f.post(t, "/applications/9999/status", url.Values{"status": {"offer"}}); rec.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", rec.Code)
	}
}

func TestApplicationEvents(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	form := url.Values{"type": {"note"}, "body": {"Called the recruiter"}}
	rec := f.post(t, "/applications/"+itoa(app.ID)+"/events", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); !strings.Contains(got, "flash=note") {
		t.Errorf("Location = %q, want flash=note", got)
	}

	events, err := f.svc.ListEvents(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Body == nil || *events[0].Body != "Called the recruiter" {
		t.Errorf("newest event = %+v", events[0])
	}

	t.Run("htmx returns the timeline block", func(t *testing.T) {
		rec := f.request(t, http.MethodPost, "/applications/"+itoa(app.ID)+"/events",
			url.Values{"type": {"email_sent"}, "body": {"Sent a follow-up"}},
			map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "<!doctype html>") {
			t.Error("htmx response rendered the full layout")
		}
		for _, want := range []string{`id="timeline-block"`, "Sent a follow-up", "Email sent", `hx-post="/applications/` + itoa(app.ID) + `/events"`} {
			if !strings.Contains(body, want) {
				t.Errorf("timeline block does not contain %q", want)
			}
		}
	})

	t.Run("htmx validation keeps the form usable", func(t *testing.T) {
		rec := f.request(t, http.MethodPost, "/applications/"+itoa(app.ID)+"/events",
			url.Values{"type": {"note"}, "body": {"  "}},
			map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "<!doctype html>") {
			t.Error("htmx response rendered the full layout")
		}
		if !strings.Contains(body, "Enter a note or message.") {
			t.Error("timeline block does not show the body error")
		}
	})

	t.Run("plain validation re-renders with 422", func(t *testing.T) {
		rec := f.post(t, "/applications/"+itoa(app.ID)+"/events", url.Values{"type": {"bogus"}, "body": {"hi"}})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Choose a valid event type.") {
			t.Error("detail page does not show the event type error")
		}
	})

	if rec := f.post(t, "/applications/9999/events", url.Values{"type": {"note"}, "body": {"hi"}}); rec.Code != http.StatusNotFound {
		t.Errorf("missing event add status = %d, want 404", rec.Code)
	}
}

func TestApplicationDelete(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	app := f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-20", "applied")

	rec := f.post(t, "/applications/"+itoa(app.ID)+"/delete", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); !strings.Contains(got, "/applications?flash=deleted") {
		t.Errorf("Location = %q, want /applications?flash=deleted", got)
	}
	if got := f.count(t); got != 0 {
		t.Errorf("applications after delete = %d, want 0", got)
	}

	if rec := f.post(t, "/applications/9999/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing delete status = %d, want 404", rec.Code)
	}
}

func TestRootRedirect(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.get(t, "/")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", got)
	}
}

func TestStaticAssetServed(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.get(t, "/static/app.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != staticCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, staticCacheControl)
	}
	if !strings.Contains(rec.Body.String(), ".flash") {
		t.Error("app.css body does not contain the expected rule")
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
