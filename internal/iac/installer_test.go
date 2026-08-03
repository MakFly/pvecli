package iac

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBin puts an executable of the given name on an otherwise empty PATH, so
// a test controls exactly which package managers "exist" without touching
// whatever the machine running the test actually has installed.
func stubBin(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
}

// Nothing on PATH means nothing to offer — the caller falls back to the
// existing MissingToolError hint.
func TestDetectPackageManagerFindsNone(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, ok := DetectPackageManager(); ok {
		t.Fatal("aucun package manager stubé, mais DetectPackageManager en a trouvé un")
	}
}

// brew comes first even when a system manager is also present: it never needs
// sudo, and where both coexist its formulae track upstream fastest.
func TestDetectPackageManagerPrefersBrew(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "apt-get")
	stubBin(t, dir, "brew")
	t.Setenv("PATH", dir)

	pm, ok := DetectPackageManager()
	if !ok || pm.Name != "brew" {
		t.Fatalf("brew doit être choisi en priorité, reçu %+v (ok=%v)", pm, ok)
	}
	if argv := pm.Install("terraform"); strings.Join(argv, " ") != "brew install terraform" {
		t.Errorf("argv brew inattendu : %v", argv)
	}
}

// A system manager without brew installed still needs sudo — pvecli does not
// silently escalate privileges beyond what the manager itself requires.
func TestDetectPackageManagerFallsBackToAptWithSudo(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "apt-get")
	t.Setenv("PATH", dir)

	pm, ok := DetectPackageManager()
	if !ok || pm.Name != "apt-get" {
		t.Fatalf("apt-get attendu, reçu %+v (ok=%v)", pm, ok)
	}
	argv := pm.Install("ansible")
	if got, want := strings.Join(argv, " "), "sudo apt-get install -y ansible"; got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// ansible-playbook is the binary pvecli looks for, but the package that
// provides it is called "ansible" everywhere.
func TestPackageNameMapsAnsiblePlaybookToAnsible(t *testing.T) {
	if got := PackageName(AnsiblePlaybookBin); got != "ansible" {
		t.Errorf("PackageName(%q) = %q, want %q", AnsiblePlaybookBin, got, "ansible")
	}
	if got := PackageName(TerraformBin); got != "terraform" {
		t.Errorf("PackageName(%q) = %q, want %q", TerraformBin, got, "terraform")
	}
}
