package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MakFly/pvecli/internal/pve"
)

// releasedBinary is what the fake release serves as "the new pvecli". Its
// content is arbitrary; what matters is that the test can assert those exact
// bytes landed on disk.
const releasedBinary = "#!/bin/sh\necho pvecli v9.9.9\n"

// oldBinary stands in for whatever is currently installed. Every failure
// path must leave it untouched, byte for byte.
const oldBinary = "the binary that is already installed\n"

// upgradeServer serves both halves of a release: the API endpoint that names
// the latest tag, and the download endpoint that serves the asset and its
// SHA256SUMS. Both githubAPIBase and githubDownloadBase are pointed at it for
// the lifetime of the test.
//
// corrupt=true serves a SHA256SUMS whose sum does not match the asset — the
// single most important case here, since it is the one where installing
// anyway would be a security bug rather than a nuisance.
func upgradeServer(t *testing.T, tag string, corrupt bool) *httptest.Server {
	t.Helper()

	asset := "pvecli_" + tag + "_" + runtime.GOOS + "_" + runtime.GOARCH
	sum := sha256.Sum256([]byte(releasedBinary))
	hexSum := hex.EncodeToString(sum[:])
	if corrupt {
		hexSum = strings.Repeat("0", 64)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			_, _ = w.Write([]byte(releasedBinary))
		case strings.HasSuffix(r.URL.Path, "/"+upgradeSums):
			_, _ = w.Write([]byte(hexSum + "  " + asset + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))

	origAPI, origDL := githubAPIBase, githubDownloadBase
	githubAPIBase, githubDownloadBase = srv.URL, srv.URL
	t.Cleanup(func() {
		githubAPIBase, githubDownloadBase = origAPI, origDL
		srv.Close()
	})
	return srv
}

// installedAt writes a stand-in for the currently installed binary and
// returns its path. A real file in a real directory: replaceBinary creates
// its temp file next to it, so a fake path would not exercise the same code.
func installedAt(t *testing.T) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "pvecli")
	if err := os.WriteFile(dest, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	return dest
}

func readInstalled(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// exitCodeOf reports the code an error carries, or -1 when it carries none.
func exitCodeOf(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return -1
}

// The nominal path: the published bytes replace the installed ones, and the
// result is executable.
func TestUpgradeReplacesTheBinary(t *testing.T) {
	upgradeServer(t, "v9.9.9", false)
	dest := installedAt(t)

	var out bytes.Buffer
	if err := upgradeTo(context.Background(), &out, "v0.1.0", dest, false, false); err != nil {
		t.Fatalf("upgrade a échoué : %v", err)
	}

	if got := readInstalled(t, dest); got != releasedBinary {
		t.Errorf("le binaire n'a pas été remplacé : %q", got)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755 — un binaire non exécutable n'a remplacé personne", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "v0.1.0 → v9.9.9") {
		t.Errorf("sortie = %q, elle doit nommer les deux versions", out.String())
	}
}

// The case that justifies the whole download-then-verify-then-write order: a
// checksum that does not match must leave the installed binary alone, and
// must not leave a temp file behind either.
func TestUpgradeRefusesAWrongChecksum(t *testing.T) {
	upgradeServer(t, "v9.9.9", true)
	dest := installedAt(t)

	var out bytes.Buffer
	err := upgradeTo(context.Background(), &out, "v0.1.0", dest, false, false)
	if err == nil {
		t.Fatal("une somme fausse doit interrompre l'upgrade")
	}
	if !strings.Contains(err.Error(), "SOMME DE CONTRÔLE INCORRECTE") {
		t.Errorf("le message n'explique pas le refus : %v", err)
	}
	if got := readInstalled(t, dest); got != oldBinary {
		t.Errorf("le binaire installé a été touché : %q", got)
	}

	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("un fichier temporaire a survécu à l'échec : %v", entries)
	}
}

// A missing entry in SHA256SUMS is a failure, not a skipped check.
func TestUpgradeRefusesAnAssetAbsentFromTheSums(t *testing.T) {
	if _, err := sumFor("deadbeef  pvecli_v1.0.0_plan9_386\n", "pvecli_v1.0.0_linux_amd64"); err == nil {
		t.Fatal("un asset absent de SHA256SUMS doit être une erreur, jamais une vérification sautée")
	}
	// The two spellings shasum produces name the same file.
	for _, line := range []string{"abc123  asset\n", "abc123 *asset\n"} {
		got, err := sumFor(line, "asset")
		if err != nil || got != "abc123" {
			t.Errorf("sumFor(%q) = %q, %v", line, got, err)
		}
	}
}

