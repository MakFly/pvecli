package pve

import (
	"net/http"
	"strings"
	"testing"
)

// The triage table of PRD §7.5: each status must lead somewhere actionable.
// "HTTP 403" alone tells an operator nothing they had not already seen.
func TestAPIErrorHints(t *testing.T) {
	tests := []struct {
		status   int
		wantCode int
		// Every substring must appear: these are the checks the hint exists to
		// prompt, and losing one silently is the regression to catch.
		wants []string
	}{
		{http.StatusUnauthorized, ExitAuth, []string{"format", "realm", "expiration"}},
		{http.StatusForbidden, ExitAuth, []string{"ACL", "access whoami", "privsep", "INTERSECTION"}},
		{http.StatusBadRequest, ExitGeneric, []string{"schéma", "pvesh usage", "version"}},
		{http.StatusNotFound, ExitGeneric, []string{"node ls", "vmid", "VERSION"}},
	}

	for _, tc := range tests {
		e := &APIError{Status: tc.status, Method: "GET", Path: "/nodes/pve/qemu"}

		if got := e.ExitCode(); got != tc.wantCode {
			t.Errorf("HTTP %d: ExitCode() = %d, want %d", tc.status, got, tc.wantCode)
		}
		msg := e.Error()
		for _, want := range tc.wants {
			if !strings.Contains(msg, want) {
				t.Errorf("HTTP %d: le diagnostic ne mentionne pas %q\n%s", tc.status, want, msg)
			}
		}
	}
}

// The 403 hint must name the path that was refused: an ACL is fixed on a path.
func TestForbiddenHintNamesThePath(t *testing.T) {
	e := &APIError{Status: http.StatusForbidden, Method: "POST", Path: "/nodes/pve/qemu"}

	if !strings.Contains(e.Error(), "/nodes/pve/qemu") {
		t.Errorf("le chemin refusé doit figurer dans le diagnostic:\n%s", e.Error())
	}
}

// A lock is reported as a lock, with the command that shows what holds it.
func TestLockedResourceHint(t *testing.T) {
	e := &APIError{
		Status:  http.StatusInternalServerError,
		Method:  "POST",
		Path:    "/nodes/pve/qemu/210/status/start",
		Message: "VM is locked (backup)",
	}

	if !strings.Contains(e.Error(), "task ls --running") {
		t.Errorf("un verrou doit renvoyer vers les tâches en cours:\n%s", e.Error())
	}
}

// Every status keeps the method and the path: "request failed" is useless.
func TestAPIErrorAlwaysCarriesMethodAndPath(t *testing.T) {
	e := &APIError{Status: 418, Method: "DELETE", Path: "/nodes/pve/qemu/210"}
	msg := e.Error()

	if !strings.Contains(msg, "DELETE") || !strings.Contains(msg, "/nodes/pve/qemu/210") {
		t.Errorf("méthode et chemin doivent survivre: %s", msg)
	}
}

// PVE reports failures either as plain text or as {"errors": {...}}; both are
// worth keeping, because the node's own wording is often the precise part.
func TestServerMessage(t *testing.T) {
	tests := map[string]string{
		`{"data":null,"errors":{"vmid":"value 99 is out of range"}}`: "vmid: value 99 is out of range",
		`{"message":"Permission check failed"}`:                      "Permission check failed",
		`plain text failure`:                                         "plain text failure",
	}
	for body, want := range tests {
		if got := serverMessage([]byte(body)); got != want {
			t.Errorf("serverMessage(%s) = %q, want %q", body, got, want)
		}
	}
}

// An unknown certificate and a changed one must never read alike.
func TestCertErrorMessagesAreDistinct(t *testing.T) {
	unknown := (&CertError{Host: "pve:8006", Reason: CertUnknown}).Error()
	changed := (&CertError{Host: "pve:8006", Reason: CertChanged, Want: "AA", Got: "BB"}).Error()

	if !strings.Contains(unknown, "config trust") {
		t.Errorf("un certificat inconnu doit proposer la commande: %s", unknown)
	}
	if strings.Contains(unknown, "CHANGÉ") {
		t.Errorf("un certificat inconnu ne doit pas parler de changement: %s", unknown)
	}
	if !strings.Contains(changed, "openssl") {
		t.Errorf("un certificat changé doit donner la vérification à faire sur le nœud: %s", changed)
	}
	if (&CertError{}).ExitCode() != ExitGeneric {
		t.Error("code de sortie d'une erreur TLS")
	}
}

func TestAuthErrorWithoutHint(t *testing.T) {
	e := &AuthError{Reason: "pas de secret"}
	if e.Error() != "pas de secret" {
		t.Errorf("Error() = %q", e.Error())
	}
	if e.ExitCode() != ExitAuth {
		t.Errorf("ExitCode() = %d, want %d", e.ExitCode(), ExitAuth)
	}
}
