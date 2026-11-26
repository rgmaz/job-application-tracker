package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/rgmaz/job-application-tracker/internal/model"
	"github.com/rgmaz/job-application-tracker/internal/service"
)

type thresholdRow struct {
	Status model.Status
	Label  string
	Days   string
	Error  string
}

type settingsView struct {
	PageData
	Timezone    string
	Thresholds  []thresholdRow
	PasswordSet bool
	Errors      map[string]string
}

func (s *server) settingsPage(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	s.renderSettings(w, r, http.StatusOK, nil, nil)
}

func (s *server) updateThresholds(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	if !parseForm(w, r) {
		return
	}

	values := make(map[string]string)
	for _, status := range service.ActiveStatuses() {
		values[string(status)] = r.PostFormValue(string(status))
	}

	if err := s.settings.UpdateThresholds(r.Context(), values); err != nil {
		var invalid *service.ValidationError
		if errors.As(err, &invalid) {
			s.renderSettings(w, r, http.StatusUnprocessableEntity, values, invalid.Fields)
			return
		}
		http.Error(w, "could not update thresholds", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings?flash=thresholds", http.StatusSeeOther)
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	if !parseForm(w, r) {
		return
	}

	err := s.settings.ChangePassword(r.Context(),
		r.PostFormValue("current_password"),
		r.PostFormValue("new_password"),
		r.PostFormValue("confirm_password"),
	)
	if err != nil {
		var invalid *service.ValidationError
		if errors.As(err, &invalid) {
			s.renderSettings(w, r, http.StatusUnprocessableEntity, nil, invalid.Fields)
			return
		}
		http.Error(w, "could not change password", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings?flash=password", http.StatusSeeOther)
}

func (s *server) rotateSessionSecret(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	if !parseForm(w, r) {
		return
	}

	if err := s.settings.RotateSessionSecret(r.Context()); err != nil {
		http.Error(w, "could not rotate session secret", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings?flash=sessions", http.StatusSeeOther)
}

// renderSettings builds the page from the stored thresholds, overlaying raw
// submitted values and per-field errors when a form is re-rendered.
func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, status int, values map[string]string, errs map[string]string) {
	thresholds, err := s.settings.Thresholds(r.Context())
	if err != nil {
		http.Error(w, "could not load settings", http.StatusInternalServerError)
		return
	}
	passwordSet, err := s.settings.PasswordSet(r.Context())
	if err != nil {
		http.Error(w, "could not load settings", http.StatusInternalServerError)
		return
	}

	rows := make([]thresholdRow, 0, len(thresholds))
	for _, threshold := range thresholds {
		days := strconv.Itoa(threshold.Days)
		if value, ok := values[string(threshold.Status)]; ok {
			days = value
		}
		rows = append(rows, thresholdRow{
			Status: threshold.Status,
			Label:  threshold.Status.Label(),
			Days:   days,
			Error:  errs[string(threshold.Status)],
		})
	}

	s.render(w, status, "settings.html", settingsView{
		PageData:    PageData{Title: "Settings", Flash: flashMessage(r), Nav: "settings"},
		Timezone:    s.settings.Timezone(),
		Thresholds:  rows,
		PasswordSet: passwordSet,
		Errors:      errs,
	})
}
