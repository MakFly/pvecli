package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakFly/pvectl/internal/config"
)

// seedConfig writes a config file in a temp dir and returns its path.
func seedConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const labConfig = `current_context: lab
contexts:
  lab:
    endpoint: https://depuis-le-fichier:8006
    token_id: automation@pve!pvectl
    node: pve
`

func TestConfigInitCreatesFileFromFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pvectl", "config.yaml")

	_, _, err := run(t, "config", "init",
		"--config", path,
		"--endpoint", "https://pve.example:8006",
		"--token-id", "automation@pve!pvectl",
		"--node", "pve")
	if err != nil {
		t.Fatalf("config init: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.CurrentContext != "lab" {
		t.Errorf("CurrentContext = %q, want lab", f.CurrentContext)
	}
	if got := f.Contexts["lab"].Endpoint; got != "https://pve.example:8006" {
		t.Errorf("endpoint = %q, l'amorçage par flag n'a pas pris", got)
	}
}

// The seed goes through the same layering as everything else, so the
// environment works too.
func TestConfigInitSeedsFromEnv(t *testing.T) {
	t.Setenv(config.EnvEndpoint, "https://depuis-env:8006")
	path := filepath.Join(t.TempDir(), "config.yaml")

	if _, _, err := run(t, "config", "init", "--config", path); err != nil {
		t.Fatalf("config init: %v", err)
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Contexts["lab"].Endpoint; got != "https://depuis-env:8006" {
		t.Errorf("endpoint = %q, l'amorçage par environnement n'a pas pris", got)
	}
}

func TestConfigInitRefusesToOverwrite(t *testing.T) {
	path := seedConfig(t, labConfig)

	if _, _, err := run(t, "config", "init", "--config", path); err == nil {
		t.Fatal("config init doit refuser d'écraser un fichier existant")
	}
	if _, _, err := run(t, "config", "init", "--config", path, "--force"); err != nil {
		t.Fatalf("--force doit autoriser l'écrasement: %v", err)
	}
}

// The proof of the story: the environment beats the file, and `config show`
// reflects the effective configuration rather than the file's contents.
func TestConfigShowEnvBeatsFile(t *testing.T) {
	t.Setenv(config.EnvEndpoint, "https://autre:8006")
	path := seedConfig(t, labConfig)

	stdout, _, err := run(t, "config", "show", "--config", path)
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(stdout, "https://autre:8006") {
		t.Errorf("l'environnement doit gagner, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, config.EnvEndpoint) {
		t.Errorf("la provenance doit être affichée, got:\n%s", stdout)
	}
}

func TestConfigShowFlagBeatsEverything(t *testing.T) {
	t.Setenv(config.EnvEndpoint, "https://autre:8006")
	path := seedConfig(t, labConfig)

	stdout, _, err := run(t, "config", "show", "--config", path, "--endpoint", "https://depuis-le-flag:8006")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(stdout, "https://depuis-le-flag:8006") {
		t.Errorf("le flag doit gagner, got:\n%s", stdout)
	}
}

// Same shape as the anti-leak test of PVX-009: inject a known secret, scan the
// whole output for it. A secret leaks through debug output far more often than
// through business logic.
func TestConfigShowNeverPrintsSecret(t *testing.T) {
	const secret = "11111111-2222-3333-4444-555555555555"
	t.Setenv(config.EnvTokenSecret, secret)
	path := seedConfig(t, labConfig)

	stdout, stderr, err := run(t, "config", "show", "--config", path)
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if strings.Contains(stdout+stderr, secret) {
		t.Error("le secret apparaît dans la sortie de config show")
	}
	if !strings.Contains(stdout, "<défini>") {
		t.Errorf("config show doit signaler la présence du secret, got:\n%s", stdout)
	}
}

func TestConfigSetWritesNestedKey(t *testing.T) {
	path := seedConfig(t, labConfig)

	if _, _, err := run(t, "config", "set", "tls.fingerprint", "9F:3D:1A:55", "--config", path); err != nil {
		t.Fatalf("config set: %v", err)
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Contexts["lab"].TLS.Fingerprint; got != "9F:3D:1A:55" {
		t.Errorf("tls.fingerprint = %q", got)
	}
	if got := f.Contexts["lab"].Endpoint; got != "https://depuis-le-fichier:8006" {
		t.Errorf("config set a écrasé une autre clé: endpoint = %q", got)
	}
}

func TestConfigSetRefusesTokenSecret(t *testing.T) {
	path := seedConfig(t, labConfig)

	_, _, err := run(t, "config", "set", "token_secret", "11111111-2222", "--config", path)
	if err == nil {
		t.Fatal("config set token_secret doit être refusé")
	}
	if !strings.Contains(err.Error(), config.EnvTokenSecret) {
		t.Errorf("l'erreur doit rediriger vers l'environnement, got: %v", err)
	}
}

// A file carrying the secret is refused before anything else happens.
func TestConfigShowRefusesSecretInFile(t *testing.T) {
	path := seedConfig(t, labConfig+"    token_secret: fuite\n")

	if _, _, err := run(t, "config", "show", "--config", path); err == nil {
		t.Fatal("un token_secret dans le fichier doit faire échouer la commande")
	}
}
