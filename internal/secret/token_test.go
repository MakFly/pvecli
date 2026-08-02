package secret

import (
	"errors"
	"strings"
	"testing"
)

// fakeKeyring stands in for the OS store so these tests never depend on a
// D-Bus session, an unlocked collection, or a Keychain prompt — the three
// things that make a real keyring untestable in CI.
type fakeKeyring struct {
	entries map[string]string
	getErr  error
	setErr  error
}

func (f *fakeKeyring) Name() string { return "faux" }
func (f *fakeKeyring) Get(ctx string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.entries[ctx], nil
}
func (f *fakeKeyring) Set(ctx, v string) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.entries == nil {
		f.entries = map[string]string{}
	}
	f.entries[ctx] = v
	return nil
}
func (f *fakeKeyring) Delete(ctx string) error { delete(f.entries, ctx); return nil }

func useKeyring(t *testing.T, kr Keyring) {
	t.Helper()
	prev := OpenKeyring
	OpenKeyring = func() Keyring { return kr }
	t.Cleanup(func() { OpenKeyring = prev })
}

// noKeyringAvailable is the container / cron / CI case: no store at all.
func noKeyringAvailable(t *testing.T) {
	t.Helper()
	prev := OpenKeyring
	OpenKeyring = func() Keyring { return nil }
	t.Cleanup(func() { OpenKeyring = prev })
}

