package model_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/rgmaz/job-application-tracker/internal/model"
)

func TestStatusesAreOrdered(t *testing.T) {
	t.Parallel()

	want := []model.Status{
		model.StatusApplied,
		model.StatusScreening,
		model.StatusInterviewing,
		model.StatusOffer,
		model.StatusAccepted,
		model.StatusRejected,
		model.StatusWithdrawn,
		model.StatusGhosted,
	}
	if !reflect.DeepEqual(model.Statuses, want) {
		t.Errorf("Statuses = %v, want %v", model.Statuses, want)
	}
}

func TestStatusesAreValidAndUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[model.Status]bool, len(model.Statuses))
	for _, status := range model.Statuses {
		if seen[status] {
			t.Errorf("duplicate status %q", status)
		}
		seen[status] = true

		if !status.Valid() {
			t.Errorf("%q.Valid() = false, want true", status)
		}
		if status.Label() == "" {
			t.Errorf("%q.Label() is empty", status)
		}
	}
}

func TestStatusValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status model.Status
		want   bool
	}{
		{status: model.StatusApplied, want: true},
		{status: model.StatusScreening, want: true},
		{status: model.StatusInterviewing, want: true},
		{status: model.StatusOffer, want: true},
		{status: model.StatusAccepted, want: true},
		{status: model.StatusRejected, want: true},
		{status: model.StatusWithdrawn, want: true},
		{status: model.StatusGhosted, want: true},
		{status: "", want: false},
		{status: "APPLIED", want: false},
		{status: "unknown", want: false},
	}
	for _, tt := range tests {
		if got := tt.status.Valid(); got != tt.want {
			t.Errorf("%q.Valid() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestStatusIsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status model.Status
		want   bool
	}{
		{status: model.StatusApplied, want: false},
		{status: model.StatusScreening, want: false},
		{status: model.StatusInterviewing, want: false},
		{status: model.StatusOffer, want: false},
		{status: model.StatusAccepted, want: true},
		{status: model.StatusRejected, want: true},
		{status: model.StatusWithdrawn, want: true},
		{status: model.StatusGhosted, want: true},
		{status: "unknown", want: false},
	}
	for _, tt := range tests {
		if got := tt.status.IsTerminal(); got != tt.want {
			t.Errorf("%q.IsTerminal() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status model.Status
		want   string
	}{
		{status: model.StatusApplied, want: "Applied"},
		{status: model.StatusScreening, want: "Screening"},
		{status: model.StatusInterviewing, want: "Interviewing"},
		{status: model.StatusOffer, want: "Offer"},
		{status: model.StatusAccepted, want: "Accepted"},
		{status: model.StatusRejected, want: "Rejected"},
		{status: model.StatusWithdrawn, want: "Withdrawn"},
		{status: model.StatusGhosted, want: "Ghosted"},
		{status: "custom", want: "custom"},
	}
	for _, tt := range tests {
		if got := tt.status.Label(); got != tt.want {
			t.Errorf("%q.Label() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    model.Status
		wantErr bool
	}{
		{raw: "applied", want: model.StatusApplied},
		{raw: "ghosted", want: model.StatusGhosted},
		{raw: "", wantErr: true},
		{raw: "APPLIED", wantErr: true},
		{raw: "unknown", wantErr: true},
	}
	for _, tt := range tests {
		got, err := model.ParseStatus(tt.raw)
		if tt.wantErr {
			if !errors.Is(err, model.ErrInvalidStatus) {
				t.Errorf("ParseStatus(%q) error = %v, want ErrInvalidStatus", tt.raw, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseStatus(%q): %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseStatus(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestSourcesAreOrdered(t *testing.T) {
	t.Parallel()

	want := []model.Source{
		model.SourceLinkedIn,
		model.SourceReferral,
		model.SourceCompanySite,
		model.SourceJobBoard,
		model.SourceRecruiter,
		model.SourceOther,
	}
	if !reflect.DeepEqual(model.Sources, want) {
		t.Errorf("Sources = %v, want %v", model.Sources, want)
	}
}

func TestSourcesAreValidAndUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[model.Source]bool, len(model.Sources))
	for _, source := range model.Sources {
		if seen[source] {
			t.Errorf("duplicate source %q", source)
		}
		seen[source] = true

		if !source.Valid() {
			t.Errorf("%q.Valid() = false, want true", source)
		}
		if source.Label() == "" {
			t.Errorf("%q.Label() is empty", source)
		}
	}
}

func TestSourceValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source model.Source
		want   bool
	}{
		{source: model.SourceLinkedIn, want: true},
		{source: model.SourceReferral, want: true},
		{source: model.SourceCompanySite, want: true},
		{source: model.SourceJobBoard, want: true},
		{source: model.SourceRecruiter, want: true},
		{source: model.SourceOther, want: true},
		{source: "", want: false},
		{source: "LINKEDIN", want: false},
		{source: "unknown", want: false},
	}
	for _, tt := range tests {
		if got := tt.source.Valid(); got != tt.want {
			t.Errorf("%q.Valid() = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestSourceLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source model.Source
		want   string
	}{
		{source: model.SourceLinkedIn, want: "LinkedIn"},
		{source: model.SourceReferral, want: "Referral"},
		{source: model.SourceCompanySite, want: "Company site"},
		{source: model.SourceJobBoard, want: "Job board"},
		{source: model.SourceRecruiter, want: "Recruiter"},
		{source: model.SourceOther, want: "Other"},
		{source: "custom", want: "custom"},
	}
	for _, tt := range tests {
		if got := tt.source.Label(); got != tt.want {
			t.Errorf("%q.Label() = %q, want %q", tt.source, got, tt.want)
		}
	}
}

func TestParseSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    model.Source
		wantErr bool
	}{
		{raw: "linkedin", want: model.SourceLinkedIn},
		{raw: "other", want: model.SourceOther},
		{raw: "", wantErr: true},
		{raw: "LINKEDIN", wantErr: true},
		{raw: "unknown", wantErr: true},
	}
	for _, tt := range tests {
		got, err := model.ParseSource(tt.raw)
		if tt.wantErr {
			if !errors.Is(err, model.ErrInvalidSource) {
				t.Errorf("ParseSource(%q) error = %v, want ErrInvalidSource", tt.raw, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSource(%q): %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseSource(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
