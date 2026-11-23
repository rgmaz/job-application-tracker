package service_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/model"
	"github.com/rgmaz/job-application-tracker/internal/service"
	"github.com/rgmaz/job-application-tracker/internal/store"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
	"github.com/rgmaz/job-application-tracker/internal/timeutil"
)

func harare(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation("Africa/Harare")
	if err != nil {
		t.Fatalf("load Africa/Harare: %v", err)
	}
	return loc
}

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time { return c.now }

func (c *fixedClock) Today() string { return c.now.Format(timeutil.DateLayout) }

type fixture struct {
	svc   *service.ApplicationService
	q     *storedb.Queries
	clock *fixedClock
}

func newFixture(t *testing.T, now time.Time) *fixture {
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

	clock := &fixedClock{now: now}
	return &fixture{
		svc:   service.NewApplications(db, clock),
		q:     storedb.New(db),
		clock: clock,
	}
}

func (f *fixture) create(t *testing.T, in service.CreateApplicationInput) storedb.Application {
	t.Helper()

	app, err := f.svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return app
}

func validInput() service.ApplicationFields {
	return service.ApplicationFields{
		Company: "Acme",
		Role:    "Backend Engineer",
	}
}

func TestApplicationCreateDefaults(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	app := f.create(t, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company: "  Acme  ",
			Role:    " Backend Engineer ",
		},
	})

	if app.Company != "Acme" || app.Role != "Backend Engineer" {
		t.Errorf("company/role = %q/%q, want trimmed Acme/Backend Engineer", app.Company, app.Role)
	}
	if app.AppliedOn != "2026-11-23" {
		t.Errorf("applied_on = %q, want 2026-11-23", app.AppliedOn)
	}
	if app.Status != string(model.StatusApplied) {
		t.Errorf("status = %q, want applied", app.Status)
	}
	if app.StatusChangedOn != app.AppliedOn {
		t.Errorf("status_changed_on = %q, want applied_on %q", app.StatusChangedOn, app.AppliedOn)
	}
	if app.StatusChangedAt != now.Unix() {
		t.Errorf("status_changed_at = %d, want %d", app.StatusChangedAt, now.Unix())
	}
	if app.CreatedAt != now.Unix() || app.UpdatedAt != now.Unix() {
		t.Errorf("created/updated = %d/%d, want %d", app.CreatedAt, app.UpdatedAt, now.Unix())
	}
	if app.Source != string(model.SourceOther) {
		t.Errorf("source = %q, want other", app.Source)
	}
	for name, value := range map[string]*string{
		"location":      app.Location,
		"job_url":       app.JobUrl,
		"follow_up_on":  app.FollowUpOn,
		"contact_name":  app.ContactName,
		"contact_email": app.ContactEmail,
		"email_subject": app.EmailSubject,
		"email_link":    app.EmailLink,
		"notes":         app.Notes,
	} {
		if value != nil {
			t.Errorf("%s = %q, want nil", name, *value)
		}
	}

	events, err := f.q.ListEvents(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Type != "status_change" {
		t.Errorf("event type = %q, want status_change", event.Type)
	}
	if event.Body == nil || *event.Body != "Status set to Applied" {
		t.Errorf("event body = %v, want %q", event.Body, "Status set to Applied")
	}
	if event.OccurredAt != now.Unix() || event.CreatedAt != now.Unix() {
		t.Errorf("event occurred/created = %d/%d, want %d", event.OccurredAt, event.CreatedAt, now.Unix())
	}
}

func TestApplicationCreateOptionalFields(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)

	app := f.create(t, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company:      "Acme",
			Role:         "Backend Engineer",
			Location:     "Harare",
			JobURL:       "https://example.com/jobs/1",
			Source:       "linkedin",
			AppliedOn:    "2026-11-01",
			FollowUpOn:   "2026-12-01",
			ContactName:  "Jane Doe",
			ContactEmail: "jane@example.com",
			EmailSubject: "Application received",
			EmailLink:    "https://mail.example.com/1",
			Notes:        "Referred by a friend",
		},
		Status: "screening",
	})

	if app.Status != "screening" {
		t.Errorf("status = %q, want screening", app.Status)
	}
	if app.AppliedOn != "2026-11-01" || app.StatusChangedOn != "2026-11-01" {
		t.Errorf("applied_on/status_changed_on = %q/%q, want 2026-11-01", app.AppliedOn, app.StatusChangedOn)
	}
	if app.Source != "linkedin" {
		t.Errorf("source = %q, want linkedin", app.Source)
	}

	wantStrings := map[string]struct {
		got  *string
		want string
	}{
		"location":      {app.Location, "Harare"},
		"job_url":       {app.JobUrl, "https://example.com/jobs/1"},
		"follow_up_on":  {app.FollowUpOn, "2026-12-01"},
		"contact_name":  {app.ContactName, "Jane Doe"},
		"contact_email": {app.ContactEmail, "jane@example.com"},
		"email_subject": {app.EmailSubject, "Application received"},
		"email_link":    {app.EmailLink, "https://mail.example.com/1"},
		"notes":         {app.Notes, "Referred by a friend"},
	}
	for name, tt := range wantStrings {
		if tt.got == nil {
			t.Errorf("%s = nil, want %q", name, tt.want)
			continue
		}
		if *tt.got != tt.want {
			t.Errorf("%s = %q, want %q", name, *tt.got, tt.want)
		}
	}

	events, err := f.q.ListEvents(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Body == nil || *events[0].Body != "Status set to Screening" {
		t.Errorf("events = %+v, want one %q event", events, "Status set to Screening")
	}
}

