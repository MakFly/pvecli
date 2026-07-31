package pve

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// TrustMode names how the server certificate is verified.
type TrustMode string

const (
	// TrustSystem is the default: the certificate must chain to a CA the
	// system already trusts.
	TrustSystem TrustMode = "vérification standard"
	// TrustFingerprint pins one certificate by its SHA-256. This is the
	// recommended mode for a lab: real verification, no CA to run.
	TrustFingerprint TrustMode = "empreinte épinglée"
	// TrustCAFile verifies against a CA bundle provided by the operator.
	TrustCAFile TrustMode = "CA fournie"
	// TrustNone verifies nothing. It works, and it warns, every time.
	TrustNone TrustMode = "AUCUNE (--insecure)"
)

// TrustOptions describes how a client should verify the node.
type TrustOptions struct {
	Fingerprint string
	CAFile      string
	Insecure    bool
}

// Mode reports which of the four behaviours the options select. Order matters:
// --insecure is an explicit override and wins, then pinning, then a CA.
func (t TrustOptions) Mode() TrustMode {
	switch {
	case t.Insecure:
		return TrustNone
	case t.Fingerprint != "":
		return TrustFingerprint
	case t.CAFile != "":
		return TrustCAFile
	default:
		return TrustSystem
	}
}

// tlsConfig turns the options into a *tls.Config.
func tlsConfig(t TrustOptions, host string) (*tls.Config, error) {
	switch t.Mode() {
	case TrustNone:
		//nolint:gosec // deliberate: --insecure is a documented, warned-about mode
		return &tls.Config{InsecureSkipVerify: true}, nil

	case TrustFingerprint:
		want, err := normalizeFingerprint(t.Fingerprint)
		if err != nil {
			return nil, err
		}
		// Go's own chain verification is switched off because a self-signed
		// lab certificate can never satisfy it. Verification is not skipped:
		// it is replaced by an identity check that is strictly stronger for a
		// single known host — this exact certificate, or nothing.
		return &tls.Config{
			InsecureSkipVerify:    true, //nolint:gosec // replaced by VerifyPeerCertificate below
			VerifyPeerCertificate: pinnedVerifier(want, host),
		}, nil

	case TrustCAFile:
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("lecture de la CA %s : %w", t.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s ne contient aucun certificat PEM exploitable", t.CAFile)
		}
		return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil

	default:
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
}

func pinnedVerifier(want, host string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return &CertError{Host: host, Reason: CertAbsent}
		}
		got := fingerprintOf(rawCerts[0])
		if got != want {
			return &CertError{Host: host, Reason: CertChanged, Want: want, Got: got}
		}
		return nil
	}
}

// CertReason distinguishes the two failures an operator must never confuse.
type CertReason int

const (
	// CertUnknown: nothing is configured and the system does not trust the
	// certificate. Expected on first contact with a self-signed lab.
	CertUnknown CertReason = iota
	// CertChanged: an empreinte is pinned and the server presented another
	// one. Either the node was reinstalled, or someone is between you and it.
	CertChanged
	// CertAbsent: the handshake produced no certificate at all.
	CertAbsent
)

// CertError is a trust failure. The distinction it carries is the whole point:
// an unknown certificate is a setup step, a changed certificate is an incident.
type CertError struct {
	Host   string
	Reason CertReason
	Want   string
	Got    string
	Err    error
}

func (e *CertError) Error() string {
	switch e.Reason {
	case CertChanged:
		return fmt.Sprintf(`le certificat de %s a CHANGÉ.

  empreinte attendue : %s
  empreinte présentée : %s

Soit le nœud a été réinstallé ou son certificat régénéré, soit quelqu'un
s'est intercalé. Ne mets pas à jour l'empreinte sans avoir vérifié laquelle
des deux hypothèses est vraie — depuis la console du nœud :

  openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -fingerprint -sha256

Si le changement est légitime :
  pvecli config trust`, e.Host, formatFingerprint(e.Want), formatFingerprint(e.Got))

	case CertAbsent:
		return fmt.Sprintf("%s n'a présenté aucun certificat", e.Host)

	default:
		return fmt.Sprintf(`le certificat de %s est inconnu du système (auto-signé ?).

C'est attendu pour un lab. Plutôt que de désactiver la vérification, épingle
son empreinte une fois pour toutes :

  pvecli config trust

La vérification restera réelle : ce certificat précis, ou aucun.
(%v)`, e.Host, e.Err)
	}
}

// ExitCode implements the contract of PRD §7.5.
func (e *CertError) ExitCode() int { return ExitGeneric }

func (e *CertError) Unwrap() error { return e.Err }

// Fingerprint returns the SHA-256 of a certificate in the colon-separated
// uppercase hexadecimal form that Proxmox itself displays.
func Fingerprint(cert *x509.Certificate) string {
	return formatFingerprint(fingerprintOf(cert.Raw))
}

// fingerprintOf hashes the DER bytes and returns bare uppercase hex, which is
// the form comparisons use.
func fingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func formatFingerprint(bare string) string {
	var b strings.Builder
	for i := 0; i < len(bare); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(bare[i : i+2])
	}
	return b.String()
}

// normalizeFingerprint accepts the colon-separated form, the bare form, and
// either case — an operator pastes whatever their tool printed.
func normalizeFingerprint(raw string) (string, error) {
	bare := strings.ToUpper(strings.NewReplacer(":", "", " ", "", "-", "").Replace(raw))
	if len(bare) != sha256.Size*2 {
		return "", fmt.Errorf("empreinte %q invalide : attendu %d caractères hexadécimaux (SHA-256), reçu %d",
			raw, sha256.Size*2, len(bare))
	}
	if _, err := hex.DecodeString(bare); err != nil {
		return "", fmt.Errorf("empreinte %q invalide : %w", raw, err)
	}
	return bare, nil
}

// FetchCertificate opens a TLS connection to the endpoint and returns the
// certificate the server presents, without verifying it.
//
// This is the one place where skipping verification is legitimate: the whole
// point is to look at an as-yet-untrusted certificate so a human can decide.
func FetchCertificate(ctx context.Context, endpoint string) (*x509.Certificate, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q illisible", endpoint)
	}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "8006")
	}

	dialer := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // see doc comment
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connexion TLS à %s : %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, &CertError{Host: addr, Reason: CertAbsent}
	}
	return state.PeerCertificates[0], nil
}
