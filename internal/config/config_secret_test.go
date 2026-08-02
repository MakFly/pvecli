package config

import (
	"strings"
	"testing"
)

// The file may name a source. It may still never hold the value — that rule is
// the reason the whole resolution chain exists, so it gets a test of its own.

func TestSecretSourceAcceptsOnlyKnownSources(t *testing.T) {
	for _, ok := range []string{"env", "command", "keyring", ""} {
		var c Context
		if err := SetKey(&c, "secret_source", ok); err != nil {
			t.Fatalf("secret_source=%q refusé : %v", ok, err)
		}
		if c.SecretSource != ok {
			t.Fatalf("secret_source = %q, attendu %q", c.SecretSource, ok)
		}
	}

	var c Context
	err := SetKey(&c, "secret_source", "trousseau")
	if err == nil {
		t.Fatal("une source inconnue doit être refusée, pas stockée silencieusement")
	}
	// A typo must list the alternatives, or the reader has to go read the code.
	if !strings.Contains(err.Error(), "keyring") {
		t.Fatalf("le refus doit lister les valeurs acceptées : %v", err)
	}
}

func TestSecretCommandIsStoredVerbatim(t *testing.T) {
	// A command is a pointer, not a credential: `pass show pve/token` in a
	// config file leaks nothing on its own. That is why it may live here
	// while the value may not.
	var c Context
	const cmdline = "pass show pve/token"
	if err := SetKey(&c, "secret_command", cmdline); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if c.SecretCommand != cmdline {
		t.Fatalf("secret_command = %q", c.SecretCommand)
	}
}

func TestTokenSecretIsStillRefusedInTheFile(t *testing.T) {
	// The rule that started all this. Widening the *sources* must not have
	// widened what the file is allowed to contain.
	var c Context
	err := SetKey(&c, "token_secret", "uuid")
	if err == nil {
		t.Fatal("« token_secret » doit toujours être refusé dans le fichier")
	}
	if strings.Contains(err.Error(), "uuid") {
		t.Fatalf("le refus ne doit pas répéter la valeur : %v", err)
	}
}

func TestSecretKeysAreAdvertisedAsWritable(t *testing.T) {
	// An accepted key absent from WritableKeys is a key nobody discovers:
	// `config set` prints this list when it refuses something.
	for _, want := range []string{"secret_source", "secret_command"} {
		found := false
		for _, k := range WritableKeys {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q accepté par SetKey mais absent de WritableKeys", want)
		}
	}
}