func TestApplicationCreateValidation(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	tests := []struct {
		name       string
		in         service.CreateApplicationInput
		wantFields []string
	}{
		{
			name: "missing required fields",
			in: service.CreateApplicationInput{
				ApplicationFields: service.ApplicationFields{Company: " ", Role: ""},
			},
			wantFields: []string{"company", "role"},
		},
		{
			name: "invalid source",
			in: service.CreateApplicationInput{
				ApplicationFields: service.ApplicationFields{
					Company: "Acme", Role: "Engineer", Source: "monster",
				},
			},
			wantFields: []string{"source"},
		},
		{
			name: "invalid status",
			in: service.CreateApplicationInput{
				ApplicationFields: service.ApplicationFields{Company: "Acme", Role: "Engineer"},
				Status:            "hired",
			},
			wantFields: []string{"status"},
		},
		{
			name: "invalid applied date",
			in: service.CreateApplicationInput{
				ApplicationFields: service.ApplicationFields{
					Company: "Acme", Role: "Engineer", AppliedOn: "23/11/2026",
				},
			},
			wantFields: []string{"applied_on"},
		},
		{
			name: "invalid follow-up date",
			in: service.CreateApplicationInput{
				ApplicationFields: service.ApplicationFields{
					Company: "Acme", Role: "Engineer", FollowUpOn: "2026-02-30",
				},
			},
			wantFields: []string{"follow_up_on"},
		},
		{
			name: "multiple errors",
			in: service.CreateApplicationInput{
				ApplicationFields: service.ApplicationFields{
					Company: "", Role: "", Source: "bad", AppliedOn: "nope", FollowUpOn: "nope",
				},
				Status: "bad",
			},
			wantFields: []string{"applied_on", "company", "follow_up_on", "role", "source", "status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Create(ctx, tt.in)
			fields := validationFields(t, err)
			if got := sortedKeys(fields); !reflect.DeepEqual(got, tt.wantFields) {
				t.Errorf("fields = %v, want %v", got, tt.wantFields)
			}
			for field, message := range fields {
				if message == "" {
					t.Errorf("field %q has an empty message", field)
				}
			}
		})
	}

	rows, err := f.svc.List(ctx, service.ListFilter{})
	if err != nil {
		t.Fatalf("List after validation failures: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("applications after validation failures = %d, want 0", len(rows))
	}
}

func TestApplicationGet(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	created := f.create(t, service.CreateApplicationInput{ApplicationFields: validInput()})

	got, err := f.svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, created) {
		t.Errorf("Get = %+v, want %+v", got, created)
	}

	if _, err := f.svc.Get(ctx, created.ID+1000); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get(missing) error = %v, want sql.ErrNoRows", err)
	}
}

