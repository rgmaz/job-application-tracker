// Package service implements application use cases on top of the generated
// store queries.
package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/model"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
	"github.com/rgmaz/job-application-tracker/internal/timeutil"
)

const (
	eventStatusChange = "status_change"

	defaultListLimit = 50
	maxListLimit     = 200
)

// Clock supplies the current instant and the Harare calendar date. The
// production implementation is *timeutil.Clock; tests provide a fixed clock.
type Clock interface {
	Now() time.Time
	Today() string
}

// ApplicationService coordinates application writes, timeline events, and
// reads. It owns the transaction boundaries the generated queries do not.
type ApplicationService struct {
	db    *sql.DB
	q     *storedb.Queries
	clock Clock
}

// NewApplications returns an application service backed by db.
func NewApplications(db *sql.DB, clock Clock) *ApplicationService {
	return &ApplicationService{db: db, q: storedb.New(db), clock: clock}
}

// ValidationError reports per-field validation failures so the web layer can
// re-render a form with 422 and field-level messages.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	fields := make([]string, 0, len(e.Fields))
	for field := range e.Fields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return "validation failed: " + strings.Join(fields, ", ")
}

// ApplicationFields are the user-editable application columns. Status changes
// go through ChangeStatus so they also append a timeline event.
type ApplicationFields struct {
	Company      string
	Role         string
	Location     string
	JobURL       string
	Source       string
	AppliedOn    string
	FollowUpOn   string
	ContactName  string
	ContactEmail string
	EmailSubject string
	EmailLink    string
	Notes        string
}

// CreateApplicationInput adds the initial status to the editable fields.
type CreateApplicationInput struct {
	ApplicationFields
	Status string
}

// ListFilter narrows List. Empty strings and a zero Limit mean "no filter" and
// "default page size" respectively.
type ListFilter struct {
	Status    string
	Source    string
	Query     string
	StaleOnly bool
	Limit     int64
	Offset    int64
}

type validatedFields struct {
	company      string
	role         string
	location     *string
	jobURL       *string
	source       model.Source
	appliedOn    string
	followUpOn   *string
	contactName  *string
	contactEmail *string
	emailSubject *string
	emailLink    *string
	notes        *string
}

// Create validates the input, inserts the application, and records the first
// status_change event in the same transaction.
func (s *ApplicationService) Create(ctx context.Context, in CreateApplicationInput) (storedb.Application, error) {
	fields, errs := validateFields(in.ApplicationFields, s.clock)

	statusRaw := strings.TrimSpace(in.Status)
	if statusRaw == "" {
		statusRaw = string(model.StatusApplied)
	}
	status, statusErr := model.ParseStatus(statusRaw)
	if statusErr != nil {
		errs["status"] = "Choose a valid status."
	}
	if len(errs) > 0 {
		return storedb.Application{}, &ValidationError{Fields: errs}
	}

	now := s.clock.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storedb.Application{}, fmt.Errorf("begin create application: %w", err)
	}
	defer rollback(tx)

	q := s.q.WithTx(tx)
	app, err := q.CreateApplication(ctx, storedb.CreateApplicationParams{
		Company:         fields.company,
		Role:            fields.role,
		Location:        fields.location,
		JobUrl:          fields.jobURL,
		Source:          string(fields.source),
		AppliedOn:       fields.appliedOn,
		Status:          string(status),
		StatusChangedOn: fields.appliedOn,
		StatusChangedAt: now.Unix(),
		FollowUpOn:      fields.followUpOn,
		ContactName:     fields.contactName,
		ContactEmail:    fields.contactEmail,
		EmailSubject:    fields.emailSubject,
		EmailLink:       fields.emailLink,
		Notes:           fields.notes,
		CreatedAt:       now.Unix(),
		UpdatedAt:       now.Unix(),
	})
	if err != nil {
		return storedb.Application{}, fmt.Errorf("create application: %w", err)
	}

	body := "Status set to " + status.Label()
	if _, err := q.CreateEvent(ctx, storedb.CreateEventParams{
		ApplicationID: app.ID,
		Type:          eventStatusChange,
		Body:          &body,
		OccurredAt:    now.Unix(),
		CreatedAt:     now.Unix(),
	}); err != nil {
		return storedb.Application{}, fmt.Errorf("create initial status event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return storedb.Application{}, fmt.Errorf("commit create application: %w", err)
	}
	return app, nil
}

