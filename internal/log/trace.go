// Package log traces HTTP exchanges on stderr under --verbose, with secrets
// removed (PRD §7.2, §9).
//
// The redaction here is the second line of defence, not the first: the API
// client already strips the Authorization header before handing anything over.
// Both exist because a secret almost never leaks through business logic — it
// leaks through debug output written in a hurry.
package log

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Level is how much of an exchange gets written.
type Level int

const (
	// Off traces nothing.
	Off Level = iota
	// Basic traces one line per exchange: method, URL, status, duration.
	Basic
	// Full adds headers and bodies.
	Full
)

// LevelFor maps a repeated --verbose count to a level.
func LevelFor(count int) Level {
	switch {
	case count <= 0:
		return Off
	case count == 1:
		return Basic
	default:
		return Full
	}
}

// sensitiveFields are the JSON/urlencoded keys whose value never gets printed.
// `value` is there because that is what POST /access/users/{u}/token/{t}
// returns: the one and only time a token secret crosses the wire.
var sensitiveFields = []string{
	"password", "ticket", "csrf", "csrfpreventiontoken",
	"token_secret", "secret", "value", "privatekey", "private_key", "keys",
}

var (
	// "field": "value"  /  "field":"value"
	jsonFieldRe = regexp.MustCompile(`(?i)"(` + strings.Join(sensitiveFields, "|") + `)"\s*:\s*"[^"]*"`)
	// field=value in a urlencoded body or a header
	formFieldRe = regexp.MustCompile(`(?i)\b(` + strings.Join(sensitiveFields, "|") + `)=([^&\s;]+)`)
	// PVEAPIToken=user@realm!name=secret, wherever it turns up.
	tokenRe = regexp.MustCompile(`PVEAPIToken=\S+`)
)

// redactToken keeps the token id and blanks the secret.
//
// The id is not a secret, and it is exactly what the 401 diagnostic asks the
// operator to check — realm included. The split is on the LAST '=', mirroring
// what PVE::AccessControl::verify_token does with its greedy /^(.*)=(.*)$/:
// redacting on a different boundary than the server parses on would eventually
// print half a secret.
func redactToken(match string) string {
	value := strings.TrimPrefix(match, "PVEAPIToken=")
	if i := strings.LastIndex(value, "="); i > 0 {
		return "PVEAPIToken=" + value[:i] + "=<redacted>"
	}
	// No second '=' means a malformed header — which is precisely the case
	// where the value is likely to be a raw secret pasted by mistake.
	return "PVEAPIToken=<redacted>"
}

// Tracer writes redacted HTTP traces.
type Tracer struct {
	out   io.Writer
	level Level

	// literals are exact strings that must never appear, whatever the shape of
	// the payload carrying them. Pattern matching handles the general case;
	// this handles the case nobody predicted.
	literals []string
}

// New builds a tracer. literals are secrets known to the caller — the token
// secret, typically — which are blanked wherever they appear.
func New(out io.Writer, level Level, literals ...string) *Tracer {
	kept := make([]string, 0, len(literals))
	for _, s := range literals {
		// A very short "secret" would blank half the trace; it is also not a
		// secret worth protecting.
		if len(s) >= 8 {
			kept = append(kept, s)
		}
	}
	return &Tracer{out: out, level: level, literals: kept}
}

// Enabled reports whether anything gets written at all.
func (t *Tracer) Enabled() bool { return t != nil && t.level > Off }

// Request traces an outgoing request.
func (t *Tracer) Request(method, url string, header http.Header, body []byte) {
	if !t.Enabled() {
		return
	}
	_, _ = fmt.Fprintf(t.out, "→ %s %s\n", method, t.Redact(url))
	if t.level >= Full {
		t.writeHeaders(header)
		t.writeBody(body)
	}
}

// Response traces an answer and how long it took.
func (t *Tracer) Response(status int, took time.Duration, header http.Header, body []byte) {
	if !t.Enabled() {
		return
	}
	_, _ = fmt.Fprintf(t.out, "← %d en %s\n", status, took.Round(time.Millisecond))
	if t.level >= Full {
		t.writeHeaders(header)
		t.writeBody(body)
	}
}

func (t *Tracer) writeHeaders(h http.Header) {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(t.out, "    %s: %s\n", name, t.Redact(strings.Join(h[name], ", ")))
	}
}

func (t *Tracer) writeBody(body []byte) {
	if len(body) == 0 {
		return
	}
	const maxTrace = 4096
	shown := string(body)
	truncated := ""
	if len(shown) > maxTrace {
		shown, truncated = shown[:maxTrace], fmt.Sprintf(" … (%d octets tronqués)", len(body)-maxTrace)
	}
	_, _ = fmt.Fprintf(t.out, "    %s%s\n", t.Redact(shown), truncated)
}

// Redact removes every secret it can recognise from s.
func (t *Tracer) Redact(s string) string {
	for _, literal := range t.literals {
		s = strings.ReplaceAll(s, literal, "<redacted>")
	}
	s = tokenRe.ReplaceAllStringFunc(s, redactToken)
	s = jsonFieldRe.ReplaceAllString(s, `"$1":"<redacted>"`)
	s = formFieldRe.ReplaceAllString(s, "$1=<redacted>")
	return s
}
