package web

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/rgmaz/job-application-tracker/internal/model"
	"github.com/rgmaz/job-application-tracker/internal/service"
	storedb "github.com/rgmaz/job-application-tracker/internal/store/db"
)

const maxFormBytes = 1 << 20

// flashMessages maps short, URL-safe codes to user-facing text. Keeping the
// lookup server-side means a crafted ?flash= value cannot inject copy.
var flashMessages = map[string]string{
	"created": "Application created.",
	"updated": "Application updated.",
	"status":  "Status updated.",
	"note":    "Timeline updated.",
	"deleted": "Application deleted.",

	"document":         "Document uploaded.",
	"document_deleted": "Document deleted.",

	"thresholds": "Thresholds updated.",
	"password":   "Password changed.",
	"sessions":   "Signed out everywhere.",
}

type applicationFilter struct {
	Status    string
	Source    string
	Query     string
	StaleOnly bool
}

type applicationListView struct {
	PageData
	Rows     []storedb.ListApplicationsRow
	Filter   applicationFilter
	Statuses []model.Status
	Sources  []model.Source
	Errors   map[string]string
}

type applicationForm struct {
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
	Status       string
}

type applicationFormView struct {
	PageData
	Form     applicationForm
	Errors   map[string]string
	Statuses []model.Status
	Sources  []model.Source
	IsEdit   bool
	ID       int64
}

// Action is the form's submit target: create on the collection, update on the
// application itself.
func (v applicationFormView) Action() string {
	if v.IsEdit {
		return applicationURL(v.ID)
	}
	return "/applications"
}

// DetailURL lets the form's cancel link return to the right place.
func (v applicationFormView) DetailURL() string {
	return applicationURL(v.ID)
}

type applicationDetailView struct {
	PageData
	App           storedb.Application
	Events        []storedb.Event
	Documents     []storedb.Document
	Errors        map[string]string
	Statuses      []model.Status
	EventTypes    []service.EventTypeOption
	DocumentKinds []service.DocumentKindOption
	MaxUploadMB   int64
}

func (v applicationDetailView) DetailURL() string {
	return applicationURL(v.App.ID)
}

func (s *server) listApplications(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}

	view, status, err := s.listView(r)
	if err != nil {
		http.Error(w, "could not load applications", http.StatusInternalServerError)
		return
	}
	s.render(w, status, "applications_list.html", view)
}

// applicationRows is the htmx target for the filter form. It renders the table
// body only so filtering never reloads the page.
func (s *server) applicationRows(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}

	view, status, err := s.listView(r)
	if err != nil {
		http.Error(w, "could not load applications", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, status, "applications_list.html", "application_rows", view)
}

func (s *server) listView(r *http.Request) (applicationListView, int, error) {
	query := r.URL.Query()
	filter := applicationFilter{
		Status:    query.Get("status"),
		Source:    query.Get("source"),
		Query:     query.Get("q"),
		StaleOnly: query.Get("stale") == "1",
	}

	view := applicationListView{
		PageData: PageData{Title: "Applications", Flash: flashMessage(r), Nav: "applications"},
		Filter:   filter,
		Statuses: model.Statuses,
		Sources:  model.Sources,
	}

	rows, err := s.apps.List(r.Context(), service.ListFilter{
		Status:    filter.Status,
		Source:    filter.Source,
		Query:     filter.Query,
		StaleOnly: filter.StaleOnly,
	})
	if err != nil {
		var invalid *service.ValidationError
		if errors.As(err, &invalid) {
			view.Errors = invalid.Fields
			return view, http.StatusUnprocessableEntity, nil
		}
		return view, http.StatusInternalServerError, err
	}

	view.Rows = rows
	return view, http.StatusOK, nil
}

func (s *server) newApplicationForm(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}

	form := applicationForm{
		Source: string(model.SourceOther),
		Status: string(model.StatusApplied),
	}
	s.renderForm(w, http.StatusOK, s.formView(r, form, nil, false, 0))
}

func (s *server) createApplication(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) || !parseForm(w, r) {
		return
	}

	form := formFromRequest(r)
	app, err := s.apps.Create(r.Context(), service.CreateApplicationInput{
		ApplicationFields: fieldsFromForm(form),
		Status:            form.Status,
	})
	if err != nil {
		var invalid *service.ValidationError
		if errors.As(err, &invalid) {
			s.renderForm(w, http.StatusUnprocessableEntity, s.formView(r, form, invalid.Fields, false, 0))
			return
		}
		http.Error(w, "could not create application", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, flashURL(app.ID, "created"), http.StatusSeeOther)
}

