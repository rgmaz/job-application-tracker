package timeutil

import (
	"testing"
	"time"
)

func harare(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Africa/Harare")
	if err != nil {
		t.Fatalf("load Africa/Harare: %v", err)
	}
	return loc
}

func TestClockToday(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{
			name: "just before harare midnight",
			now:  time.Date(2026, 1, 31, 21, 59, 59, 0, time.UTC),
			want: "2026-01-31",
		},
		{
			name: "at harare midnight",
			now:  time.Date(2026, 1, 31, 22, 0, 0, 0, time.UTC),
			want: "2026-02-01",
		},
		{
			name: "utc evening crosses the date line",
			now:  time.Date(2026, 1, 31, 23, 30, 0, 0, time.UTC),
			want: "2026-02-01",
		},
		{
			name: "harare afternoon",
			now:  time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC),
			want: "2026-06-15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newClock(loc, func() time.Time { return tt.now })
			if got := c.Today(); got != tt.want {
				t.Errorf("Today() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClockNow(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	fixed := time.Date(2026, 1, 31, 23, 30, 0, 0, time.UTC)
	c := newClock(loc, func() time.Time { return fixed })

	got := c.Now()
	if !got.Equal(fixed) {
		t.Errorf("Now() = %v, want instant %v", got, fixed)
	}
	if got.Location().String() != "Africa/Harare" {
		t.Errorf("Now().Location() = %q, want Africa/Harare", got.Location())
	}
}

func TestFormatDate(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "utc evening crosses the date line",
			in:   time.Date(2026, 1, 31, 23, 30, 0, 0, time.UTC),
			want: "2026-02-01",
		},
		{
			name: "utc evening stays on the same date",
			in:   time.Date(2026, 1, 31, 21, 59, 0, 0, time.UTC),
			want: "2026-01-31",
		},
		{
			name: "already in harare",
			in:   time.Date(2026, 3, 5, 10, 0, 0, 0, loc),
			want: "2026-03-05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newClock(loc, time.Now)
			if got := c.FormatDate(tt.in); got != tt.want {
				t.Errorf("FormatDate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatDateTime(t *testing.T) {
	t.Parallel()

	loc := harare(t)
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "crosses midnight",
			in:   time.Date(2026, 1, 31, 23, 30, 0, 0, time.UTC),
			want: "2026-02-01 01:30",
		},
		{
			name: "crosses new year",
			in:   time.Date(2026, 12, 31, 22, 15, 0, 0, time.UTC),
			want: "2027-01-01 00:15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newClock(loc, time.Now)
			if got := c.FormatDateTime(tt.in); got != tt.want {
				t.Errorf("FormatDateTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewNilLocationUsesUTC(t *testing.T) {
	t.Parallel()

	c := New(nil)
	if got := c.Now().Location(); got != time.UTC {
		t.Errorf("Now().Location() = %q, want UTC", got)
	}
}
