package log

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The secret injected everywhere a secret could plausibly end up. The test is
// crude on purpose: it scans the entire output for this literal.
const secret = "11111111-2222-3333-4444-555555555555"

// The anti-leak test of PRD §9. It does not check that redaction was applied in
// the places we thought of — it checks the whole trace, byte for byte.
func TestNoSecretLeak(t *testing.T) {
	var buf bytes.Buffer
	tr := New(&buf, Full, secret)

	header := http.Header{
		"Authorization": {"PVEAPIToken=automation@pve!pvectl=" + secret},
		"Cookie":        {"PVEAuthCookie=PVE:root@pam:" + secret},
	}
	body := []byte(`{"password":"` + secret + `","ticket":"` + secret +
		`","value":"` + secret + `","data":{"csrf":"` + secret + `"}}`)

	tr.Request("POST", "https://pve:8006/api2/json/access/ticket?password="+secret, header, body)
	tr.Response(200, 12*time.Millisecond, header, body)

	if out := buf.String(); strings.Contains(out, secret) {
		t.Errorf("le secret apparaît dans la trace :\n%s", out)
	}
	if !strings.Contains(buf.String(), "<redacted>") {
		t.Error("rien n'a été masqué — le test ne prouve rien")
	}
}

// The token id survives redaction: it is not a secret, and the 401 diagnostic
// tells the operator to go and check it.
func TestRedactKeepsTokenID(t *testing.T) {
	tr := New(nil, Full)

	got := tr.Redact("Authorization: PVEAPIToken=automation@pve!pvectl=" + secret)

	if !strings.Contains(got, "automation@pve!pvectl") {
		t.Errorf("l'identifiant du token doit rester lisible: %s", got)
	}
	if strings.Contains(got, secret) {
		t.Errorf("le secret a fui: %s", got)
	}
}

// A malformed header — no second '=' — is the case where the value is most
// likely a raw secret pasted by mistake. Blank the lot.
func TestRedactMalformedTokenHeader(t *testing.T) {
	tr := New(nil, Full)

	if got := tr.Redact("PVEAPIToken=" + secret); strings.Contains(got, secret) {
		t.Errorf("un en-tête malformé doit être masqué entièrement: %s", got)
	}
}

// Redaction must not undo itself when applied twice.
func TestRedactIsIdempotent(t *testing.T) {
	tr := New(nil, Full)
	in := "PVEAPIToken=automation@pve!pvectl=" + secret

	once := tr.Redact(in)
	if twice := tr.Redact(once); twice != once {
		t.Errorf("Redact n'est pas idempotent:\n  1× %s\n  2× %s", once, twice)
	}
}

func TestLevels(t *testing.T) {
	var buf bytes.Buffer

	New(&buf, Off).Request("GET", "https://pve:8006/api2/json/version", nil, nil)
	if buf.Len() != 0 {
		t.Errorf("sans --verbose, rien ne doit être écrit: %q", buf.String())
	}

	buf.Reset()
	New(&buf, Basic).Request("GET", "https://pve:8006/api2/json/version",
		http.Header{"Authorization": {"PVEAPIToken=a@pve!b=" + secret}}, nil)
	if strings.Contains(buf.String(), "Authorization") {
		t.Errorf("-v ne doit pas afficher les en-têtes: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "/version") {
		t.Errorf("-v doit afficher méthode et URL: %q", buf.String())
	}

	if got := LevelFor(0); got != Off {
		t.Errorf("LevelFor(0) = %v", got)
	}
	if got := LevelFor(1); got != Basic {
		t.Errorf("LevelFor(1) = %v", got)
	}
	if got := LevelFor(3); got != Full {
		t.Errorf("LevelFor(3) = %v", got)
	}
}

// A short literal must not be used as a redaction pattern: it would blank
// unrelated text and make the trace useless.
func TestShortLiteralsIgnored(t *testing.T) {
	tr := New(nil, Full, "pve")

	if got := tr.Redact("GET /nodes/pve/status"); !strings.Contains(got, "pve") {
		t.Errorf("un littéral trop court ne doit pas être appliqué: %s", got)
	}
}
