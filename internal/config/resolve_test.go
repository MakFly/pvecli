package config

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestMain clears the PVE_* variables before anything runs.
//
// Without it these tests pass or fail depending on whether the developer has
// sourced ~/.config/pvecli/env in the shell they typed `make test` in — which
// is precisely what happened, on a package that had been fixed everywhere
// except here.
func TestMain(m *testing.M) {
	for _, name := range []string{EnvEndpoint, EnvTokenID, EnvTokenSecret, EnvInsecure} {
		_ = os.Unsetenv(name)
	}
	_ = os.Unsetenv("PVECLI_CONFIG")
	os.Exit(m.Run())
}

// flagsFor mirrors the persistent flags declared on the root command.
func flagsFor(t *testing.T, set map[string]string) *pflag.FlagSet {
	t.Helper()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	for _, name := range []string{"context", "endpoint", "token-id", "node"} {
		fs.String(name, "", "")
	}
	for name, value := range set {
		if err := fs.Set(name, value); err != nil {
			t.Fatalf("flag --%s=%q: %v", name, value, err)
		}
	}
	return fs
}

func fileWithLab() *File {
	return &File{
		CurrentContext: "lab",
		Contexts: map[string]*Context{
			"lab": {
				Endpoint: "https://file:8006",
				TokenID:  "file@pve!id",
				Node:     "file-node",
				TLS:      TLS{Fingerprint: "AA:BB"},
			},
		},
	}
}

// The whole point of PRD §7.1: flags beat environment beats file beats
// defaults, field by field.
func TestResolvePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		flags      map[string]string
		env        map[string]string
		wantValue  string
		wantSource string
	}{
		{
			name:       "le flag gagne sur tout",
			flags:      map[string]string{"endpoint": "https://flag:8006"},
			env:        map[string]string{EnvEndpoint: "https://env:8006"},
			wantValue:  "https://flag:8006",
			wantSource: "flag --endpoint",
		},
		{
			name:       "l'environnement gagne sur le fichier",
			env:        map[string]string{EnvEndpoint: "https://env:8006"},
			wantValue:  "https://env:8006",
			wantSource: "env " + EnvEndpoint,
		},
		{
			name:       "le fichier gagne sur le défaut",
			wantValue:  "https://file:8006",
			wantSource: "fichier",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			eff, err := Resolve(flagsFor(t, tc.flags), fileWithLab())
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if eff.Endpoint != tc.wantValue {
				t.Errorf("Endpoint = %q, want %q", eff.Endpoint, tc.wantValue)
			}
			if got := eff.Sources["endpoint"]; got != tc.wantSource {
				t.Errorf("Sources[endpoint] = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

// With no file and no environment, every field falls back to its default and
// says so.
func TestResolveFallsBackToDefaults(t *testing.T) {
	eff, err := Resolve(flagsFor(t, nil), &File{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if eff.Endpoint != "" {
		t.Errorf("Endpoint = %q, want empty", eff.Endpoint)
	}
	if got := eff.Sources["endpoint"]; got != "défaut" {
		t.Errorf("Sources[endpoint] = %q, want %q", got, "défaut")
	}
}

// `--endpoint ""` is an explicit choice and must beat the file. This is why
// pick() tests Changed() rather than the emptiness of the value.
func TestResolveExplicitEmptyFlagBeatsFile(t *testing.T) {
	eff, err := Resolve(flagsFor(t, map[string]string{"endpoint": ""}), fileWithLab())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if eff.Endpoint != "" {
		t.Errorf("Endpoint = %q, want empty", eff.Endpoint)
	}
	if got := eff.Sources["endpoint"]; got != "flag --endpoint" {
		t.Errorf("Sources[endpoint] = %q, want the flag layer", got)
	}
}

// The secret comes from the environment and from nowhere else.
func TestResolveTokenSecretComesFromEnvOnly(t *testing.T) {
	t.Setenv(EnvTokenSecret, "s3cr3t")

	eff, err := Resolve(flagsFor(t, nil), fileWithLab())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if eff.TokenSecret != "s3cr3t" {
		t.Errorf("TokenSecret = %q, want it read from %s", eff.TokenSecret, EnvTokenSecret)
	}
}

// A context named but absent is a typo. Failing here beats failing three
// commands later with "endpoint manquant".
func TestResolveUnknownContextIsAnError(t *testing.T) {
	_, err := Resolve(flagsFor(t, map[string]string{"context": "prod"}), fileWithLab())
	if err == nil {
		t.Fatal("un contexte inconnu doit échouer")
	}
	if !strings.Contains(err.Error(), "lab") {
		t.Errorf("l'erreur doit lister les contextes connus, got: %v", err)
	}
}

func TestResolveInsecureFromEnv(t *testing.T) {
	t.Setenv(EnvInsecure, "1")

	eff, err := Resolve(flagsFor(t, nil), fileWithLab())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !eff.Insecure {
		t.Error("Insecure = false, want true")
	}
	if got := eff.Sources["insecure"]; got != "env "+EnvInsecure {
		t.Errorf("Sources[insecure] = %q, want the env layer", got)
	}
}

func TestResolveInsecureRejectsGarbage(t *testing.T) {
	t.Setenv(EnvInsecure, "peut-être")

	if _, err := Resolve(flagsFor(t, nil), fileWithLab()); err == nil {
		t.Fatal("une valeur non booléenne doit échouer explicitement")
	}
}
