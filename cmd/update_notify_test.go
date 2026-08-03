package cmd

// This file replaces scripts/shell/update_notify_test.go, which tested
// scripts/shell/update-notify.sh directly off disk. That file moved to
// cmd/assets/update-notify.sh (see cmd/update_hook.go for why go:embed
// forced the move); these tests move with it and now exercise
// updateNotifySnippet — the exact bytes embedded into the binary — rather
// than a path on disk, which is a strictly stronger guarantee: it is what
// actually ships, not a copy that could drift from the embed. All four
// original cases are preserved, in particular
// TestUpdateNotifySnippetStaysSilentWithAnOlderPvecli, which pins a defect
// measured in production on 03-08-2026.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireZsh skips the test on a machine without zsh, rather than failing
// the build for everyone: the snippet is zsh-specific, the rest of pvecli is
// not, and a test that cannot run should say so instead of going red.
func requireZsh(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh introuvable sur cette machine — test de couverture du snippet ignoré")
	}
	return path
}

// fakePvecli writes a stand-in `pvecli` ahead of PATH. It never touches a
// network: it only echoes a known marker line for --notify and drops a
// marker file for --refresh, which is exactly the two observable effects the
// snippet's two calls are supposed to have.
func fakePvecli(t *testing.T, binDir, notifyLine, refreshMarker string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = update ] && [ \"$2\" = check ] && [ \"$3\" = --notify ]; then\n" +
		"  echo '" + notifyLine + "'\n" +
		"elif [ \"$1\" = update ] && [ \"$2\" = check ] && [ \"$3\" = --refresh ]; then\n" +
		"  : > '" + refreshMarker + "'\n" +
		"fi\n"
	path := filepath.Join(binDir, "pvecli")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("écriture du faux pvecli : %v", err)
	}
}

// writeSnippetToTempFile materialises the exact bytes embedded in the
// binary as a real file zsh can source, in its own throwaway directory.
func writeSnippetToTempFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update-notify.sh")
	if err := os.WriteFile(path, []byte(updateNotifySnippet), 0o644); err != nil {
		t.Fatalf("écriture du snippet : %v", err)
	}
	return path
}

// TestUpdateNotifySnippetPrintsNotifyLineAndBackgroundsRefresh is the test
// that would have caught PVX-090's regression: the snippet must print
// --notify's line on stdout (not swallow it), and it must still trigger
// --refresh in the background so the NEXT terminal has fresh data.
func TestUpdateNotifySnippetPrintsNotifyLineAndBackgroundsRefresh(t *testing.T) {
	zshPath := requireZsh(t)
	snippet := writeSnippetToTempFile(t)
	binDir := t.TempDir()
	refreshMarker := filepath.Join(binDir, "refresh-called")
	fakePvecli(t, binDir, "FAKE-NOTIFY-LINE", refreshMarker)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zshPath, "-c", "source "+snippet)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing the snippet failed: %v\noutput: %s", err, out)
	}

	text := string(out)
	if !strings.Contains(text, "FAKE-NOTIFY-LINE") {
		t.Errorf("output = %q, want the --notify line on stdout — this is the exact bug this test exists to catch", text)
	}

	// --refresh runs detached; give the orphaned background job a moment to
	// actually execute before deciding it never ran.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, statErr := os.Stat(refreshMarker); statErr == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("--refresh was never invoked in the background within 2s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestUpdateNotifySnippetIsANoopWithoutPvecli makes sure the guard clause at