func TestApplicationUpdate(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	created := f.create(t, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company:     "Acme",
			Role:        "Backend Engineer",
			Location:    "Harare",
			JobURL:      "https://example.com/jobs/1",
			Source:      "linkedin",
			FollowUpOn:  "2026-12-01",
			ContactName: "Jane Doe",
			Notes:       "first contact",
		},
	})

	f.clock.now = now.Add(2 * time.Hour)

	updated, err := f.svc.Update(ctx, created.ID, service.ApplicationFields{
		Company:   "Acme Corp",
		Role:      "Senior Backend Engineer",
		Source:    "referral",
		AppliedOn: "2026-11-05",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Company != "Acme Corp" || updated.Role != "Senior Backend Engineer" {
		t.Errorf("company/role = %q/%q, want Acme Corp/Senior Backend Engineer", updated.Company, updated.Role)
	}
	if updated.Source != "referral" || updated.AppliedOn != "2026-11-05" {
		t.Errorf("source/applied_on = %q/%q, want referral/2026-11-05", updated.Source, updated.AppliedOn)
	}
	if updated.Status != created.Status || updated.StatusChangedOn != created.StatusChangedOn {
		t.Errorf("Update changed status fields: status=%q changed_on=%q", updated.Status, updated.StatusChangedOn)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Errorf("created_at = %d, want %d", updated.CreatedAt, created.CreatedAt)
	}
	if updated.UpdatedAt != f.clock.now.Unix() {
		t.Errorf("updated_at = %d, want %d", updated.UpdatedAt, f.clock.now.Unix())
	}
	for name, value := range map[string]*string{
		"location":      updated.Location,
		"job_url":       updated.JobUrl,
		"follow_up_on":  updated.FollowUpOn,
		"contact_name":  updated.ContactName,
		"contact_email": updated.ContactEmail,
		"email_subject": updated.EmailSubject,
		"email_link":    updated.EmailLink,
		"notes":         updated.Notes,
	} {
		if value != nil {
			t.Errorf("%s = %q, want nil after clearing", name, *value)
		}
	}

	if _, err := f.svc.Update(ctx, created.ID+1000, validInput()); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Update(missing) error = %v, want sql.ErrNoRows", err)
	}

	_, err = f.svc.Update(ctx, created.ID, service.ApplicationFields{Company: "", Role: ""})
	if fields := validationFields(t, err); len(fields) != 2 {
		t.Errorf("Update validation fields = %v, want company and role", fields)
	}
}

func TestApplicationChangeStatus(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	created := f.create(t, service.CreateApplicationInput{ApplicationFields: validInput()})

	f.clock.now = now.Add(24 * time.Hour)

	changed, err := f.svc.ChangeStatus(ctx, created.ID, "screening")
	if err != nil {
		t.Fatalf("ChangeStatus: %v", err)
	}
	if changed.Status != "screening" {
		t.Errorf("status = %q, want screening", changed.Status)
	}
	if changed.StatusChangedOn != "2026-11-24" {
		t.Errorf("status_changed_on = %q, want 2026-11-24", changed.StatusChangedOn)
	}
	if changed.StatusChangedAt != f.clock.now.Unix() || changed.UpdatedAt != f.clock.now.Unix() {
		t.Errorf("status_changed_at/updated_at = %d/%d, want %d",
			changed.StatusChangedAt, changed.UpdatedAt, f.clock.now.Unix())
	}
	if changed.AppliedOn != created.AppliedOn || changed.Company != created.Company {
		t.Errorf("ChangeStatus changed unrelated fields: %+v", changed)
	}

	events, err := f.q.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Type != "status_change" || events[0].Body == nil || *events[0].Body != "Applied → Screening" {
		t.Errorf("newest event = %+v, want status_change %q", events[0], "Applied → Screening")
	}
	if events[0].OccurredAt != f.clock.now.Unix() {
		t.Errorf("event occurred_at = %d, want %d", events[0].OccurredAt, f.clock.now.Unix())
	}

	// Re-applying the current status is a no-op: no reset and no extra event.
	same, err := f.svc.ChangeStatus(ctx, created.ID, "screening")
	if err != nil {
		t.Fatalf("ChangeStatus(same): %v", err)
	}
	if same.UpdatedAt != changed.UpdatedAt || same.StatusChangedOn != changed.StatusChangedOn {
		t.Errorf("same-status change modified timestamps: %+v", same)
	}
	events, err = f.q.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents after same status: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events after same status = %d, want 2", len(events))
	}

	f.clock.now = f.clock.now.Add(time.Hour)
	if _, err := f.svc.ChangeStatus(ctx, created.ID, "hired"); err == nil {
		t.Fatal("ChangeStatus(invalid) error = nil, want validation error")
	} else if fields := validationFields(t, err); len(fields) != 1 || fields["status"] == "" {
		t.Errorf("validation fields = %v, want status", fields)
	}
	events, err = f.q.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents after invalid status: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events after invalid status = %d, want 2", len(events))
	}

	if _, err := f.svc.ChangeStatus(ctx, created.ID+1000, "offer"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ChangeStatus(missing) error = %v, want sql.ErrNoRows", err)
	}
}

