package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rgmaz/job-application-tracker/internal/store"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

const (
	// testToday is the fixed reference date for staleness assertions.
	testToday = "2026-11-22"
	// testTimestamp is a stable unix timestamp for rows that only need to be
	// internally consistent.
	testTimestamp = int64(1_763_000_000)
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func currentVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()

	var version int64
	if err := db.QueryRow("SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	return version
}

// testStore is a migrated database plus the generated queries bound to it.
type testStore struct {
	db *sql.DB
	q  *storedb.Queries
}

func newTestStore(t *testing.T) *testStore {
	t.Helper()

	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	return &testStore{db: db, q: storedb.New(db)}
}

func (s *testStore) createApplication(t *testing.T, mutate func(*storedb.CreateApplicationParams)) storedb.Application {
	t.Helper()

	params := storedb.CreateApplicationParams{
		Company:         "Acme",
		Role:            "Engineer",
		Source:          "other",
		AppliedOn:       "2026-11-01",
		Status:          "applied",
		StatusChangedOn: "2026-11-01",
		StatusChangedAt: testTimestamp,
		CreatedAt:       testTimestamp,
		UpdatedAt:       testTimestamp,
	}
	if mutate != nil {
		mutate(&params)
	}

	app, err := s.q.CreateApplication(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateApplication(%q): %v", params.Company, err)
	}
	return app
}

func (s *testStore) listApplications(t *testing.T, mutate func(*storedb.ListApplicationsParams)) []storedb.ListApplicationsRow {
	t.Helper()

	params := storedb.ListApplicationsParams{Today: testToday, Limit: 100}
	if mutate != nil {
		mutate(&params)
	}

	rows, err := s.q.ListApplications(context.Background(), params)
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	return rows
}

func applicationIDs(rows []storedb.ListApplicationsRow) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func ptr[T any](v T) *T { return &v }

func TestOpenCreatesParentDirectory(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "data")
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("parent directory was not created: %v", err)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := store.Open(context.Background(), ""); err == nil {
		t.Fatal("store.Open(\"\") error = nil, want error")
	}
}

func TestOpenAppliesPragmas(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{name: "foreign_keys", query: "PRAGMA foreign_keys", want: 1},
		{name: "busy_timeout", query: "PRAGMA busy_timeout", want: 5000},
		{name: "synchronous", query: "PRAGMA synchronous", want: 1}, // NORMAL
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			if err := db.QueryRowContext(ctx, tt.query).Scan(&got); err != nil {
				t.Fatalf("%s: %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("%s = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestMigrate(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	if got := currentVersion(t, db); got != 2 {
		t.Fatalf("goose version = %d, want 2", got)
	}

	for _, table := range []string{
		"applications", "documents", "events", "settings",
		"status_thresholds", "goose_db_version",
	} {
		var name string
		err := db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}

	var thresholds int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM status_thresholds").Scan(&thresholds); err != nil {
		t.Fatalf("count thresholds: %v", err)
	}
	if thresholds != 4 {
		t.Errorf("seeded thresholds = %d, want 4", thresholds)
	}

	// A second run must be a no-op rather than an error or a re-apply.
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("second store.Migrate: %v", err)
	}
	if got := currentVersion(t, db); got != 2 {
		t.Errorf("goose version after second run = %d, want 2", got)
	}
}

func TestMigrateConcurrentDatabases(t *testing.T) {
	t.Parallel()

	const databases = 4

	dir := t.TempDir()
	paths := make([]string, databases)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("app-%d.db", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, databases)
	for i := range paths {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			db, err := store.Open(context.Background(), paths[i])
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = db.Close() }()

			errs[i] = store.Migrate(context.Background(), db)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("database %d: %v", i, err)
		}
	}
}

