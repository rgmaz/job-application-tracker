package web

import (
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/rgmaz/job-application-tracker/internal/service"
)

const sessionCookieName = "jat_session"

type authView struct {
	PageData
	Error  string
	Errors map[string]string
}

func (s *server) setupForm(w http.ResponseWriter, r *http.Request) {
	if !s.readyAuth(w) {
		return
	}

	set, err := s.auth.PasswordSet(r.Context())
	if err != nil {
		http.Error(w, "could not load setup", http.StatusInternalServerError)
		return
	}
	if set {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	s.render(w, http.StatusOK, "setup.html", authView{
		PageData: PageData{Title: "Set up", Flash: flashMessage(r)},
	})
}

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.readyAuth(w) {
		return
	}
	if !parseForm(w, r) {
		return
	}

	err := s.auth.Setup(r.Context(),
		r.PostFormValue("password"),
		r.PostFormValue("confirm_password"),
	)
	if err != nil {
		var invalid *service.ValidationError
		switch {
		case errors.As(err, &invalid):
			s.render(w, http.StatusUnprocessableEntity, "setup.html", authView{
				PageData: PageData{Title: "Set up"},
				Errors:   invalid.Fields,
			})
		case errors.Is(err, service.ErrAlreadyConfigured):
			http.Redirect(w, r, "/login?flash=configured", http.StatusSeeOther)
		default:
			http.Error(w, "could not set password", http.StatusInternalServerError)
		}
		return
	}

	if !s.startSession(w, r) {
		return
	}
	http.Redirect(w, r, "/dashboard?flash=welcome", http.StatusSeeOther)
}

func (s *server) loginForm(w http.ResponseWriter, r *http.Request) {
	if !s.readyAuth(w) {
		return
	}

	set, err := s.auth.PasswordSet(r.Context())
	if err != nil {
		http.Error(w, "could not load login", http.StatusInternalServerError)
		return
	}
	if !set {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	valid, err := s.currentSession(r)
	if err != nil {
		http.Error(w, "could not load login", http.StatusInternalServerError)
		return
	}
	if valid {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	s.render(w, http.StatusOK, "login.html", authView{
		PageData: PageData{Title: "Sign in", Flash: flashMessage(r)},
	})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.readyAuth(w) {
		return
	}
	if !parseForm(w, r) {
		return
	}

	err := s.auth.Login(r.Context(), clientKey(r), r.PostFormValue("password"))
	if err != nil {
		view := authView{PageData: PageData{Title: "Sign in"}}
		switch {
		case errors.Is(err, service.ErrRateLimited):
			view.Error = "Too many attempts. Try again later."
			s.render(w, http.StatusTooManyRequests, "login.html", view)
		case errors.Is(err, service.ErrInvalidCredentials):
			view.Error = "Incorrect password."
			s.render(w, http.StatusUnauthorized, "login.html", view)
		default:
			http.Error(w, "could not sign in", http.StatusInternalServerError)
		}
		return
	}

	if !s.startSession(w, r) {
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.readyAuth(w) {
		return
	}
	if !parseForm(w, r) {
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	http.Redirect(w, r, "/login?flash=signed_out", http.StatusSeeOther)
}

// currentSession reports whether the request carries a valid session cookie.
func (s *server) currentSession(r *http.Request) (bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if errors.Is(err, http.ErrNoCookie) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.auth.ValidateSession(r.Context(), cookie.Value)
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request) bool {
	token, expires, err := s.auth.NewSession(r.Context())
	if err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(s.auth.SessionTTL().Seconds()),
	})
	return true
}

// readyAuth guards the auth handlers: the rest of the application can be
// health-only, but login/setup/logout need the auth service.
func (s *server) readyAuth(w http.ResponseWriter) bool {
	if !s.ready(w) {
		return false
	}
	if s.auth == nil {
		http.Error(w, "authentication is not configured", http.StatusInternalServerError)
		return false
	}
	return true
}

// clientKey is the rate-limit bucket. Behind the reverse proxy every request
// shares the proxy's loopback address, which is fine for a single user.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
