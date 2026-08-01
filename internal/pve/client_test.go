package pve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The proof of PVX-003: the exact shape of the header, byte for byte.
func TestAuthHeader(t *testing.T) {
	got := authHeader("automation@pve!pvectl", "11111111-2222-3333-4444-555555555555")
	want := "PVEAPIToken=automation@pve!pvectl=11111111-2222-3333-4444-555555555555"

	if got != want {
		t.Errorf("authHeader() =\n  %q\nwant\n  %q", got, want)
	}
}

// newTestClient wires a client onto an httptest server: no Proxmox node needed.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New(Options{
		Endpoint:  srv.URL,
		TokenID:   "automation@pve!pvectl",
		Secret:    "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// The header must reach the wire, and no CSRF token must accompany it.
func TestClientSendsTokenAndNoCSRF(t *testing.T) {
	var gotAuth, gotCSRF, gotPath string

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCSRF = r.Header.Get("CSRFPreventionToken")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
	})

	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}

	if want := "PVEAPIToken=automation@pve!pvectl=s3cr3t"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if gotCSRF != "" {
		t.Errorf("CSRFPreventionToken = %q, il ne doit jamais être envoyé avec un token", gotCSRF)
	}
	if want := "/api2/json/version"; gotPath != want {
		t.Errorf("chemin appelé = %q, want %q", gotPath, want)
	}
}

// Behind Cloudflare Access, these two headers are the difference between
// reaching the API and being handed a login page.
func TestClientSendsTheAccessServiceTokenWhenConfigured(t *testing.T) {
	var gotID, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get(accessIDHeader)
		gotSecret = r.Header.Get(accessSecretHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "automation@pve!pvectl", Secret: "s3cr3t",
		AccessClientID: "client-id", AccessClientSecret: "client-secret",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if gotID != "client-id" || gotSecret != "client-secret" {
		t.Errorf("%s = %q, %s = %q", accessIDHeader, gotID, accessSecretHeader, gotSecret)
	}
}

// On the LAN there is no Access application. Sending the headers empty would
// still be sending them.
func TestClientOmitsTheAccessHeadersWhenNotConfigured(t *testing.T) {
	var present bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header[accessIDHeader]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
	})
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if present {
		t.Errorf("%s envoyé alors qu'aucun service token n'est configuré", accessIDHeader)
	}
}

// Half a service token produces a 403 from Cloudflare that reads exactly like a
// 403 from Proxmox. Refusing at New() is what keeps that hour from being lost.
func TestNewRefusesAHalfConfiguredServiceToken(t *testing.T) {
	_, err := New(Options{
		Endpoint: "https://pve.example.com:8006",
		TokenID:  "automation@pve!pvectl", Secret: "s3cr3t",
		AccessClientID: "client-id",
	})
	if err == nil {
		t.Fatal("New doit refuser un service token incomplet")
	}
	if !strings.Contains(err.Error(), "CF_ACCESS_CLIENT_SECRET") {
		t.Errorf("l'erreur doit nommer la variable manquante, got: %v", err)
	}
}

// The trace is the one place a secret can leave this package.
func TestTraceRedactsTheAccessSecret(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://pve.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "PVEAPIToken=automation@pve!pvectl=s3cr3t")
	req.Header.Set(accessSecretHeader, "client-secret")

	safe := redactedHeader(req, "automation@pve!pvectl")
	if got := safe.Get(accessSecretHeader); strings.Contains(got, "client-secret") {
		t.Errorf("%s = %q, le secret ne doit pas sortir du paquet", accessSecretHeader, got)
	}
	if strings.Contains(safe.Get("Authorization"), "s3cr3t") {
		t.Error("le secret du token PVE ne doit pas sortir non plus")
	}
}

// The {"data": …} envelope is unwrapped for the caller, using a real answer
// captured from the lab node.
func TestClientUnwrapsDataEnvelope(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/version.json")
	if err != nil {
		t.Fatal(err)
	}

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})

	out, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if out.Version != "9.2.2" || out.Release != "9.2" || out.RepoID != "b9984c6d90a4bd80" {
		t.Errorf("décodage = %+v", out)
	}
}

// A missing secret must be caught before a socket is opened, and must carry
// exit code 3 (PRD §7.5).
func TestNewFailsWithoutSecretBeforeAnyRequest(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	_, err := New(Options{Endpoint: srv.URL, TokenID: "automation@pve!pvectl"})
	if err == nil {
		t.Fatal("New doit échouer sans secret")
	}
	if called {
		t.Error("une requête a été émise malgré l'absence de secret")
	}

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("erreur = %T, want *AuthError", err)
	}
	if authErr.ExitCode() != ExitAuth {
		t.Errorf("ExitCode() = %d, want %d", authErr.ExitCode(), ExitAuth)
	}
	if !strings.Contains(err.Error(), "PVE_API_TOKEN_SECRET") {
		t.Errorf("l'erreur doit dire où définir le secret, got: %v", err)
	}
}

func TestAPIErrorCarriesStatusAndExitCode(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"data":null,"errors":{"path":"Permission check failed"}}`))
	})

	_, err := c.Nodes(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("erreur = %T, want *APIError", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", apiErr.Status)
	}
	if apiErr.ExitCode() != ExitAuth {
		t.Errorf("ExitCode() = %d, want %d pour un 403", apiErr.ExitCode(), ExitAuth)
	}
	if !strings.Contains(apiErr.Error(), "Permission check failed") {
		t.Errorf("le message du serveur doit remonter, got: %v", apiErr)
	}
}

// Both endpoint spellings people have on hand must work.
func TestNormalizeBase(t *testing.T) {
	for _, in := range []string{
		"https://192.0.2.23:8006",
		"https://192.0.2.23:8006/",
		"https://192.0.2.23:8006/api2/json",
		"https://192.0.2.23:8006/api2/json/",
	} {
		u, err := normalizeBase(in)
		if err != nil {
			t.Fatalf("normalizeBase(%q): %v", in, err)
		}
		if want := "https://192.0.2.23:8006/api2/json"; u.String() != want {
			t.Errorf("normalizeBase(%q) = %q, want %q", in, u.String(), want)
		}
	}

	if _, err := normalizeBase("192.0.2.23:8006"); err == nil {
		t.Error("un endpoint sans schéma doit être refusé")
	}
}

func TestDefaultTimeoutApplies(t *testing.T) {
	c, err := New(Options{Endpoint: "https://x:8006", TokenID: "a@pve!b", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", c.http.Timeout, DefaultTimeout)
	}

	c, err = New(Options{Endpoint: "https://x:8006", TokenID: "a@pve!b", Secret: "s", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.http.Timeout)
	}
}