func TestApplicationCRUD(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	created := s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Company = "Acme"
		p.Role = "Backend Engineer"
		p.Location = ptr("Harare")
		p.JobUrl = ptr("https://example.com/jobs/1")
		p.Source = "linkedin"
		p.Notes = ptr("first contact")
	})
	if created.ID == 0 {
		t.Fatal("CreateApplication returned zero ID")
	}

	got, err := s.q.GetApplication(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if !reflect.DeepEqual(got, created) {
		t.Errorf("GetApplication = %+v, want %+v", got, created)
	}

	updated, err := s.q.UpdateApplication(ctx, storedb.UpdateApplicationParams{
		Company:   "Acme Corp",
		Role:      "Senior Backend Engineer",
		Source:    "referral",
		AppliedOn: "2026-11-02",
		Notes:     ptr("second contact"),
		UpdatedAt: created.UpdatedAt + 100,
		ID:        created.ID,
	})
	if err != nil {
		t.Fatalf("UpdateApplication: %v", err)
	}
	if updated.Company != "Acme Corp" || updated.Role != "Senior Backend Engineer" {
		t.Errorf("updated company/role = %q/%q, want Acme Corp/Senior Backend Engineer", updated.Company, updated.Role)
	}
	if updated.Location != nil || updated.JobUrl != nil {
		t.Errorf("nullable fields were not cleared: location=%v job_url=%v", updated.Location, updated.JobUrl)
	}
	if updated.Status != "applied" || updated.StatusChangedOn != created.StatusChangedOn {
		t.Errorf("UpdateApplication changed status fields: status=%q changed_on=%q", updated.Status, updated.StatusChangedOn)
	}
	if updated.UpdatedAt != created.UpdatedAt+100 {
		t.Errorf("updated_at = %d, want %d", updated.UpdatedAt, created.UpdatedAt+100)
	}

	statusChanged, err := s.q.UpdateStatus(ctx, storedb.UpdateStatusParams{
		Status:          "interviewing",
		StatusChangedOn: "2026-11-20",
		StatusChangedAt: created.StatusChangedAt + 200,
		UpdatedAt:       created.UpdatedAt + 200,
		ID:              created.ID,
	})
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if statusChanged.Status != "interviewing" || statusChanged.StatusChangedOn != "2026-11-20" {
		t.Errorf("status = %q on %q, want interviewing on 2026-11-20", statusChanged.Status, statusChanged.StatusChangedOn)
	}
	if statusChanged.Company != "Acme Corp" {
		t.Errorf("UpdateStatus changed company = %q, want Acme Corp", statusChanged.Company)
	}

	if _, err := s.q.UpdateApplication(ctx, storedb.UpdateApplicationParams{ID: created.ID + 1000}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateApplication(missing) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := s.q.UpdateStatus(ctx, storedb.UpdateStatusParams{ID: created.ID + 1000}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateStatus(missing) error = %v, want sql.ErrNoRows", err)
	}

	deleted, err := s.q.DeleteApplication(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteApplication: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteApplication rows = %d, want 1", deleted)
	}
	if _, err := s.q.GetApplication(ctx, created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetApplication after delete error = %v, want sql.ErrNoRows", err)
	}

	deleted, err = s.q.DeleteApplication(ctx, created.ID)
	if err != nil {
		t.Fatalf("second DeleteApplication: %v", err)
	}
	if deleted != 0 {
		t.Errorf("second DeleteApplication rows = %d, want 0", deleted)
	}
}

func TestListApplicationsFilters(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	acme := s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Company = "Acme"
		p.Role = "Backend Engineer"
		p.Source = "linkedin"
		p.Notes = ptr("Remote friendly")
		p.StatusChangedOn = "2026-11-01" // stale under the 14-day threshold
		p.CreatedAt = testTimestamp + 3
	})
	globex := s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Company = "Globex"
		p.Role = "Frontend Engineer"
		p.Source = "referral"
		p.Status = "interviewing"
		p.StatusChangedOn = "2026-11-20"
		p.CreatedAt = testTimestamp + 2
	})
	initech := s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Company = "Initech"
		p.Role = "Data Analyst"
		p.Source = "other"
		p.Notes = ptr("Needs SQLite experience")
		p.Status = "rejected"
		p.StatusChangedOn = "2020-01-01"
		p.CreatedAt = testTimestamp + 1
	})

	tests := []struct {
		name   string
		mutate func(*storedb.ListApplicationsParams)
		want   []int64
	}{
		{
			name: "all ordered by created_at desc",
			want: []int64{acme.ID, globex.ID, initech.ID},
		},
		{
			name:   "status filter",
			mutate: func(p *storedb.ListApplicationsParams) { p.StatusFilter = "interviewing" },
			want:   []int64{globex.ID},
		},
		{
			name:   "source filter",
			mutate: func(p *storedb.ListApplicationsParams) { p.SourceFilter = "linkedin" },
			want:   []int64{acme.ID},
		},
		{
			name:   "query matches company and role",
			mutate: func(p *storedb.ListApplicationsParams) { p.Q = "engineer" },
			want:   []int64{acme.ID, globex.ID},
		},
		{
			name:   "query matches notes case-insensitively",
			mutate: func(p *storedb.ListApplicationsParams) { p.Q = "sqlite" },
			want:   []int64{initech.ID},
		},
		{
			name:   "query matches company case-insensitively",
			mutate: func(p *storedb.ListApplicationsParams) { p.Q = "ACME" },
			want:   []int64{acme.ID},
		},
		{
			name:   "query with no match",
			mutate: func(p *storedb.ListApplicationsParams) { p.Q = "nonexistent" },
			want:   []int64{},
		},
		{
			name:   "stale only",
			mutate: func(p *storedb.ListApplicationsParams) { p.StaleOnly = 1 },
			want:   []int64{acme.ID},
		},
		{
			name:   "limit",
			mutate: func(p *storedb.ListApplicationsParams) { p.Limit = 2 },
			want:   []int64{acme.ID, globex.ID},
		},
		{
			name: "limit with offset",
			mutate: func(p *storedb.ListApplicationsParams) {
				p.Limit = 2
				p.Offset = 2
			},
			want: []int64{initech.ID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := s.listApplications(t, tt.mutate)
			if got := applicationIDs(rows); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListApplicationsStaleness(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	create := func(company, status, changedOn string, followUpOn *string) storedb.Application {
		return s.createApplication(t, func(p *storedb.CreateApplicationParams) {
			p.Company = company
			p.Status = status
			p.StatusChangedOn = changedOn
			p.FollowUpOn = followUpOn
		})
	}

	// applied threshold is 14 days, offer is 3 days.
	boundary := create("Boundary", "applied", "2026-11-08", nil) // +14d == today
	fresh := create("Fresh", "applied", "2026-11-09", nil)       // +14d == tomorrow
	overriddenFuture := create("OverriddenFuture", "applied", "2026-10-01", ptr("2026-12-01"))
	overriddenToday := create("OverriddenToday", "applied", "2026-11-20", ptr(testToday))
	terminal := create("Terminal", "rejected", "2020-01-01", nil)
	terminalFollowUp := create("TerminalFollowUp", "rejected", "2020-01-01", ptr("2026-01-01"))
	offer := create("Offer", "offer", "2026-11-19", nil) // +3d == today

	want := map[int64]struct {
		dueOn *string
		stale bool
	}{
		boundary.ID:         {dueOn: ptr("2026-11-22"), stale: true},
		fresh.ID:            {dueOn: ptr("2026-11-23"), stale: false},
		overriddenFuture.ID: {dueOn: ptr("2026-12-01"), stale: false},
		overriddenToday.ID:  {dueOn: ptr("2026-11-22"), stale: true},
		terminal.ID:         {dueOn: nil, stale: false},
		terminalFollowUp.ID: {dueOn: ptr("2026-01-01"), stale: false},
		offer.ID:            {dueOn: ptr("2026-11-22"), stale: true},
	}

	rows := s.listApplications(t, nil)
	if len(rows) != len(want) {
		t.Fatalf("ListApplications returned %d rows, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		expect, ok := want[row.ID]
		if !ok {
			t.Errorf("unexpected application ID %d", row.ID)
			continue
		}
		if !reflect.DeepEqual(row.DueOn, expect.dueOn) {
			t.Errorf("%s due_on = %v, want %v", row.Company, deref(row.DueOn), deref(expect.dueOn))
		}
		if got := row.IsStale == 1; got != expect.stale {
			t.Errorf("%s is_stale = %v, want %v", row.Company, got, expect.stale)
		}
	}
}

func TestDeleteApplicationCascades(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	app := s.createApplication(t, nil)

	for _, body := range []string{"applied", "phone screen"} {
		if _, err := s.q.CreateEvent(ctx, storedb.CreateEventParams{
			ApplicationID: app.ID,
			Type:          "status_change",
			Body:          ptr(body),
			OccurredAt:    testTimestamp,
			CreatedAt:     testTimestamp,
		}); err != nil {
			t.Fatalf("CreateEvent(%q): %v", body, err)
		}
	}
	for _, name := range []string{"cv.pdf", "job-ad.html"} {
		if _, err := s.q.CreateDocument(ctx, storedb.CreateDocumentParams{
			ApplicationID: app.ID,
			Kind:          "cv",
			OriginalName:  name,
			Mime:          "application/pdf",
			Size:          1024,
			Sha256:        "deadbeef",
			StorageKey:    "key-" + name,
			UploadedAt:    testTimestamp,
		}); err != nil {
			t.Fatalf("CreateDocument(%q): %v", name, err)
		}
	}

	if events, err := s.q.ListEvents(ctx, app.ID); err != nil || len(events) != 2 {
		t.Fatalf("events before delete = %d, %v; want 2, nil", len(events), err)
	}
	if docs, err := s.q.ListDocuments(ctx, app.ID); err != nil || len(docs) != 2 {
		t.Fatalf("documents before delete = %d, %v; want 2, nil", len(docs), err)
	}

	deleted, err := s.q.DeleteApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("DeleteApplication: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteApplication rows = %d, want 1", deleted)
	}

	for _, table := range []string{"events", "documents"} {
		var count int
		if err := s.db.QueryRowContext(ctx,
			"SELECT count(*) FROM "+table+" WHERE application_id = ?", app.ID,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s rows after delete = %d, want 0", table, count)
		}
	}

	// Cascades only work because the connection enables foreign keys.
	_, err = s.q.CreateEvent(ctx, storedb.CreateEventParams{
		ApplicationID: app.ID + 1000,
		Type:          "note",
		OccurredAt:    testTimestamp,
		CreatedAt:     testTimestamp,
	})
	if err == nil {
		t.Fatal("CreateEvent for a missing application: error = nil, want foreign key violation")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Errorf("CreateEvent error = %v, want a foreign key violation", err)
	}
}

func TestDashboardCounts(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Status = "applied"
		p.StatusChangedOn = "2026-11-01" // stale
	})
	s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Status = "applied"
		p.StatusChangedOn = "2026-11-20"
	})
	s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Status = "screening"
		p.StatusChangedOn = "2026-11-20"
	})
	s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Status = "interviewing"
		p.StatusChangedOn = "2026-11-20"
	})
	s.createApplication(t, func(p *storedb.CreateApplicationParams) {
		p.Status = "offer"
		p.StatusChangedOn = "2026-11-18" // stale under the 3-day threshold
	})
	for _, status := range []string{"rejected", "accepted", "withdrawn", "ghosted"} {
		s.createApplication(t, func(p *storedb.CreateApplicationParams) {
			p.Status = status
			p.StatusChangedOn = "2020-01-01" // terminal statuses never go stale
		})
	}

	got, err := s.q.DashboardCounts(context.Background(), testToday)
	if err != nil {
		t.Fatalf("DashboardCounts: %v", err)
	}
	want := storedb.DashboardCountsRow{
		Total:        9,
		Active:       5,
		Interviewing: 1,
		Offers:       1,
		Stale:        2,
	}
	if got != want {
		t.Errorf("DashboardCounts = %+v, want %+v", got, want)
	}
}

