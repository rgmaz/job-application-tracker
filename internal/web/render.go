package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/model"
	"github.com/rgmaz/job-application-tracker/internal/timeutil"
	webassets "github.com/rgmaz/job-application-tracker/web"
)

const (
	displayDateLayout     = "2 Jan 2006"
	displayDateTimeLayout = "2 Jan 2006 15:04"
)

// PageData is the data every layout needs. Page-specific structs embed it so
// handlers can add their own fields without losing the nav and flash message.
type PageData struct {
	Title string
	Flash string
	Nav   string
}

// Renderer holds one parsed template set per page. Parsing each page separately
// lets every page define the "content" block without colliding with siblings.
type Renderer struct {
	pages map[string]*template.Template
}

// NewRenderer parses the layout, partials, and every page under templates/.
// The synthetic "layout" page renders the shell with its default content.
func NewRenderer(clock *timeutil.Clock) (*Renderer, error) {
	if clock == nil {
		clock = timeutil.New(nil)
	}
	funcs := templateFuncs(clock)

	base, err := parseBase(funcs)
	if err != nil {
		return nil, err
	}
	pages := map[string]*template.Template{"layout": base}

	files, err := fs.Glob(webassets.FS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: list templates: %w", err)
	}
	for _, file := range files {
		name := path.Base(file)
		if name == "layout.html" || strings.HasPrefix(name, "_") {
			continue
		}

		page, err := parseBase(funcs)
		if err != nil {
			return nil, err
		}
		if _, err := page.ParseFS(webassets.FS, file); err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", file, err)
		}
		pages[name] = page
	}

	return &Renderer{pages: pages}, nil
}

// Render executes page into a buffer before writing, so a template error can
// still become a clean 500 instead of a partial response.
func (r *Renderer) Render(w http.ResponseWriter, status int, page string, data any) error {
	return r.render(w, status, page, "layout", data)
}

// RenderPartial executes one named template from page's set. Handlers use it
// for htmx fragments so a swap never returns the surrounding layout.
func (r *Renderer) RenderPartial(w http.ResponseWriter, status int, page, name string, data any) error {
	return r.render(w, status, page, name, data)
}

func (r *Renderer) render(w http.ResponseWriter, status int, page, name string, data any) error {
	tmpl, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("web: unknown page %q", page)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("web: render %q/%q: %w", page, name, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		return fmt.Errorf("web: write %q/%q: %w", page, name, err)
	}
	return nil
}

func parseBase(funcs template.FuncMap) (*template.Template, error) {
	tmpl, err := template.New("layout").Funcs(funcs).ParseFS(
		webassets.FS, "templates/layout.html", "templates/_*.html",
	)
	if err != nil {
		return nil, fmt.Errorf("web: parse layout: %w", err)
	}
	return tmpl, nil
}

func templateFuncs(clock *timeutil.Clock) template.FuncMap {
	return template.FuncMap{
		"fmtDate":     func(value any) string { return formatDate(clock, value) },
		"fmtDateTime": func(value any) string { return formatDateTime(clock, value) },
		"statusLabel": func(value any) string {
			return model.Status(fmt.Sprint(value)).Label()
		},
		"sourceLabel": func(value any) string {
			return model.Source(fmt.Sprint(value)).Label()
		},
		"eventLabel": func(value any) string {
			switch raw := fmt.Sprint(value); raw {
			case "status_change":
				return "Status"
			case "note":
				return "Note"
			case "email_sent":
				return "Email sent"
			case "email_received":
				return "Email received"
			case "document_added":
				return "Document"
			default:
				return raw
			}
		},
		"isTerminal": func(value any) bool {
			return model.Status(fmt.Sprint(value)).IsTerminal()
		},
	}
}

func formatDate(clock *timeutil.Clock, value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		if v == "" {
			return ""
		}
		parsed, err := time.Parse(timeutil.DateLayout, v)
		if err != nil {
			return v
		}
		return parsed.Format(displayDateLayout)
	case *string:
		if v == nil {
			return ""
		}
		return formatDate(clock, *v)
	case time.Time:
		return v.In(clock.Location()).Format(displayDateLayout)
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.In(clock.Location()).Format(displayDateLayout)
	default:
		return fmt.Sprint(v)
	}
}

func formatDateTime(clock *timeutil.Clock, value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case time.Time:
		return v.In(clock.Location()).Format(displayDateTimeLayout)
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.In(clock.Location()).Format(displayDateTimeLayout)
	case int64:
		return time.Unix(v, 0).In(clock.Location()).Format(displayDateTimeLayout)
	case *int64:
		if v == nil {
			return ""
		}
		return time.Unix(*v, 0).In(clock.Location()).Format(displayDateTimeLayout)
	case int:
		return time.Unix(int64(v), 0).In(clock.Location()).Format(displayDateTimeLayout)
	case string:
		if v == "" {
			return ""
		}
		if parsed, err := time.Parse(timeutil.DateTimeLayout, v); err == nil {
			return parsed.Format(displayDateTimeLayout)
		}
		if parsed, err := time.Parse(timeutil.DateLayout, v); err == nil {
			return parsed.Format(displayDateLayout)
		}
		return v
	case *string:
		if v == nil {
			return ""
		}
		return formatDateTime(clock, *v)
	default:
		return fmt.Sprint(v)
	}
}
