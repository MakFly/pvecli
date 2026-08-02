package secret

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// This file resolves the PVE API token secret.
//
// The original rule was "the environment, and nothing else". The reasoning was
// right — a flag is visible in `ps` to every user of the machine and stays in
// the shell history; a secret written to a config file outlives the session
// that needed it. But the consequence was that every new shell, every cron,
// every agent session started unable to talk to the node, and the workaround
// people actually reach for is pasting the export into a dotfile. That is the
// config-file failure mode with extra steps and no one watching.
//
// So the rule is kept and widened by exactly two sources that still never put
// the value on disk in clear text, nor in argv:
//
//	1. the environment                      unchanged, still wins
//	2. a command whose stdout is the secret  pass, gopass, vault, 1password…
//	3. the OS keyring                        libsecret, Keychain
//
// What the config file gains is the *name* of the source. Never the value.

// Source names where a secret came from, or where it should be looked for.
type Source string

const (
	// SourceAuto tries every source in order. It is what an unconfigured
	// context does, and what most people should leave it at.
	SourceAuto Source = ""
	// SourceEnv reads the environment variable only.
	SourceEnv Source = "env"
	// SourceCommand runs a command and takes its stdout.
	SourceCommand Source = "command"
	// SourceKeyring asks the OS keyring.
	SourceKeyring Source = "keyring"
)

// Valid reports whether s is a source a human may write into the config.
func (s Source) Valid() bool {
	switch s {
	case SourceAuto, SourceEnv, SourceCommand, SourceKeyring:
		return true
	}
	return false
}

// SourceNames lists the writable values, for error messages and completion.
var SourceNames = []string{string(SourceEnv), string(SourceCommand), string(SourceKeyring)}

// Environment variable names. EnvSecret is the historical one and keeps its
// precedence: a shell that already exports it must behave exactly as before.
const (
	EnvSecret        = "PVE_API_TOKEN_SECRET"
	EnvSecretCommand = "PVE_API_TOKEN_SECRET_COMMAND"
)

// Request is what the resolver needs: which context is being opened, and what
// that context declared about where its secret lives.
type Request struct {
	// Context is the pvecli context name. The keyring entry is keyed by it, so
	// two contexts on one machine never share one secret.
	Context string
	// Source restricts the lookup. Empty tries everything.
	Source Source
	// Command overrides EnvSecretCommand for this context.
	Command string
}

// Result is a resolved secret and where it came from. Origin is fit to print:
// it never contains the value.
type Result struct {
	Secret string
	Source Source
	Origin string
}

// ErrNotFound means no source held a secret. It is not the failure of any one
// source — most will simply have had nothing to say.
var ErrNotFound = errors.New("aucune source n'a fourni le secret du token")

// Resolve walks the sources in precedence order and returns the first hit.
//
// A source that is *configured but broken* is an error, not a silent skip: if
// someone set `secret_source: command` and the command exits non-zero, they
// need to hear that, not watch the tool quietly fall through to a stale
// keyring entry and authenticate as something they did not intend. A source
// that is merely *absent* — no env var, no keyring daemon — is skipped.
func Resolve(req Request) (Result, error) {
	if !req.Source.Valid() {
		return Result{}, fmt.Errorf("source de secret inconnue %q ; valeurs acceptées : %s",
			req.Source, strings.Join(SourceNames, ", "))
	}

	// An explicit source is exclusive: it is a statement about where the
	// secret is, so falling back elsewhere would hide the misconfiguration
	// instead of reporting it.
	if req.Source != SourceAuto {
		res, ok, err := trySource(req.Source, req)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, ErrNotFound
		}
		return res, nil
	}

	for _, s := range []Source{SourceEnv, SourceCommand, SourceKeyring} {
		res, ok, err := trySource(s, req)
		if err != nil {
			return Result{}, err
		}
		if ok {
			return res, nil
		}
	}
	return Result{}, ErrNotFound
}