func (s *server) applicationDetail(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	app, events, docs, err := s.loadDetail(r, id)
	if err != nil {
		writeLoadError(w, r, err)
		return
	}
	s.renderDetail(w, r, http.StatusOK, app, events, docs, nil)
}

func (s *server) editApplicationForm(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	app, err := s.apps.Get(r.Context(), id)
	if err != nil {
		writeLoadError(w, r, err)
		return
	}
	s.renderForm(w, http.StatusOK, s.formView(r, formFromApplication(app), nil, true, app.ID))
}

func (s *server) updateApplication(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok || !parseForm(w, r) {
		return
	}

	form := formFromRequest(r)
	app, err := s.apps.Update(r.Context(), id, fieldsFromForm(form))
	if err != nil {
		var invalid *service.ValidationError
		if errors.As(err, &invalid) {
			s.renderForm(w, http.StatusUnprocessableEntity, s.formView(r, form, invalid.Fields, true, id))
			return
		}
		writeLoadError(w, r, err)
		return
	}

	http.Redirect(w, r, flashURL(app.ID, "updated"), http.StatusSeeOther)
}

func (s *server) changeStatus(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok || !parseForm(w, r) {
		return
	}

	app, err := s.apps.ChangeStatus(r.Context(), id, r.PostFormValue("status"))
	if err != nil {
		var invalid *service.ValidationError
		if !errors.As(err, &invalid) {
			writeLoadError(w, r, err)
			return
		}

		app, events, docs, loadErr := s.loadDetail(r, id)
		if loadErr != nil {
			writeLoadError(w, r, loadErr)
			return
		}
		s.renderDetail(w, r, http.StatusUnprocessableEntity, app, events, docs, invalid.Fields)
		return
	}

	http.Redirect(w, r, flashURL(app.ID, "status"), http.StatusSeeOther)
}

func (s *server) addEvent(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok || !parseForm(w, r) {
		return
	}

	_, err := s.apps.AddEvent(r.Context(), id, r.PostFormValue("type"), r.PostFormValue("body"))
	if err != nil {
		var invalid *service.ValidationError
		if !errors.As(err, &invalid) {
			writeLoadError(w, r, err)
			return
		}

		events, loadErr := s.apps.ListEvents(r.Context(), id)
		if loadErr != nil {
			writeLoadError(w, r, loadErr)
			return
		}

		if isHTMX(r) {
			// htmx does not swap 4xx responses, so the fragment carries the
			// error and the form stays usable in place.
			view := s.detailView(r, storedb.Application{ID: id}, events, nil, invalid.Fields)
			s.renderPartial(w, http.StatusOK, "applications_detail.html", "timeline_block", view)
			return
		}

		app, detailEvents, docs, loadErr := s.loadDetail(r, id)
		if loadErr != nil {
			writeLoadError(w, r, loadErr)
			return
		}
		s.renderDetail(w, r, http.StatusUnprocessableEntity, app, detailEvents, docs, invalid.Fields)
		return
	}

	if isHTMX(r) {
		events, loadErr := s.apps.ListEvents(r.Context(), id)
		if loadErr != nil {
			writeLoadError(w, r, loadErr)
			return
		}
		view := s.detailView(r, storedb.Application{ID: id}, events, nil, nil)
		s.renderPartial(w, http.StatusOK, "applications_detail.html", "timeline_block", view)
		return
	}

	http.Redirect(w, r, flashURL(id, "note"), http.StatusSeeOther)
}

