// Package timeutil provides the application clock and date formatting.
//
// Calendar dates are handled in the configured location (Africa/Harare by
// default) because they are persisted as YYYY-MM-DD strings and compared
// lexicographically in SQLite.
package timeutil

import (
	"time"
	_ "time/tzdata" // embed the IANA database for minimal container images
)

const (
	// DateLayout is the persisted and displayed calendar-date format.
	DateLayout = "2006-01-02"
	// DateTimeLayout is the displayed local date and time format.
	DateTimeLayout = "2006-01-02 15:04"
)

// Clock reports and formats time in a fixed location.
type Clock struct {
	loc *time.Location
	now func() time.Time
}

// New returns a Clock backed by the system time source. A nil location falls
// back to UTC so callers cannot panic on a missing configuration value.
func New(loc *time.Location) *Clock {
	if loc == nil {
		loc = time.UTC
	}
	return newClock(loc, time.Now)
}

func newClock(loc *time.Location, now func() time.Time) *Clock {
	return &Clock{loc: loc, now: now}
}

// Now returns the current time in the clock's location.
func (c *Clock) Now() time.Time {
	return c.now().In(c.loc)
}

// Today returns the current calendar date in the clock's location.
func (c *Clock) Today() string {
	return c.Now().Format(DateLayout)
}

// FormatDate renders t as a calendar date in the clock's location.
func (c *Clock) FormatDate(t time.Time) string {
	return t.In(c.loc).Format(DateLayout)
}

// FormatDateTime renders t as a local date and time in the clock's location.
func (c *Clock) FormatDateTime(t time.Time) string {
	return t.In(c.loc).Format(DateTimeLayout)
}
