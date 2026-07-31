package pve

import (
	"fmt"
	"net/http"
	"strings"
)

// Exit codes of PRD §7.5. A script reading $? must be able to tell "I don't
// know who you are" from "the node refused the job".
const (
	ExitOK      = 0
	ExitGeneric = 1
	ExitUsage   = 2
	ExitAuth    = 3
	ExitTask    = 4
	ExitConfirm = 5
)

// AuthError is an authentication failure detected locally, before any request
// leaves the machine — typically a missing secret.
type AuthError struct {
	Reason string
	Hint   string
}

func (e *AuthError) Error() string {
	if e.Hint == "" {
		return e.Reason
	}
	return e.Reason + "\n\n" + e.Hint
}

// ExitCode implements the contract main relies on.
func (e *AuthError) ExitCode() int { return ExitAuth }

// APIError is a non-2xx answer from the node, carrying what to check next.
//
// The hint is the point. "HTTP 403" tells an operator nothing they did not
// already see; "which ACL, on which path, and mind privilege separation" is
// the difference between a five-minute fix and an afternoon.
type APIError struct {
	Status int
	Method string
	Path   string

	// Message is what the node said. It is shown in the short form too,
	// because PVE's own wording is often the most precise part.
	Message string

	// Raw is the untouched response body, surfaced only under --verbose
	// (PVX-009) and never containing a secret: the client never sends one in
	// a body, and redaction covers the rest.
	Raw string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s : HTTP %d", e.Method, e.Path, e.Status)
	if e.Message != "" {
		fmt.Fprintf(&b, " — %s", e.Message)
	}
	if hint := e.Hint(); hint != "" {
		fmt.Fprintf(&b, "\n\n%s", hint)
	}
	return b.String()
}

// Hint returns the diagnostic lead for this status, per the triage table of
// PRD §7.5.
func (e *APIError) Hint() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return `Authentification refusée — le nœud ne sait pas qui tu es. À vérifier, dans cet ordre :
  · le format de l'en-tête : PVEAPIToken=<user>@<realm>!<nom>=<secret>
  · le token_id, realm compris (« automation@pve!pvectl », pas « automation!pvectl »)
  · le secret : présent dans PVE_API_TOKEN_SECRET, et sans espace parasite
  · l'expiration du token — un token expiré répond 401, pas 403`

	case http.StatusForbidden:
		return fmt.Sprintf(`Privilège manquant sur « %s » — le nœud sait qui tu es et refuse.
Changer de token ne corrigera rien : c'est une ACL qu'il faut corriger.
  · quels droits ai-je réellement :  pvectl access whoami
  · l'ACL est-elle posée sur le bon chemin, et propage-t-elle ?
  · privilege separation : avec privsep=1, les droits effectifs du token sont
    l'INTERSECTION de ceux du token et de ceux de son utilisateur. Une ACL sur
    le seul token ne suffit pas.`, e.Path)

	case http.StatusBadRequest:
		// The query string is stripped: `pvesh usage` takes a path, and
		// pasting the whole URL into it produces a second, confusing error.
		path, _, _ := strings.Cut(e.Path, "?")
		return fmt.Sprintf(`Paramètre invalide — le schéma attendu n'est pas celui envoyé.
Ne devine pas : compare avec le schéma réel de cette version de PVE.
  pvesh usage %s -v          (depuis le nœud)
Attention à la version : un paramètre présent en 8.x peut avoir disparu en 9.x.`, path)

	case http.StatusNotFound:
		return `Ressource absente. Dans l'ordre de probabilité :
  · le nom du nœud est faux         → pvectl node ls
  · le vmid ou le storage n'existe pas
  · l'endpoint n'existe pas DANS CETTE VERSION de PVE — c'est le cas le plus
    fréquent et le plus déroutant. Vérifie contre le schéma de la version
    détectée (« detected_version » dans la configuration).`

	case http.StatusInternalServerError:
		if strings.Contains(strings.ToLower(e.Message), "lock") {
			return "Ressource verrouillée — une tâche est probablement en cours :\n  pvectl task ls --running"
		}
		return "Erreur interne du nœud — le message ci-dessus vient de PVE ; le log de tâche en dira plus."

	default:
		return ""
	}
}

// ExitCode maps the status to the table of PRD §7.5.
func (e *APIError) ExitCode() int {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ExitAuth
	default:
		return ExitGeneric
	}
}