// Get returns one application, or sql.ErrNoRows if it does not exist.
func (s *ApplicationService) Get(ctx context.Context, id int64) (storedb.Application, error) {
	app, err := s.q.GetApplication(ctx, id)
	if err != nil {
		return storedb.Application{}, fmt.Errorf("get application %d: %w", id, err)
	}
	return app, nil
}

// Update edits the user-editable fields without touching the status or its
// timestamps.
func (s *ApplicationService) Update(ctx context.Context, id int64, in ApplicationFields) (storedb.Application, error) {
	fields, errs := validateFields(in, s.clock)
	if len(errs) > 0 {
		return storedb.Application{}, &ValidationError{Fields: errs}
	}

	now := s.clock.Now()
	app, err := s.q.UpdateApplication(ctx, storedb.UpdateApplicationParams{
		Company:      fields.company,
		Role:         fields.role,
		Location:     fields.location,
		JobUrl:       fields.jobURL,
		Source:       string(fields.source),
		AppliedOn:    fields.appliedOn,
		FollowUpOn:   fields.followUpOn,
		ContactName:  fields.contactName,
		ContactEmail: fields.contactEmail,
		EmailSubject: fields.emailSubject,
		EmailLink:    fields.emailLink,
		Notes:        fields.notes,
		UpdatedAt:    now.Unix(),
		ID:           id,
	})
	if err != nil {
		return storedb.Application{}, fmt.Errorf("update application %d: %w", id, err)
	}
	return app, nil
}

// ChangeStatus validates and applies a status transition, recording a
// status_change event in the same transaction. Re-applying the current status
// is a no-op so it does not reset the follow-up clock.
func (s *ApplicationService) ChangeStatus(ctx context.Context, id int64, rawStatus string) (storedb.Application, error) {
	status, err := model.ParseStatus(strings.TrimSpace(rawStatus))
	if err != nil {
		return storedb.Application{}, &ValidationError{
			Fields: map[string]string{"status": "Choose a valid status."},
		}
	}

	now := s.clock.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storedb.Application{}, fmt.Errorf("begin status change: %w", err)
	}
	defer rollback(tx)

	q := s.q.WithTx(tx)
	current, err := q.GetApplication(ctx, id)
	if err != nil {
		return storedb.Application{}, fmt.Errorf("get application %d: %w", id, err)
	}
	if current.Status == string(status) {
		return current, nil
	}

	updated, err := q.UpdateStatus(ctx, storedb.UpdateStatusParams{
		Status:          string(status),
		StatusChangedOn: s.clock.Today(),
		StatusChangedAt: now.Unix(),
		UpdatedAt:       now.Unix(),
		ID:              id,
	})
	if err != nil {
		return storedb.Application{}, fmt.Errorf("update status for application %d: %w", id, err)
	}

	body := fmt.Sprintf("%s → %s", model.Status(current.Status).Label(), status.Label())
	if _, err := q.CreateEvent(ctx, storedb.CreateEventParams{
		ApplicationID: id,
		Type:          eventStatusChange,
		Body:          &body,
		OccurredAt:    now.Unix(),
		CreatedAt:     now.Unix(),
	}); err != nil {
		return storedb.Application{}, fmt.Errorf("create status event for application %d: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return storedb.Application{}, fmt.Errorf("commit status change for application %d: %w", id, err)
	}
	return updated, nil
}

