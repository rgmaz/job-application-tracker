// Package model holds the domain vocabulary shared by the service and web
// layers: application statuses and their sources.
package model

import (
	"errors"
	"fmt"
)

// ErrInvalidStatus is returned by ParseStatus for an unrecognised status.
var ErrInvalidStatus = errors.New("invalid status")

// Status is an application's position in the hiring pipeline.
type Status string

const (
	StatusApplied      Status = "applied"
	StatusScreening    Status = "screening"
	StatusInterviewing Status = "interviewing"
	StatusOffer        Status = "offer"
	StatusAccepted     Status = "accepted"
	StatusRejected     Status = "rejected"
	StatusWithdrawn    Status = "withdrawn"
	StatusGhosted      Status = "ghosted"
)

// Statuses is the ordered pipeline, active statuses first and terminal
// statuses last. It is the source for status selectors and dashboard grouping.
var Statuses = []Status{
	StatusApplied,
	StatusScreening,
	StatusInterviewing,
	StatusOffer,
	StatusAccepted,
	StatusRejected,
	StatusWithdrawn,
	StatusGhosted,
}

var statusLabels = map[Status]string{
	StatusApplied:      "Applied",
	StatusScreening:    "Screening",
	StatusInterviewing: "Interviewing",
	StatusOffer:        "Offer",
	StatusAccepted:     "Accepted",
	StatusRejected:     "Rejected",
	StatusWithdrawn:    "Withdrawn",
	StatusGhosted:      "Ghosted",
}

// Label returns the human-readable status name, falling back to the raw value
// so an unexpected database row is still visible rather than blank.
func (s Status) Label() string {
	if label, ok := statusLabels[s]; ok {
		return label
	}
	return string(s)
}

// IsTerminal reports whether the application has reached a final state and
// therefore has no follow-up threshold.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusAccepted, StatusRejected, StatusWithdrawn, StatusGhosted:
		return true
	default:
		return false
	}
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	_, ok := statusLabels[s]
	return ok
}

// ParseStatus validates and converts a raw status value.
func ParseStatus(raw string) (Status, error) {
	status := Status(raw)
	if !status.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidStatus, raw)
	}
	return status, nil
}

// ErrInvalidSource is returned by ParseSource for an unrecognised source.
var ErrInvalidSource = errors.New("invalid source")

// Source records where an application came from.
type Source string

const (
	SourceLinkedIn    Source = "linkedin"
	SourceReferral    Source = "referral"
	SourceCompanySite Source = "company_site"
	SourceJobBoard    Source = "job_board"
	SourceRecruiter   Source = "recruiter"
	SourceOther       Source = "other"
)

// Sources is the ordered list used by source selectors.
var Sources = []Source{
	SourceLinkedIn,
	SourceReferral,
	SourceCompanySite,
	SourceJobBoard,
	SourceRecruiter,
	SourceOther,
}

var sourceLabels = map[Source]string{
	SourceLinkedIn:    "LinkedIn",
	SourceReferral:    "Referral",
	SourceCompanySite: "Company site",
	SourceJobBoard:    "Job board",
	SourceRecruiter:   "Recruiter",
	SourceOther:       "Other",
}

// Label returns the human-readable source name, falling back to the raw value.
func (s Source) Label() string {
	if label, ok := sourceLabels[s]; ok {
		return label
	}
	return string(s)
}

// Valid reports whether s is a known source.
func (s Source) Valid() bool {
	_, ok := sourceLabels[s]
	return ok
}

// ParseSource validates and converts a raw source value.
func ParseSource(raw string) (Source, error) {
	source := Source(raw)
	if !source.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidSource, raw)
	}
	return source, nil
}