// A local build is ahead of the latest release, not behind it. Refusing is
// the point; refusing WITHOUT calling GitHub is what makes it cheap.
func TestUpgradeRefusesADevBuildWithoutForce(t *testing.T) {
	origAPI := githubAPIBase
	githubAPIBase = failIfCalled(t).URL
	t.Cleanup(func() { githubAPIBase = origAPI })

	dest := installedAt(t)
	var out bytes.Buffer
	err := upgradeTo(context.Background(), &out, "dev", dest, false, false)
	if err == nil {
		t.Fatal("un build dev ne doit pas être écrasé sans --force")
	}
	if got := exitCodeOf(err); got != pve.ExitUsage {
		t.Errorf("code de sortie = %d, want %d", got, pve.ExitUsage)
	}
	if got := readInstalled(t, dest); got != oldBinary {
		t.Errorf("le binaire a été touché : %q", got)
	}
}

// ... and --force lifts exactly that refusal.
func TestUpgradeForceOverridesADevBuild(t *testing.T) {
	upgradeServer(t, "v9.9.9", false)
	dest := installedAt(t)

	var out bytes.Buffer
	if err := upgradeTo(context.Background(), &out, "dev", dest, false, true); err != nil {
		t.Fatalf("--force doit lever le refus : %v", err)
	}
	if got := readInstalled(t, dest); got != releasedBinary {
		t.Errorf("--force n'a rien installé : %q", got)
	}
}

// Already on the published version: say so, write nothing, exit 0.
func TestUpgradeIsANoOpWhenAlreadyLatest(t *testing.T) {
	upgradeServer(t, "v9.9.9", false)
	dest := installedAt(t)

	var out bytes.Buffer
	if err := upgradeTo(context.Background(), &out, "v9.9.9", dest, false, false); err != nil {
		t.Fatalf("une version à jour ne doit pas être une erreur : %v", err)
	}
	if !strings.Contains(out.String(), "déjà à jour") {
		t.Errorf("sortie = %q", out.String())
	}
	if got := readInstalled(t, dest); got != oldBinary {
		t.Errorf("le binaire a été réécrit alors qu'il était à jour : %q", got)
	}
}

// --dry-run names the asset, the sums and the destination, and touches none
// of them.
func TestUpgradeDryRunWritesNothing(t *testing.T) {
	upgradeServer(t, "v9.9.9", false)
	dest := installedAt(t)

	var out bytes.Buffer
	if err := upgradeTo(context.Background(), &out, "v0.1.0", dest, true, false); err != nil {
		t.Fatalf("--dry-run a échoué : %v", err)
	}
	if got := readInstalled(t, dest); got != oldBinary {
		t.Errorf("--dry-run a écrit sur le disque : %q", got)
	}
	for _, want := range []string{"v9.9.9", upgradeSums, dest, "rien n'a été écrit"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("sortie = %q, il y manque %q", out.String(), want)
		}
	}
}

// The asset name must match what the release workflow actually publishes.
func TestUpgradeAssetNamesWhatTheWorkflowPublishes(t *testing.T) {
	got, err := upgradeAsset("v0.2.0")
	if err != nil {
		t.Skipf("plateforme non publiée (%s/%s) — rien à vérifier ici", runtime.GOOS, runtime.GOARCH)
	}
	want := "pvecli_v0.2.0_" + runtime.GOOS + "_" + runtime.GOARCH
	if got != want {
		t.Errorf("asset = %q, want %q", got, want)
	}
}

// The root alias must reach the same code, and must not have broken the bare
// invocation that used to rely on Cobra printing the help itself.
func TestRootUpgradeFlagIsWiredAndBareInvocationStillHelps(t *testing.T) {
	root := NewRootCmd("dev", "abc1234")
	if root.Flags().Lookup("upgrade") == nil {
		t.Fatal("pvecli --upgrade n'est pas déclaré")
	}

	origAPI := githubAPIBase
	githubAPIBase = failIfCalled(t).URL
	t.Cleanup(func() { githubAPIBase = origAPI })

	// A dev build refuses before the network — which proves the flag reached
	// upgradeTo rather than the help.
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"--upgrade"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "compilé localement") {
		t.Fatalf("--upgrade n'a pas atteint la commande : %v", err)
	}
}
