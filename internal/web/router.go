// Package web wires the HTTP routes and handlers.
package web

import (
	"encoding/json"
	"net/http"
)

// NewRouter returns the application's HTTP handler.
func NewRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// The status line is already sent; a write error is not actionable.
	_ = json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"})
}
