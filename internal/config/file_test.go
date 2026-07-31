package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The core lesson of PVX-002: a secret in the config file is refused, loudly,
// with the line to go and delete and the variable to use instead.
func TestLoadRejectsTokenSecret(t *testing.T) {
	path := writeFile(t, `current_context: lab
contexts:
  lab:
    endpoint: https://pve.example:8006
    token_id: automation@pve!pvectl
    token_secret: 11111111-2222-3333-4444-555555555555
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("un token_secret dans le fichier doit être refusé")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ligne 6") {
		t.Errorf("l'erreur doit pointer la ligne 6, got: %s", msg)
	}
	if !strings.Contains(msg, EnvTokenSecret) {
		t.Errorf("l'erreur doit indiquer %s, got: %s", EnvTokenSecret, msg)
	}
}

// The key is refused wherever it sits — including in a context that is not the
// current one, which an unmarshal into the typed struct would never notice.
func TestLoadRejectsTokenSecretInAnyContext(t *testing.T) {
	path := writeFile(t, `current_context: lab
contexts:
  lab:
    endpoint: https://pve.example:8006
  vieux:
    token_secret: oublié-là-depuis-des-mois
`)

	if _, err := Load(path); err == nil {
		t.Fatal("le token_secret d'un contexte inactif doit aussi être refusé")
	}
}

// No file is a legitimate state: environment and flags alone can drive the CLI.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("un fichier absent ne doit pas être une erreur: %v", err)
	}
	if f.Contexts == nil {
		t.Error("Contexts doit être une map utilisable, pas nil")
	}
}

func TestSavePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pvectl")
	path := filepath.Join(dir, "config.yaml")

	f := &File{
		CurrentContext: "lab",
		Contexts:       map[string]*Context{"lab": {Endpoint: "https://pve.example:8006"}},
	}
	if err := Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode du fichier = %o, want 600", got)
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("mode du dossier = %o, want 700", got)
	}
}

// WriteFile only applies its mode when it creates the file. Without the
// explicit chmod, a config that was once world-readable would stay that way.
func TestSaveTightensExistingPermissions(t *testing.T) {
	path := writeFile(t, "current_context: lab\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Save(path, &File{CurrentContext: "lab"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600 — Save doit resserrer les droits", got)
	}
}

func TestPathPrecedence(t *testing.T) {
	t.Setenv("PVECTL_CONFIG", "/tmp/depuis-env.yaml")

	got, err := Path("/tmp/depuis-flag.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/depuis-flag.yaml" {
		t.Errorf("Path = %q, le flag doit gagner", got)
	}

	if got, err = Path(""); err != nil || got != "/tmp/depuis-env.yaml" {
		t.Errorf("Path = %q (%v), PVECTL_CONFIG doit gagner sur le défaut", got, err)
	}
}

func TestSetKeyRefusesTokenSecret(t *testing.T) {
	err := SetKey(&Context{}, "token_secret", "11111111-2222")
	if err == nil {
		t.Fatal("config set token_secret doit être refusé")
	}
	if !strings.Contains(err.Error(), EnvTokenSecret) {
		t.Errorf("l'erreur doit rediriger vers %s, got: %v", EnvTokenSecret, err)
	}
}

func TestSetKeyNested(t *testing.T) {
	c := &Context{}
	if err := SetKey(c, "tls.fingerprint", "9F:3D:1A:55"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if c.TLS.Fingerprint != "9F:3D:1A:55" {
		t.Errorf("TLS.Fingerprint = %q", c.TLS.Fingerprint)
	}
}

func TestSetKeyRejectsUnknownKey(t *testing.T) {
	err := SetKey(&Context{}, "endpiont", "https://typo:8006")
	if err == nil {
		t.Fatal("une clé inconnue doit échouer plutôt que d'être stockée")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("l'erreur doit lister les clés valides, got: %v", err)
	}
}
