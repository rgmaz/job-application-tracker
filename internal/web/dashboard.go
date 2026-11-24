package web

import (
	"net/http"

	"github.com/rgmaz/job-application-tracker/internal/service"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

const (
	dashboardStaleLimit  = 10
	dashboardRecentLimit = 5
)

type dashboardView struct {
	PageData
	Counts storedb.DashboardCountsRow
	Stale  []storedb.ListApplicationsRow
	Recent []storedb.ListApplicationsRow
}

// dashboard shows the headline counts plus the two lists that need action:
// stale applications and the most recently added ones.
func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}

	counts, err := s.apps.Dashboard(r.Context())
	if err != nil {
		http.Error(w, "could not load dashboard", http.StatusInternalServerError)
		return
	}
	stale, err := s.apps.List(r.Context(), service.ListFilter{
		StaleOnly: true,
		Limit:     dashboardStaleLimit,
	})
	if err != nil {
		http.Error(w, "could not load dashboard", http.StatusInternalServerError)
		return
	}
	recent, err := s.apps.List(r.Context(), service.ListFilter{Limit: dashboardRecentLimit})
	if err != nil {
		http.Error(w, "could not load dashboard", http.StatusInternalServerError)
		return
	}

	s.render(w, http.StatusOK, "dashboard.html", dashboardView{
		PageData: PageData{Title: "Dashboard", Flash: flashMessage(r), Nav: "dashboard"},
		Counts:   counts,
		Stale:    stale,
		Recent:   recent,
	})
}
