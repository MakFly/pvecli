package secret

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Keyring is the OS credential store, for the one secret this tool reads back:
// the PVE API token.
//
// This is deliberately separate from Store/Read above. Those file *generated*
// service passwords and accept the macOS weakness of passing the value in argv.
// The API token may not: it is the credential that opens the whole node, and
// the CLI has always refused to let it near a command line. So every write here
// goes through stdin, and a platform that cannot offer that does not get a
// writer at all.
type Keyring interface {
	// Name is what the provenance line calls this store.
	Name() string
	// Get returns the secret for a context, or "" when there is no entry.
	// A missing entry is not an error — several sources are tried in turn.
	Get(context string) (string, error)
	// Set writes the secret. Backends that cannot write it without exposing
	// it in argv return ErrWriteUnsupported instead of doing it anyway.
	Set(context, value string) error
	// Delete removes the entry. Deleting what is not there succeeds.
	Delete(context string) error
}

// ErrWriteUnsupported is returned by a backend that can read a secret but
// cannot accept one without putting it somewhere `ps` would see. macOS is the
// case: `security add-generic-password` takes the password as an argument and
// offers no stdin form.
var ErrWriteUnsupported = errors.New("ce trousseau ne peut pas recevoir le secret sans l'exposer dans la ligne de commande")

// keyringService is the attribute every pvecli entry shares, so one `clear`
// cannot reach another application's secrets.
const keyringService = "pvecli"

// OpenKeyring returns the keyring for this platform, or nil when none is
// reachable. Nil is a normal answer: a container, a cron, a CI runner have no
// keyring and must fall through to another source rather than fail.
//
// A variable so tests can install a fake without a D-Bus session.
var OpenKeyring = func() Keyring {
	switch runtime.GOOS {
	case "linux":
		if lookPath("secret-tool") == nil {
			return secretTool{}
		}
	case "darwin":
		if lookPath("security") == nil {
			return macSecurity{}
		}
	}
	return nil
}

// ── Linux: libsecret via secret-tool ────────────────────────────────────────
//
// secret-tool reads the secret on stdin and writes it on stdout, so the value
// never appears in argv. Only the attributes do, and those are not secret.

type secretTool struct{}

func (secretTool) Name() string { return "libsecret" }

func (secretTool) Get(context string) (string, error) {
	cmd := exec.Command("secret-tool", "lookup", "service", keyringService, "context", context)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		// secret-tool exits 1 with no output when the attributes match
		// nothing. That is "no entry", not a failure.
		if errors.As(err, &exit) && stderr.Len() == 0 {
			return "", nil
		}
		return "", keyringErr("lecture", err, stderr.String())
	}
	// lookup emits the value with no trailing newline, but a value stored by
	// another tool may carry one.
	return strings.TrimRight(string(out), "\n"), nil
}

func (secretTool) Set(context, value string) error {
	cmd := exec.Command("secret-tool", "store",
		"--label", "pvecli — secret du token d'API (contexte "+context+")",
		"service", keyringService, "context", context)
	cmd.Stdin = strings.NewReader(value)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return keyringErr("écriture", err, stderr.String())
	}
	return nil
}

func (secretTool) Delete(context string) error {
	cmd := exec.Command("secret-tool", "clear", "service", keyringService, "context", context)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// clear on a missing entry exits non-zero and says nothing; that is
		// the outcome the caller asked for.
		var exit *exec.ExitError
		if errors.As(err, &exit) && stderr.Len() == 0 {
			return nil
		}
		return keyringErr("suppression", err, stderr.String())
	}
	return nil
}

// ── macOS: Keychain via security ────────────────────────────────────────────

type macSecurity struct{}

func (macSecurity) Name() string { return "Keychain" }

func (macSecurity) Get(context string) (string, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", keyringService, "-a", context, "-w")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		// 44 = SecItemNotFound. No entry, not a failure.
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return "", nil
		}
		return "", keyringErr("lecture", err, stderr.String())
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Set refuses. `security add-generic-password` takes the password as an
// argument, and this is the one secret that may not travel that way — the CLI
// has refused a --token-secret flag since the first release for exactly this
// reason, and going around it here would make that refusal theatre.
//
// The operator runs the command themselves, once, from their own shell.
func (macSecurity) Set(string, string) error { return ErrWriteUnsupported }

func (macSecurity) Delete(context string) error {
	cmd := exec.Command("security", "delete-generic-password",
		"-s", keyringService, "-a", context)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return nil
		}
		return keyringErr("suppression", err, stderr.String())
	}
	return nil
}

// WriteHint is what to tell someone whose backend cannot be written to.
func WriteHint(context string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return fmt.Sprintf(
		"Le trousseau macOS n'accepte un mot de passe qu'en argument de commande,\n"+
			"et le secret du token ne doit pas passer par là. Range-le toi-même, une\n"+
			"fois, depuis ton shell — « -w » sans valeur demande la saisie sans écho :\n\n"+
			"  security add-generic-password -U -s %s -a %s -w\n\n"+
			"Puis déclare la source :\n\n"+
			"  pvecli config set secret_source keyring",
		keyringService, context)
}

// keyringErr keeps the backend's own words: "Cannot create an item in a locked
// collection" is the entire diagnosis when a keyring is locked, and losing it
// behind a generic message is what turns a 30-second fix into an afternoon.
func keyringErr(action string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("%s dans le trousseau : %w", action, err)
	}
	return fmt.Errorf("%s dans le trousseau : %s", action, stderr)
}
