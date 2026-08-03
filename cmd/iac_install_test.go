package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/testutil"
)

// isolatedPath points PATH at an empty directory, so a test controls exactly
// what looks installed — a machine (or a CI image) that happens to have a
// real terraform, brew or apt-get on it must not change what these tests
// observe.
func isolatedPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

func writeStub(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
}

func applyConfig(t *testing.T, tfDir string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte(
		"current_context: test\ncontexts:\n  test:\n    iac:\n      terraform_dir: "+tfDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PVECLI_CONFIG", cfg)
}

// Without a terminal to confirm on, ensureTool must not change anything: a CI
// log that has always seen MissingToolError keeps seeing exactly that, even
// though a package manager (stubbed here as "brew", never actually run) is
// sitting right there on PATH. Regression guard for PVX behaviour that must
// survive the new install offer.
func TestMissingTerraformIsUnchangedWithoutATerminal(t *testing.T) {
	dir := isolatedPath(t)
	installed := filepath.Join(dir, "brew-was-run")
	// ": >" rather than "touch": the point is that this script must never run
	// at all, and it must not depend on a binary that happens not to be on
	// this fully isolated PATH.
	writeStub(t, dir, "brew", ": > "+installed+"\nexit 0\n")

	applyConfig(t, t.TempDir())
	srv := testutil.New(t, "../testdata", map[string]string{"GET /api2/json/version": "version.json"})
	point(t, srv.URL)

	_, stderr, err := run(t, "iac", "apply", "--dry-run", "--node", "pve")
	if err == nil {
		t.Fatal("terraform absent : une erreur était attendue")
	}
	if !strings.Contains(err.Error(), "introuvable dans le PATH") {
		t.Errorf("le message MissingToolError d'origine doit rester inchangé, reçu : %v\nstderr:\n%s", err, stderr)
	}
	if _, statErr := os.Stat(installed); statErr == nil {
		t.Error("sans terminal, l'installeur ne doit jamais être exécuté")
	}
}

// --yes bypasses the confirmation, but not the absence of any package
// manager: pvecli does not invent one. The original hint is still what
// reaches the operator.
func TestMissingTerraformWithYesButNoPackageManager(t *testing.T) {
	isolatedPath(t) // empty: nothing looks like a package manager either

	applyConfig(t, t.TempDir())
	srv := testutil.New(t, "../testdata", map[string]string{"GET /api2/json/version": "version.json"})
	point(t, srv.URL)

	_, _, err := run(t, "iac", "apply", "--dry-run", "--yes", "--node", "pve")
	if err == nil {
		t.Fatal("terraform absent et aucun package manager : une erreur était attendue")
	}
	if !strings.Contains(err.Error(), "introuvable dans le PATH") {
		t.Errorf("erreur inattendue : %v", err)
	}
}

// With --yes and a package manager on PATH, ensureTool runs the manager's own
// install command and, once it has actually put the binary there, continues —
// exactly as if terraform had been present from the start.
func TestYesInstallsThroughDetectedPackageManager(t *testing.T) {
	dir := isolatedPath(t)
	// Fully isolated, not prepended: this machine may well have a real
	// terraform on its PATH (that is the whole point of the feature under
	// test), and the point of this test is that ensureTool never reaches it —
	// only the stubbed "brew" below may run. /usr/bin/cat and /usr/bin/chmod
	// are called by absolute path so the stub script needs nothing from a real
	// PATH either.
	writeStub(t, dir, "brew", `if [ "$1" = install ]; then
  /usr/bin/cat > `+dir+`/terraform <<'EOS'
#!/bin/sh
printf '{"format_version":"1.0"}\n'
EOS
  /usr/bin/chmod +x `+dir+`/terraform
fi
`)

	applyConfig(t, t.TempDir())
	srv := testutil.New(t, "../testdata", map[string]string{"GET /api2/json/version": "version.json"})
	point(t, srv.URL)

	_, stderr, err := run(t, "iac", "apply", "--dry-run", "--yes", "--node", "pve")
	if err != nil {
		t.Fatalf("iac apply --dry-run --yes : %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "brew install terraform") {
		t.Errorf("la commande d'installation exacte doit être tracée sur stderr :\n%s", stderr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "terraform")); statErr != nil {
		t.Error("le stub d'installation devait avoir posé le binaire terraform")
	}
}