func TestDashboardCountsEmpty(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	got, err := s.q.DashboardCounts(context.Background(), testToday)
	if err != nil {
		t.Fatalf("DashboardCounts: %v", err)
	}
	if got != (storedb.DashboardCountsRow{}) {
		t.Errorf("DashboardCounts on empty database = %+v, want zero value", got)
	}
}

func TestSettings(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.q.GetSetting(ctx, "timezone"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSetting(missing) error = %v, want sql.ErrNoRows", err)
	}

	for _, value := range []string{"Africa/Harare", "UTC"} {
		if err := s.q.UpsertSetting(ctx, storedb.UpsertSettingParams{Key: "timezone", Value: value}); err != nil {
			t.Fatalf("UpsertSetting(timezone=%q): %v", value, err)
		}
	}
	if err := s.q.UpsertSetting(ctx, storedb.UpsertSettingParams{Key: "password_hash", Value: "hash"}); err != nil {
		t.Fatalf("UpsertSetting(password_hash): %v", err)
	}

	value, err := s.q.GetSetting(ctx, "timezone")
	if err != nil {
		t.Fatalf("GetSetting(timezone): %v", err)
	}
	if value != "UTC" {
		t.Errorf("timezone = %q, want UTC", value)
	}

	settings, err := s.q.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	wantSettings := []storedb.Setting{
		{Key: "password_hash", Value: "hash"},
		{Key: "timezone", Value: "UTC"},
	}
	if !reflect.DeepEqual(settings, wantSettings) {
		t.Errorf("ListSettings = %+v, want %+v", settings, wantSettings)
	}

	thresholds, err := s.q.ListThresholds(ctx)
	if err != nil {
		t.Fatalf("ListThresholds: %v", err)
	}
	wantThresholds := []storedb.StatusThreshold{
		{Status: "applied", Days: 14},
		{Status: "interviewing", Days: 7},
		{Status: "screening", Days: 7},
		{Status: "offer", Days: 3},
	}
	if !reflect.DeepEqual(thresholds, wantThresholds) {
		t.Errorf("ListThresholds = %+v, want %+v", thresholds, wantThresholds)
	}

	if err := s.q.UpsertThreshold(ctx, storedb.UpsertThresholdParams{Status: "applied", Days: 21}); err != nil {
		t.Fatalf("UpsertThreshold: %v", err)
	}
	thresholds, err = s.q.ListThresholds(ctx)
	if err != nil {
		t.Fatalf("ListThresholds after update: %v", err)
	}
	if len(thresholds) != 4 || thresholds[0].Status != "applied" || thresholds[0].Days != 21 {
		t.Errorf("thresholds after update = %+v, want applied=21 first", thresholds)
	}
}