// the top of the script actually guards: no output, nothing.
//
// The snippet's own exit status is the guard's `command -v pvecli || return`,
// i.e. non-zero here — exactly like grep or test failing — which is what a
// bare `zsh -c "source …"` surfaces as a non-zero process exit. In a real
// ~/.zshrc that status is discarded (sourcing a file never aborts an
// interactive shell), so the trailing `; true` below reproduces that
// context instead of asserting on a code that means nothing outside it.
func TestUpdateNotifySnippetIsANoopWithoutPvecli(t *testing.T) {
	zshPath := requireZsh(t)
	snippet := writeSnippetToTempFile(t)
	emptyBinDir := t.TempDir() // deliberately no `pvecli` in here

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zshPath, "-c", "source "+snippet+"; true")
	cmd.Env = []string{"PATH=" + emptyBinDir}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing the snippet without pvecli must not error: %v\noutput: %s", err, out)
	}
	if string(out) != "" {
		t.Errorf("output = %q, want strictly empty when pvecli is not on PATH", out)
	}
}

// TestUpdateNotifySnippetStaysSilentWithAnOlderPvecli pins a failure that was
// measured, not imagined: on 2026-08-03 the snippet was added to a real
// ~/.zshrc while ~/.local/bin/pvecli still held a build predating this story.
// That binary does not know `update check`, so cobra wrote
// "Error: unknown flag: --notify" to stderr — on EVERY new terminal.
//
// `command -v pvecli` cannot catch this: the binary exists, it is simply too
// old. The guard has to be on the output, and it has to be on stderr ONLY —
// redirecting stdout is what broke this feature the first time around.
func TestUpdateNotifySnippetStaysSilentWithAnOlderPvecli(t *testing.T) {
	zshPath := requireZsh(t)
	snippet := writeSnippetToTempFile(t)
	binDir := t.TempDir()

	// A pvecli that predates `update check`: it fails the way cobra does,
	// on stderr with a non-zero status.
	stale := "#!/bin/sh\necho 'Error: unknown flag: --notify' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "pvecli"), []byte(stale), 0o755); err != nil {
		t.Fatalf("écriture du pvecli périmé : %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zshPath, "-c", "source "+snippet+"; sleep 0.3; true")
	cmd.Env = []string{"PATH=" + binDir + ":/usr/bin:/bin"}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("an older pvecli must not make the shell error out: %v\noutput: %s", err, out)
	}
	if string(out) != "" {
		t.Errorf("output = %q, want strictly empty: a pvecli too old to know `update check` must stay quiet, not explain itself on every prompt", out)
	}
}

// TestUpdateNotifySnippetProducesNoJobControlNoiseInteractively pins the
// claim made in the snippet's own comment: the subshell form must not print
// a job id or a "done" line, and — the detail the comment could otherwise get
// away with getting wrong — this must hold in an INTERACTIVE zsh, not just a
// `zsh -c` script, since job-control reporting differs between the two.
func TestUpdateNotifySnippetProducesNoJobControlNoiseInteractively(t *testing.T) {
	zshPath := requireZsh(t)
	snippet := writeSnippetToTempFile(t)
	binDir := t.TempDir()
	fakePvecli(t, binDir, "FAKE-NOTIFY-LINE", filepath.Join(binDir, "refresh-called"))

	// An empty ZDOTDIR keeps whoever's real ~/.zshrc off this run: an
	// interactive zsh would otherwise source it and the assertions below
	// would depend on a stranger's shell config.
	zdotdir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The trailing `sleep 0.3` gives the backgrounded job a chance to finish
	// (and, if the subshell trick failed, to print its "done" line) before
	// the interactive shell — and this assertion — exits.
	cmd := exec.CommandContext(ctx, zshPath, "-i", "-c", "source "+snippet+"; sleep 0.3")
	cmd.Env = []string{
		"PATH=" + binDir + ":/usr/bin:/bin",
		"ZDOTDIR=" + zdotdir,
		"TERM=dumb",
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("interactive zsh run failed: %v\noutput: %s", err, out)
	}

	text := string(out)
	for _, noise := range []string{"[1]", "done"} {
		if strings.Contains(text, noise) {
			t.Errorf("interactive output = %q, contains job-control noise (%q) — the subshell comment's claim does not hold interactively", text, noise)
		}
	}
}
