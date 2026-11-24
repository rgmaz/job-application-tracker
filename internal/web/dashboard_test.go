package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestDashboard(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)
	f.seed(t, "Acme", "Backend Engineer", "linkedin", "2026-11-01", "applied")       // stale
	f.seed(t, "Globex", "Frontend Engineer", "referral", "2026-11-20", "applied")    // fresh
	f.seed(t, "Initech", "Data Analyst", "other", "2026-11-20", "interviewing")      // fresh
	f.seed(t, "OfferCo", "Platform Engineer", "company_site", "2026-11-18", "offer") // stale at 3 days
	f.seed(t, "Umbrella", "QA Engineer", "job_board", "2020-01-01", "rejected")      // terminal, never stale

	rec := f.get(t, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	for _, want := range []string{
		`data-count="total">5<`,
		`data-count="active">4<`,
		`data-count="interviewing">1<`,
		`data-count="offers">1<`,
		`data-count="stale">2<`,
		"Needs attention",
		"Recently added",
		`href="/applications?stale=1"`,
		`href="/applications"`,
		`href="/dashboard" aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard does not contain %q", want)
		}
	}

	stale := section(t, body, "Needs attention", "Recently added")
	for _, want := range []string{"Acme", "OfferCo"} {
		if !strings.Contains(stale, want) {
			t.Errorf("stale section does not contain %q", want)
		}
	}
	for _, notWant := range []string{"Globex", "Initech", "Umbrella"} {
		if strings.Contains(stale, notWant) {
			t.Errorf("stale section unexpectedly contains %q", notWant)
		}
	}

	recent := body[strings.Index(body, "Recently added"):]
	for _, want := range []string{"Acme", "Globex", "Initech", "OfferCo", "Umbrella"} {
		if !strings.Contains(recent, want) {
			t.Errorf("recent section does not contain %q", want)
		}
	}
}

func TestDashboardEmpty(t *testing.T) {
	t.Parallel()

	f := newWebFixture(t)

	rec := f.get(t, "/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-count="total">0<`,
		`data-count="active">0<`,
		`data-count="interviewing">0<`,
		`data-count="offers">0<`,
		`data-count="stale">0<`,
		"Nothing here.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty dashboard does not contain %q", want)
		}
	}
}

// section returns the markup between two headings so tests can assert what a
// single dashboard panel contains without matching the whole page.
func section(t *testing.T, body, start, end string) string {
	t.Helper()

	startIndex := strings.Index(body, start)
	if startIndex < 0 {
		t.Fatalf("body does not contain %q", start)
	}
	endIndex := strings.Index(body[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("body does not contain %q after %q", end, start)
	}
	return body[startIndex : startIndex+endIndex]
}