// Delete removes an application and its cascaded rows, returning the storage
// keys of its documents so the caller can delete the files after the commit.
func (s *ApplicationService) Delete(ctx context.Context, id int64) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delete application: %w", err)
	}
	defer rollback(tx)

	q := s.q.WithTx(tx)
	if _, err := q.GetApplication(ctx, id); err != nil {
		return nil, fmt.Errorf("get application %d: %w", id, err)
	}

	docs, err := q.ListDocuments(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list documents for application %d: %w", id, err)
	}
	keys := make([]string, 0, len(docs))
	for _, doc := range docs {
		keys = append(keys, doc.StorageKey)
	}

	deleted, err := q.DeleteApplication(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("delete application %d: %w", id, err)
	}
	if deleted != 1 {
		return nil, fmt.Errorf("delete application %d: deleted %d rows", id, deleted)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete application %d: %w", id, err)
	}
	return keys, nil
}

// List returns applications matching filter, with derived due_on and is_stale
// values relative to today in the configured location.
func (s *ApplicationService) List(ctx context.Context, filter ListFilter) ([]storedb.ListApplicationsRow, error) {
	errs := make(map[string]string)

	statusFilter := strings.TrimSpace(filter.Status)
	if statusFilter != "" {
		if _, err := model.ParseStatus(statusFilter); err != nil {
			errs["status"] = "Choose a valid status."
		}
	}
	sourceFilter := strings.TrimSpace(filter.Source)
	if sourceFilter != "" {
		if _, err := model.ParseSource(sourceFilter); err != nil {
			errs["source"] = "Choose a valid source."
		}
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	staleOnly := int64(0)
	if filter.StaleOnly {
		staleOnly = 1
	}

	rows, err := s.q.ListApplications(ctx, storedb.ListApplicationsParams{
		Today:        s.clock.Today(),
		StatusFilter: statusFilter,
		SourceFilter: sourceFilter,
		Q:            strings.TrimSpace(filter.Query),
		StaleOnly:    staleOnly,
		Offset:       offset,
		Limit:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	return rows, nil
}

// Dashboard returns the headline counts for the dashboard.
func (s *ApplicationService) Dashboard(ctx context.Context) (storedb.DashboardCountsRow, error) {
	counts, err := s.q.DashboardCounts(ctx, s.clock.Today())
	if err != nil {
		return storedb.DashboardCountsRow{}, fmt.Errorf("dashboard counts: %w", err)
	}
	return counts, nil
}

func validateFields(in ApplicationFields, clock Clock) (validatedFields, map[string]string) {
	errs := make(map[string]string)

	company := strings.TrimSpace(in.Company)
	if company == "" {
		errs["company"] = "Company is required."
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		errs["role"] = "Role is required."
	}

	source := model.SourceOther
	if raw := strings.TrimSpace(in.Source); raw != "" {
		parsed, err := model.ParseSource(raw)
		if err != nil {
			errs["source"] = "Choose a valid source."
		} else {
			source = parsed
		}
	}

	appliedOn := strings.TrimSpace(in.AppliedOn)
	if appliedOn == "" {
		appliedOn = clock.Today()
	} else if !validDate(appliedOn) {
		errs["applied_on"] = "Use a valid date (YYYY-MM-DD)."
	}

	followUpOn := optional(in.FollowUpOn)
	if followUpOn != nil && !validDate(*followUpOn) {
		errs["follow_up_on"] = "Use a valid date (YYYY-MM-DD)."
	}

	return validatedFields{
		company:      company,
		role:         role,
		location:     optional(in.Location),
		jobURL:       optional(in.JobURL),
		source:       source,
		appliedOn:    appliedOn,
		followUpOn:   followUpOn,
		contactName:  optional(in.ContactName),
		contactEmail: optional(in.ContactEmail),
		emailSubject: optional(in.EmailSubject),
		emailLink:    optional(in.EmailLink),
		notes:        optional(in.Notes),
	}, errs
}

func optional(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	return &value
}

// rollback discards a transaction. After a successful Commit it returns
// sql.ErrTxDone, which is expected and safe to ignore.
func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

func validDate(raw string) bool {
	parsed, err := time.Parse(timeutil.DateLayout, raw)
	return err == nil && parsed.Format(timeutil.DateLayout) == raw
}