func trySource(s Source, req Request) (Result, bool, error) {
	switch s {
	case SourceEnv:
		if v := os.Getenv(EnvSecret); v != "" {
			return Result{Secret: v, Source: SourceEnv, Origin: "env " + EnvSecret}, true, nil
		}
		return Result{}, false, nil

	case SourceCommand:
		cmdline := req.Command
		origin := "commande du contexte"
		if cmdline == "" {
			cmdline, origin = os.Getenv(EnvSecretCommand), "env "+EnvSecretCommand
		}
		if cmdline == "" {
			return Result{}, false, nil
		}
		v, err := runSecretCommand(cmdline)
		if err != nil {
			return Result{}, false, err
		}
		if v == "" {
			return Result{}, false, fmt.Errorf(
				"la commande de secret n'a rien écrit sur sa sortie standard : %s", cmdline)
		}
		return Result{Secret: v, Source: SourceCommand, Origin: origin}, true, nil

	case SourceKeyring:
		kr := OpenKeyring()
		if kr == nil {
			return Result{}, false, nil
		}
		v, err := kr.Get(req.Context)
		if err != nil {
			return Result{}, false, err
		}
		if v == "" {
			return Result{}, false, nil
		}
		return Result{
			Secret: v,
			Source: SourceKeyring,
			Origin: fmt.Sprintf("trousseau %s (contexte « %s »)", kr.Name(), req.Context),
		}, true, nil
	}
	return Result{}, false, nil
}

// runSecretCommand executes cmdline through a shell and returns its stdout.
//
// Through a shell on purpose: what people already have is a shell one-liner
// (`pass show pve/token`, `op read op://vault/pve/secret`), and re-implementing
// quoting here would only produce a subtly different language for them to trip
// on. The command comes from the operator's own config file or environment, so
// it carries exactly the trust of that file — it is not an injection surface,
// it *is* the operator speaking.
func runSecretCommand(cmdline string) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", cmdline)
	// stderr belongs to the operator: a password manager asking for a
	// passphrase writes there, and swallowing it would look like a hang.
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("la commande de secret a échoué (%s) : %w", cmdline, err)
	}
	// A trailing newline is what every one of these tools emits. A PVE token
	// secret is a UUID — it has no meaningful surrounding whitespace to lose.
	return strings.TrimSpace(string(out)), nil
}

// StoreToken puts the token secret in the keyring for a context.
func StoreToken(context, value string) error {
	kr := OpenKeyring()
	if kr == nil {
		return noKeyring()
	}
	return kr.Set(context, value)
}

// EraseToken removes a context's token secret from the keyring.
func EraseToken(context string) error {
	kr := OpenKeyring()
	if kr == nil {
		return noKeyring()
	}
	return kr.Delete(context)
}

func noKeyring() error {
	return fmt.Errorf(`aucun trousseau joignable sur cette machine.

Sous Linux il faut « secret-tool » (paquet libsecret-tools) et un service
Secret Service qui écoute sur D-Bus (gnome-keyring, KeePassXC, KWallet).

  sudo apt install libsecret-tools

Sans trousseau, les deux autres sources restent disponibles :

  export %s="…"
  export %s="pass show pve/token"`, EnvSecret, EnvSecretCommand)
}

// MissingHint is the message shown when nothing answered. It lists every
// source rather than only the historical one, so the reader learns what the
// tool can actually do instead of only what it used to do.
func MissingHint(context string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Trois sources sont consultées, dans cet ordre, et aucune n'a répondu
pour le contexte « %s » :

  1. env %s
     export %s="…"

  2. une commande dont la sortie standard EST le secret
     export %s="pass show pve/token"
     ou, durablement :  pvecli config set secret_command "pass show pve/token"

  3. le trousseau du système
     pvecli auth set-secret

Aucune de ces sources n'écrit le secret dans le fichier de configuration, et
aucune ne le fait passer par la ligne de commande — c'est pour ça qu'il n'y a
toujours pas de drapeau --token-secret.`,
		context, EnvSecret, EnvSecret, EnvSecretCommand)
	return b.String()
}
