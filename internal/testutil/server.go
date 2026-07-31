// Package testutil replays real API answers so the whole CLI can be tested
// with no Proxmox node powered on (PRD §9).
//
// The fixtures under testdata/ are captured from the lab with `make capture`
// and anonymised. Replaying real answers rather than hand-written ones is the
// point: a hand-written fixture agrees with whatever the developer believed the
// schema was, which is exactly the failure mode this project is built to avoid.
package testutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Server serves fixtures by method and path.
type Server struct {
	*httptest.Server

	// Requests records what was asked, in order, so a test can assert on the
	// call sequence — which is how the mutation contract of PRD §5.3 gets
	// verified later (pre-read → write → poll → post-read).
	Requests []string
}

// New starts a server. routes maps "METHOD /api2/json/path" to a fixture file
// under dir; a request with no route answers 404 with a PVE-shaped body.
func New(t *testing.T, dir string, routes map[string]string) *Server {
	t.Helper()

	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		s.Requests = append(s.Requests, key)

		fixture, ok := routes[key]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"data":null,"errors":{"path":"no such resource"}}`))
			return
		}

		body, err := os.ReadFile(filepath.Join(dir, fixture))
		if err != nil {
			t.Errorf("fixture %s: %v", fixture, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)

	return s
}