func TestApplicationDelete(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	app := f.create(t, service.CreateApplicationInput{ApplicationFields: validInput()})

	for _, key := range []string{"key-a", "key-b"} {
		if _, err := f.q.CreateDocument(ctx, storedb.CreateDocumentParams{
			ApplicationID: app.ID,
			Kind:          "cv",
			OriginalName:  key + ".pdf",
			Mime:          "application/pdf",
			Size:          1024,
			Sha256:        "deadbeef",
			StorageKey:    key,
			UploadedAt:    now.Unix(),
		}); err != nil {
			t.Fatalf("CreateDocument(%q): %v", key, err)
		}
	}

	keys, err := f.svc.Delete(ctx, app.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	slices.Sort(keys)
	wantKeys := []string{"key-a", "key-b"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("storage keys = %v, want %v", keys, wantKeys)
	}

	if _, err := f.svc.Get(ctx, app.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get after delete error = %v, want sql.ErrNoRows", err)
	}
	if events, err := f.q.ListEvents(ctx, app.ID); err != nil || len(events) != 0 {
		t.Errorf("events after delete = %d, %v; want 0, nil", len(events), err)
	}
	if docs, err := f.q.ListDocuments(ctx, app.ID); err != nil || len(docs) != 0 {
		t.Errorf("documents after delete = %d, %v; want 0, nil", len(docs), err)
	}

	if keys, err := f.svc.Delete(ctx, app.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Delete(missing) error = %v, want sql.ErrNoRows", err)
	} else if len(keys) != 0 {
		t.Errorf("Delete(missing) keys = %v, want none", keys)
	}
}

func TestApplicationList(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	stale := f.create(t, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company: "Acme", Role: "Backend Engineer", Source: "linkedin", AppliedOn: "2026-11-01",
		},
	})
	fresh := f.create(t, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company: "Globex", Role: "Frontend Engineer", Source: "referral", AppliedOn: "2026-11-20",
		},
		Status: "interviewing",
	})
	terminal := f.create(t, service.CreateApplicationInput{
		ApplicationFields: service.ApplicationFields{
			Company: "Initech", Role: "Data Analyst", Source: "other", AppliedOn: "2020-01-01",
		},
		Status: "rejected",
	})

	tests := []struct {
		name    string
		filter  service.ListFilter
		wantIDs []int64
	}{
		{
			name:    "default",
			wantIDs: []int64{terminal.ID, fresh.ID, stale.ID},
		},
		{
			name:    "status filter",
			filter:  service.ListFilter{Status: "interviewing"},
			wantIDs: []int64{fresh.ID},
		},
		{
			name:    "source filter",
			filter:  service.ListFilter{Source: "linkedin"},
			wantIDs: []int64{stale.ID},
		},
		{
			name:    "query filter",
			filter:  service.ListFilter{Query: "engineer"},
			wantIDs: []int64{fresh.ID, stale.ID},
		},
		{
			name:    "stale only",
			filter:  service.ListFilter{StaleOnly: true},
			wantIDs: []int64{stale.ID},
		},
		{
			name:    "pagination",
			filter:  service.ListFilter{Limit: 1, Offset: 1},
			wantIDs: []int64{fresh.ID},
		},
		{
			name:    "whitespace query is ignored",
			filter:  service.ListFilter{Query: "   "},
			wantIDs: []int64{terminal.ID, fresh.ID, stale.ID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := f.svc.List(ctx, tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			ids := make([]int64, len(rows))
			for i, row := range rows {
				ids[i] = row.ID
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Errorf("IDs = %v, want %v", ids, tt.wantIDs)
			}
		})
	}

	if _, err := f.svc.List(ctx, service.ListFilter{Status: "hired"}); err == nil {
		t.Error("List(invalid status) error = nil, want validation error")
	} else if fields := validationFields(t, err); fields["status"] == "" {
		t.Errorf("validation fields = %v, want status", fields)
	}
	if _, err := f.svc.List(ctx, service.ListFilter{Source: "monster"}); err == nil {
		t.Error("List(invalid source) error = nil, want validation error")
	} else if fields := validationFields(t, err); fields["source"] == "" {
		t.Errorf("validation fields = %v, want source", fields)
	}
}

func TestApplicationDashboard(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	now := time.Date(2026, 11, 23, 9, 30, 0, 0, loc)
	f := newFixture(t, now)
	ctx := context.Background()

	create := func(status, appliedOn string) {
		t.Helper()
		f.create(t, service.CreateApplicationInput{
			ApplicationFields: service.ApplicationFields{
				Company: "Acme", Role: "Engineer", AppliedOn: appliedOn,
			},
			Status: status,
		})
	}

	create("applied", "2026-11-01")      // stale
	create("applied", "2026-11-20")      // fresh
	create("interviewing", "2026-11-20") // fresh
	create("offer", "2026-11-18")        // stale under the 3-day threshold
	create("rejected", "2020-01-01")     // terminal, never stale

	got, err := f.svc.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	want := storedb.DashboardCountsRow{
		Total:        5,
		Active:       4,
		Interviewing: 1,
		Offers:       1,
		Stale:        2,
	}
	if got != want {
		t.Errorf("Dashboard = %+v, want %+v", got, want)
	}
}

func validationFields(t *testing.T, err error) map[string]string {
	t.Helper()

	var invalid *service.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *service.ValidationError", err)
	}
	return invalid.Fields
}

func sortedKeys(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for field := range fields {
		keys = append(keys, field)
	}
	slices.Sort(keys)
	return keys
}