func (s *server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	if !s.ready(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// The cascade removes document rows; the returned keys let us remove the
	// bytes after the commit. DeleteStored logs orphans rather than failing the
	// request.
	keys, err := s.apps.Delete(r.Context(), id)
	if err != nil {
		writeLoadError(w, r, err)
		return
	}
	s.docs.DeleteStored(r.Context(), keys)

	http.Redirect(w, r, "/applications?flash=deleted", http.StatusSeeOther)
}

func (s *server) loadDetail(r *http.Request, id int64) (storedb.Application, []storedb.Event, []storedb.Document, error) {
	app, err := s.apps.Get(r.Context(), id)
	if err != nil {
		return storedb.Application{}, nil, nil, err
	}
	events, err := s.apps.ListEvents(r.Context(), id)
	if err != nil {
		return storedb.Application{}, nil, nil, err
	}
	docs, err := s.docs.List(r.Context(), id)
	if err != nil {
		return storedb.Application{}, nil, nil, err
	}
	return app, events, docs, nil
}

func (s *server) formView(r *http.Request, form applicationForm, errs map[string]string, isEdit bool, id int64) applicationFormView {
	title := "New application"
	if isEdit {
		title = "Edit application"
	}
	return applicationFormView{
		PageData: PageData{Title: title, Flash: flashMessage(r), Nav: "applications"},
		Form:     form,
		Errors:   errs,
		Statuses: model.Statuses,
		Sources:  model.Sources,
		IsEdit:   isEdit,
		ID:       id,
	}
}

func (s *server) detailView(r *http.Request, app storedb.Application, events []storedb.Event, docs []storedb.Document, errs map[string]string) applicationDetailView {
	return applicationDetailView{
		PageData:      PageData{Title: app.Company, Flash: flashMessage(r), Nav: "applications"},
		App:           app,
		Events:        events,
		Documents:     docs,
		Errors:        errs,
		Statuses:      model.Statuses,
		EventTypes:    service.UserEventTypes,
		DocumentKinds: service.DocumentKinds,
		MaxUploadMB:   s.docs.MaxUploadBytes() >> 20,
	}
}

func (s *server) renderDetail(w http.ResponseWriter, r *http.Request, status int, app storedb.Application, events []storedb.Event, docs []storedb.Document, errs map[string]string) {
	s.render(w, status, "applications_detail.html", s.detailView(r, app, events, docs, errs))
}

func (s *server) renderForm(w http.ResponseWriter, status int, view applicationFormView) {
	s.render(w, status, "applications_form.html", view)
}

func (s *server) render(w http.ResponseWriter, status int, page string, data any) {
	if err := s.renderer.Render(w, status, page, data); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
	}
}

func (s *server) renderPartial(w http.ResponseWriter, status int, page, name string, data any) {
	if err := s.renderer.RenderPartial(w, status, page, name, data); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
	}
}

func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func writeLoadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "could not load application", http.StatusInternalServerError)
}

func formFromRequest(r *http.Request) applicationForm {
	return applicationForm{
		Company:      r.PostFormValue("company"),
		Role:         r.PostFormValue("role"),
		Location:     r.PostFormValue("location"),
		JobURL:       r.PostFormValue("job_url"),
		Source:       r.PostFormValue("source"),
		AppliedOn:    r.PostFormValue("applied_on"),
		FollowUpOn:   r.PostFormValue("follow_up_on"),
		ContactName:  r.PostFormValue("contact_name"),
		ContactEmail: r.PostFormValue("contact_email"),
		EmailSubject: r.PostFormValue("email_subject"),
		EmailLink:    r.PostFormValue("email_link"),
		Notes:        r.PostFormValue("notes"),
		Status:       r.PostFormValue("status"),
	}
}

func formFromApplication(app storedb.Application) applicationForm {
	return applicationForm{
		Company:      app.Company,
		Role:         app.Role,
		Location:     deref(app.Location),
		JobURL:       deref(app.JobUrl),
		Source:       app.Source,
		AppliedOn:    app.AppliedOn,
		FollowUpOn:   deref(app.FollowUpOn),
		ContactName:  deref(app.ContactName),
		ContactEmail: deref(app.ContactEmail),
		EmailSubject: deref(app.EmailSubject),
		EmailLink:    deref(app.EmailLink),
		Notes:        deref(app.Notes),
		Status:       app.Status,
	}
}

func fieldsFromForm(form applicationForm) service.ApplicationFields {
	return service.ApplicationFields{
		Company:      form.Company,
		Role:         form.Role,
		Location:     form.Location,
		JobURL:       form.JobURL,
		Source:       form.Source,
		AppliedOn:    form.AppliedOn,
		FollowUpOn:   form.FollowUpOn,
		ContactName:  form.ContactName,
		ContactEmail: form.ContactEmail,
		EmailSubject: form.EmailSubject,
		EmailLink:    form.EmailLink,
		Notes:        form.Notes,
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func flashMessage(r *http.Request) string {
	return flashMessages[r.URL.Query().Get("flash")]
}

func applicationURL(id int64) string {
	return "/applications/" + strconv.FormatInt(id, 10)
}

func flashURL(id int64, code string) string {
	return applicationURL(id) + "?flash=" + url.QueryEscape(code)
}