func TestEnvWinsOverEverything(t *testing.T) {
	// The historical behaviour. A shell that already exports the variable must
	// keep working byte for byte, and must not even consult the keyring —
	// otherwise adding a source would have changed which credential is used.
	t.Setenv(EnvSecret, "depuis-env")
	t.Setenv(EnvSecretCommand, "echo depuis-commande")
	useKeyring(t, &fakeKeyring{entries: map[string]string{"lab": "depuis-trousseau"}})

	res, err := Resolve(Request{Context: "lab"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Secret != "depuis-env" {
		t.Fatalf("secret = %q, attendu depuis-env", res.Secret)
	}
	if res.Source != SourceEnv {
		t.Fatalf("source = %q, attendu env", res.Source)
	}
}

func TestEnvIsNotConsultedWhenSourceIsKeyring(t *testing.T) {
	// An explicit source is a statement about where the secret is. Falling
	// back to the environment would silently authenticate as something the
	// operator did not choose.
	t.Setenv(EnvSecret, "depuis-env")
	useKeyring(t, &fakeKeyring{entries: map[string]string{"lab": "depuis-trousseau"}})

	res, err := Resolve(Request{Context: "lab", Source: SourceKeyring})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Secret != "depuis-trousseau" {
		t.Fatalf("secret = %q, attendu depuis-trousseau", res.Secret)
	}
}

func TestCommandStdoutIsTheSecret(t *testing.T) {
	t.Setenv(EnvSecret, "")
	noKeyringAvailable(t)

	res, err := Resolve(Request{Context: "lab", Command: "printf 'uuid-du-token'"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Secret != "uuid-du-token" {
		t.Fatalf("secret = %q", res.Secret)
	}
	if res.Source != SourceCommand {
		t.Fatalf("source = %q, attendu command", res.Source)
	}
}

func TestCommandTrailingNewlineIsTrimmed(t *testing.T) {
	// Every password manager emits one. A secret carrying a stray \n produces
	// a 401 that looks like a wrong secret, which is the worst possible way to
	// learn about a whitespace bug.
	t.Setenv(EnvSecret, "")
	noKeyringAvailable(t)

	res, err := Resolve(Request{Context: "lab", Command: "echo uuid-du-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Secret != "uuid-du-token" {
		t.Fatalf("secret = %q — la newline n'a pas été retirée", res.Secret)
	}
}

func TestFailingCommandIsAnErrorNotAFallthrough(t *testing.T) {
	// The whole point of reporting instead of skipping: falling through would
	// reach a stale keyring entry and authenticate as the wrong identity.
	t.Setenv(EnvSecret, "")
	useKeyring(t, &fakeKeyring{entries: map[string]string{"lab": "vieux-secret"}})

	_, err := Resolve(Request{Context: "lab", Command: "exit 3"})
	if err == nil {
		t.Fatal("une commande qui échoue doit remonter une erreur")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("une commande cassée n'est pas « aucune source »")
	}
	if !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("l'erreur doit citer la commande fautive : %v", err)
	}
}

func TestSilentCommandIsAnError(t *testing.T) {
	// Exit 0 with no output is the shape of `pass show` on a missing entry.
	// Treating that as "no secret here" would hide a typo in the path.
	t.Setenv(EnvSecret, "")
	noKeyringAvailable(t)

	if _, err := Resolve(Request{Context: "lab", Command: "true"}); err == nil {
		t.Fatal("une commande muette doit être une erreur")
	}
}

func TestMissingKeyringIsSkippedNotFatal(t *testing.T) {
	// A container, a cron, a CI runner: no keyring is normal, and must fall
	// through to the next source rather than fail the command.
	t.Setenv(EnvSecret, "")
	t.Setenv(EnvSecretCommand, "")
	noKeyringAvailable(t)

	_, err := Resolve(Request{Context: "lab"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
}

func TestLockedKeyringSurfacesItsOwnWords(t *testing.T) {
	// "Cannot create an item in a locked collection" IS the diagnosis. Losing
	// it behind a generic message turns a 30-second fix into an afternoon.
	t.Setenv(EnvSecret, "")
	t.Setenv(EnvSecretCommand, "")
	useKeyring(t, &fakeKeyring{getErr: errors.New("Cannot create an item in a locked collection")})

	_, err := Resolve(Request{Context: "lab"})
	if err == nil || !strings.Contains(err.Error(), "locked collection") {
		t.Fatalf("le message du trousseau doit remonter tel quel : %v", err)
	}
}

func TestEmptyKeyringEntryFallsThrough(t *testing.T) {
	t.Setenv(EnvSecret, "")
	t.Setenv(EnvSecretCommand, "")
	useKeyring(t, &fakeKeyring{entries: map[string]string{}})

	if _, err := Resolve(Request{Context: "lab"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
}

func TestKeyringIsKeyedByContext(t *testing.T) {
	// Two contexts on one machine are two different nodes. Sharing one entry
	// would send the prod secret to the lab endpoint.
	t.Setenv(EnvSecret, "")
	t.Setenv(EnvSecretCommand, "")
	useKeyring(t, &fakeKeyring{entries: map[string]string{"lab": "secret-lab", "prod": "secret-prod"}})

	for ctx, want := range map[string]string{"lab": "secret-lab", "prod": "secret-prod"} {
		res, err := Resolve(Request{Context: ctx})
		if err != nil {
			t.Fatalf("Resolve(%s): %v", ctx, err)
		}
		if res.Secret != want {
			t.Fatalf("contexte %s → %q, attendu %q", ctx, res.Secret, want)
		}
	}
}

func TestExplicitSourceThatAnswersNothingIsNotFound(t *testing.T) {
	t.Setenv(EnvSecret, "peu-importe")
	noKeyringAvailable(t)

	if _, err := Resolve(Request{Context: "lab", Source: SourceKeyring}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
}

func TestUnknownSourceIsRejected(t *testing.T) {
	if _, err := Resolve(Request{Context: "lab", Source: Source("trousseau")}); err == nil {
		t.Fatal("une source inconnue doit être refusée")
	}
}

func TestContextCommandBeatsEnvCommand(t *testing.T) {
	// The config file is the deliberate, durable statement; the environment is
	// the ad-hoc one. For the *pointer* (unlike the value) the explicit
	// per-context declaration is what the operator meant.
	t.Setenv(EnvSecret, "")
	t.Setenv(EnvSecretCommand, "printf depuis-env")
	noKeyringAvailable(t)

	res, err := Resolve(Request{Context: "lab", Command: "printf depuis-contexte"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Secret != "depuis-contexte" {
		t.Fatalf("secret = %q", res.Secret)
	}
}

func TestOriginNeverLeaksTheSecret(t *testing.T) {
	// Origin is printed by `config show`, `doctor` and `auth status`. A prefix
	// of a secret in a terminal scrollback is still a piece of a secret.
	const value = "s3cr3t-a-ne-jamais-afficher"
	t.Setenv(EnvSecret, value)

	res, err := Resolve(Request{Context: "lab"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(res.Origin, value) {
		t.Fatalf("Origin contient le secret : %q", res.Origin)
	}
	if strings.Contains(MissingHint("lab"), value) {
		t.Fatal("MissingHint contient le secret")
	}
}

func TestStoreTokenWithoutKeyringExplainsItself(t *testing.T) {
	noKeyringAvailable(t)

	err := StoreToken("lab", "peu-importe")
	if err == nil {
		t.Fatal("StoreToken sans trousseau doit échouer")
	}
	// The message has to name the other two ways out, or the reader is stuck.
	for _, want := range []string{EnvSecret, EnvSecretCommand} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("le message doit mentionner %s : %v", want, err)
		}
	}
}

func TestWriteUnsupportedIsDistinguishable(t *testing.T) {
	// macOS: readable but not writable. The caller must be able to tell that
	// apart from a broken keyring, because the fix is a different sentence.
	useKeyring(t, &fakeKeyring{setErr: ErrWriteUnsupported})

	if err := StoreToken("lab", "x"); !errors.Is(err, ErrWriteUnsupported) {
		t.Fatalf("attendu ErrWriteUnsupported, obtenu %v", err)
	}
}