func TestEvents(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	app := s.createApplication(t, nil)
	create := func(occurredAt int64) storedb.Event {
		t.Helper()
		event, err := s.q.CreateEvent(ctx, storedb.CreateEventParams{
			ApplicationID: app.ID,
			Type:          "note",
			Body:          ptr("event"),
			OccurredAt:    occurredAt,
			CreatedAt:     testTimestamp,
		})
		if err != nil {
			t.Fatalf("CreateEvent: %v", err)
		}
		return event
	}

	first := create(100)
	second := create(200)
	third := create(200)

	events, err := s.q.ListEvents(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	wantIDs := []int64{third.ID, second.ID, first.ID}
	gotIDs := make([]int64, len(events))
	for i, event := range events {
		gotIDs[i] = event.ID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("event IDs = %v, want %v", gotIDs, wantIDs)
	}

	deleted, err := s.q.DeleteEvent(ctx, second.ID)
	if err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteEvent rows = %d, want 1", deleted)
	}
	deleted, err = s.q.DeleteEvent(ctx, second.ID)
	if err != nil {
		t.Fatalf("second DeleteEvent: %v", err)
	}
	if deleted != 0 {
		t.Errorf("second DeleteEvent rows = %d, want 0", deleted)
	}
}

func TestDocuments(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	app := s.createApplication(t, nil)
	create := func(name string, uploadedAt int64) storedb.Document {
		t.Helper()
		doc, err := s.q.CreateDocument(ctx, storedb.CreateDocumentParams{
			ApplicationID: app.ID,
			Kind:          "cv",
			OriginalName:  name,
			Mime:          "application/pdf",
			Size:          2048,
			Sha256:        "deadbeef",
			StorageKey:    "key-" + name,
			UploadedAt:    uploadedAt,
		})
		if err != nil {
			t.Fatalf("CreateDocument(%q): %v", name, err)
		}
		return doc
	}

	older := create("older.pdf", 100)
	newer := create("newer.pdf", 200)

	docs, err := s.q.ListDocuments(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 2 || docs[0].ID != newer.ID || docs[1].ID != older.ID {
		t.Errorf("document order = %+v, want newer then older", docs)
	}

	got, err := s.q.GetDocument(ctx, older.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if !reflect.DeepEqual(got, older) {
		t.Errorf("GetDocument = %+v, want %+v", got, older)
	}

	deleted, err := s.q.DeleteDocument(ctx, older.ID)
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteDocument rows = %d, want 1", deleted)
	}
	if _, err := s.q.GetDocument(ctx, older.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDocument after delete error = %v, want sql.ErrNoRows", err)
	}
	deleted, err = s.q.DeleteDocument(ctx, older.ID)
	if err != nil {
		t.Fatalf("second DeleteDocument: %v", err)
	}
	if deleted != 0 {
		t.Errorf("second DeleteDocument rows = %d, want 0", deleted)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
