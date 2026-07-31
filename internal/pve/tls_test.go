package pve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tlsServer returns a TLS test server answering a valid /version payload, plus
// the SHA-256 of the certificate it presents.
func tlsServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
	}))
	t.Cleanup(srv.Close)

	return srv, Fingerprint(srv.Certificate())
}

func get(t *testing.T, trust TrustOptions, endpoint string) error {
	t.Helper()

	c, err := New(Options{
		Endpoint: endpoint,
		TokenID:  "automation@pve!pvectl",
		Secret:   "s3cr3t",
		Trust:    trust,
	})
	if err != nil {
		return err
	}
	_, err = c.Version(context.Background())
	return err
}

// The recommended mode: a self-signed certificate, verified for real.
func TestPinnedFingerprintAccepted(t *testing.T) {
	srv, fp := tlsServer(t)

	if err := get(t, TrustOptions{Fingerprint: fp}, srv.URL); err != nil {
		t.Fatalf("l'empreinte correcte doit être acceptée: %v", err)
	}
}

// A pinned certificate that changed is an incident, and the message must say
// so — not "certificate verify failed".
func TestPinnedFingerprintMismatchIsReportedAsChanged(t *testing.T) {
	srv, _ := tlsServer(t)
	const other = "9F:3D:1A:7C:42:B8:E0:55:6D:19:C4:88:2F:A1:73:0B:E6:5C:94:D2:38:AF:61:07:CB:4E:80:15:9A:D7:23:6F"

	err := get(t, TrustOptions{Fingerprint: other}, srv.URL)

	var certErr *CertError
	if !errors.As(err, &certErr) {
		t.Fatalf("erreur = %T (%v), want *CertError", err, err)
	}
	if certErr.Reason != CertChanged {
		t.Errorf("Reason = %v, want CertChanged", certErr.Reason)
	}
	if !strings.Contains(certErr.Error(), "CHANGÉ") {
		t.Errorf("le message doit distinguer un changement d'un inconnu, got:\n%s", certErr)
	}
}

// First contact with a self-signed lab: unknown, not changed, and the fix is
// spelled out.
func TestUnknownCertificateSuggestsTrust(t *testing.T) {
	srv, _ := tlsServer(t)

	err := get(t, TrustOptions{}, srv.URL)

	var certErr *CertError
	if !errors.As(err, &certErr) {
		t.Fatalf("erreur = %T (%v), want *CertError", err, err)
	}
	if certErr.Reason != CertUnknown {
		t.Errorf("Reason = %v, want CertUnknown", certErr.Reason)
	}
	if !strings.Contains(certErr.Error(), "pvectl config trust") {
		t.Errorf("le message doit proposer la commande exacte, got:\n%s", certErr)
	}
}

func TestInsecureSkipsVerification(t *testing.T) {
	srv, _ := tlsServer(t)

	if err := get(t, TrustOptions{Insecure: true}, srv.URL); err != nil {
		t.Fatalf("--insecure doit fonctionner: %v", err)
	}
}

// --insecure is an explicit override and beats a configured fingerprint;
// otherwise a stale config would silently keep verification on.
func TestTrustModePrecedence(t *testing.T) {
	tests := []struct {
		opts TrustOptions
		want TrustMode
	}{
		{TrustOptions{}, TrustSystem},
		{TrustOptions{Fingerprint: "AA"}, TrustFingerprint},
		{TrustOptions{CAFile: "/tmp/ca.pem"}, TrustCAFile},
		{TrustOptions{Fingerprint: "AA", CAFile: "/tmp/ca.pem"}, TrustFingerprint},
		{TrustOptions{Fingerprint: "AA", Insecure: true}, TrustNone},
	}
	for _, tc := range tests {
		if got := tc.opts.Mode(); got != tc.want {
			t.Errorf("%+v.Mode() = %q, want %q", tc.opts, got, tc.want)
		}
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	const bare = "9F3D1A7C42B8E0556D19C4882FA1730BE65C94D238AF6107CB4E80159AD7236F"

	for _, in := range []string{
		bare,
		strings.ToLower(bare),
		"9F:3D:1A:7C:42:B8:E0:55:6D:19:C4:88:2F:A1:73:0B:E6:5C:94:D2:38:AF:61:07:CB:4E:80:15:9A:D7:23:6F",
	} {
		got, err := normalizeFingerprint(in)
		if err != nil {
			t.Fatalf("normalizeFingerprint(%q): %v", in, err)
		}
		if got != bare {
			t.Errorf("normalizeFingerprint(%q) = %q", in, got)
		}
	}

	for _, bad := range []string{"", "AA:BB", "zz" + bare[2:]} {
		if _, err := normalizeFingerprint(bad); err == nil {
			t.Errorf("normalizeFingerprint(%q) doit échouer", bad)
		}
	}
}

func TestFetchCertificate(t *testing.T) {
	srv, fp := tlsServer(t)

	cert, err := FetchCertificate(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchCertificate: %v", err)
	}
	if got := Fingerprint(cert); got != fp {
		t.Errorf("Fingerprint = %q, want %q", got, fp)
	}
}
