package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldDirs points the config at two fresh directories and returns them.
func scaffoldDirs(t *testing.T) (tfDir, ansDir string) {
	t.Helper()
	tfDir, ansDir = t.TempDir(), t.TempDir()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	doc := "current_context: test\ncontexts:\n  test:\n    iac:\n" +
		"      terraform_dir: " + tfDir + "\n" +
		"      ansible_dir: " + ansDir + "\n"
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PVECLI_CONFIG", cfg)
	return tfDir, ansDir
}

func TestScaffoldWritesModuleAndRoles(t *testing.T) {
	tfDir, ansDir := scaffoldDirs(t)

	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatalf("iac scaffold : %v", err)
	}
	for _, rel := range []string{
		filepath.Join(tfDir, "pvecli-vms.tf"),
		filepath.Join(tfDir, "pvecli-lxc.tf"),
		filepath.Join(tfDir, "pvecli-base.tf"),
		filepath.Join(ansDir, "pvecli.yml"),
		filepath.Join(ansDir, "roles", "docker", "tasks", "main.yml"),
		filepath.Join(ansDir, "roles", "postgresql", "tasks", "main.yml"),
		filepath.Join(ansDir, "roles", "cloudflared", "tasks", "main.yml"),
	} {
		if _, err := os.Stat(rel); err != nil {
			t.Errorf("%s manquant après scaffold", rel)
		}
	}
}

// The manifest stays inside the binary. A copy on disk would let a stale file
// advertise services this build cannot install.
func TestScaffoldDoesNotCopyTheManifest(t *testing.T) {
	tfDir, ansDir := scaffoldDirs(t)
	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{tfDir, ansDir} {
		if _, err := os.Stat(filepath.Join(dir, "catalog.yaml")); err == nil {
			t.Errorf("catalog.yaml ne doit pas être recopié dans %s", dir)
		}
	}
}

func TestScaffoldDryRunWritesNothing(t *testing.T) {
	tfDir, ansDir := scaffoldDirs(t)

	stdout, _, err := run(t, "iac", "scaffold", "--dry-run")
	if err != nil {
		t.Fatalf("iac scaffold --dry-run : %v", err)
	}
	if !strings.Contains(stdout, "pvecli-vms.tf") {
		t.Errorf("--dry-run doit tout de même annoncer le plan :\n%s", stdout)
	}
	for _, dir := range []string{tfDir, ansDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("--dry-run a écrit dans %s : %v", dir, entries)
		}
	}
}

// Two `provider "proxmox"` blocks in one directory is a `terraform init` that
// fails before doing anything, so an existing lab keeps its own.
func TestScaffoldSkipsBaseWhenTheDirectoryAlreadyHasAProvider(t *testing.T) {
	tfDir, _ := scaffoldDirs(t)
	main := filepath.Join(tfDir, "main.tf")
	if err := os.WriteFile(main, []byte("provider \"proxmox\" {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatalf("iac scaffold : %v", err)
	}
	if _, err := os.Stat(filepath.Join(tfDir, "pvecli-base.tf")); err == nil {
		t.Error("pvecli-base.tf ne doit pas être posé à côté d'un provider existant")
	}
	if _, err := os.Stat(filepath.Join(tfDir, "pvecli-vms.tf")); err != nil {
		t.Error("pvecli-vms.tf doit être posé, lui")
	}
}

// An Ansible directory that already exists almost always has a hand-written
// site.yml. Shipping the catalogue under that name would make scaffold refuse
// on the very first run against a real repository -- which is how this was
// found.
func TestScaffoldCoexistsWithAHandWrittenSiteYML(t *testing.T) {
	_, ansDir := scaffoldDirs(t)
	mine := filepath.Join(ansDir, "site.yml")
	if err := os.WriteFile(mine, []byte("---\n- name: le mien\n  hosts: lab_apps\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatalf("scaffold doit passer à côté d'un site.yml existant : %v", err)
	}
	raw, err := os.ReadFile(mine) //nolint:gosec // path built by the test
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "le mien") {
		t.Error("le site.yml écrit à la main a été touché")
	}
	if _, err := os.Stat(filepath.Join(ansDir, "pvecli.yml")); err != nil {
		t.Error("le playbook du catalogue doit être posé à côté, sous son propre nom")
	}
}

// Same trap, second file: overwriting an existing requirements.yml would drop
// the collections it declared, silently, and the playbooks that needed them
// would fail much later with a missing-module error.
func TestScaffoldCoexistsWithAHandWrittenRequirements(t *testing.T) {
	_, ansDir := scaffoldDirs(t)
	mine := filepath.Join(ansDir, "requirements.yml")
	body := "collections:\n  - name: community.docker\n"
	if err := os.WriteFile(mine, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatalf("scaffold doit passer à côté d'un requirements.yml existant : %v", err)
	}
	raw, err := os.ReadFile(mine) //nolint:gosec // path built by the test
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Errorf("le requirements.yml écrit à la main a été touché :\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(ansDir, "pvecli-requirements.yml")); err != nil {
		t.Error("les collections du catalogue doivent être posées dans leur propre fichier")
	}
}

// The refusal is the feature: the difference is either a local adaptation or an
// older version, and choosing between them silently helps nobody.
func TestScaffoldRefusesToOverwriteALocalEdit(t *testing.T) {
	_, ansDir := scaffoldDirs(t)
	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatal(err)
	}

	site := filepath.Join(ansDir, "pvecli.yml")
	if err := os.WriteFile(site, []byte("# à moi\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "iac", "scaffold")
	if err == nil {
		t.Fatal("un fichier modifié localement doit arrêter la commande")
	}
	if !strings.Contains(err.Error(), site) {
		t.Errorf("le message doit nommer le fichier en cause : %v", err)
	}

	if _, _, err := run(t, "iac", "scaffold", "--force"); err != nil {
		t.Fatalf("--force doit trancher : %v", err)
	}
	raw, err := os.ReadFile(site) //nolint:gosec // path built by the test
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "à moi") {
		t.Error("--force n'a pas réécrit le fichier")
	}
}

// Running it twice must report every file as unchanged -- the same discipline
// the Ansible roles are held to.
func TestScaffoldIsIdempotent(t *testing.T) {
	scaffoldDirs(t)
	if _, _, err := run(t, "iac", "scaffold"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := run(t, "iac", "scaffold")
	if err != nil {
		t.Fatalf("second passage : %v", err)
	}
	if strings.Contains(stdout, "\técrit") {
		t.Errorf("le second passage ne doit rien réécrire :\n%s", stdout)
	}
	if !strings.Contains(stderr, "0 fichier(s) écrit(s)") {
		t.Errorf("le second passage doit annoncer zéro écriture :\n%s", stderr)
	}
}
