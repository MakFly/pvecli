package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetHookEnv gives every test a known-empty baseline for the variables
// install-hook reads, then lets it override the ones it cares about. Without
// this, a variable already set in the developer's own shell (XDG_DATA_HOME,
// PVECLI_SHELL_RC, …) would leak into the test and make it depend on whose
// machine runs it — exactly what TestMain already guards against for the
// config env vars.
func resetHookEnv(t *testing.T, home, shell string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", shell)
	t.Setenv("ZDOTDIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PVECLI_SHELL_RC", "")
	t.Setenv("PVECLI_NO_SHELL_HOOK", "")
}

func snippetFilePath(home string) string {
	return filepath.Join(home, ".local", "share", "pvecli", "update-notify.sh")
}

// listFilesUnder is used to assert "nothing was written": it walks home and
// returns every regular file found, so a test can compare against an empty
// (or unchanged) set instead of checking one guessed path and missing a
// write somewhere else.
func listFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		found = append(found, path)
		return nil
	})
	return found
}

// 1. --print: output equals the embedded content, and writes nothing.
func TestInstallHookPrintEmitsSnippetAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")

	stdout, _, err := run(t, "update", "install-hook", "--print")
	if err != nil {
		t.Fatalf("install-hook --print: %v", err)
	}
	if stdout != updateNotifySnippet {
		t.Errorf("stdout = %q, want exactly the embedded snippet", stdout)
	}
	if files := listFilesUnder(t, home); len(files) != 0 {
		t.Errorf("--print wrote files: %v", files)
	}
}

// 2. zsh wiring: block written into .zshrc, snippet placed at the right
// spot, mode 0755 verified with os.Stat.
func TestInstallHookWiresZsh(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")

	stdout, _, err := run(t, "update", "install-hook")
	if err != nil {
		t.Fatalf("install-hook: %v", err)
	}
	if !strings.Contains(stdout, home) {
		t.Errorf("stdout = %q, want it to name the wired file", stdout)
	}

	rc := filepath.Join(home, ".zshrc")
	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("reading .zshrc: %v", err)
	}
	if !strings.Contains(string(content), shellHookBegin) || !strings.Contains(string(content), shellHookEnd) {
		t.Errorf(".zshrc = %q, want both markers", content)
	}
	snippetPath := snippetFilePath(home)
	if !strings.Contains(string(content), snippetPath) {
		t.Errorf(".zshrc = %q, want it to source %s", content, snippetPath)
	}

	info, err := os.Stat(snippetPath)
	if err != nil {
		t.Fatalf("stat snippet: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("snippet mode = %v, want 0755", info.Mode().Perm())
	}
	snippetContent, err := os.ReadFile(snippetPath)
	if err != nil {
		t.Fatalf("reading snippet: %v", err)
	}
	if string(snippetContent) != updateNotifySnippet {
		t.Error("written snippet does not match the embedded one")
	}
}

// 3. bash wiring: .bashrc when it exists, .bash_profile when it is the only
// one present.
func TestInstallHookWiresBashUsingBashrcWhenPresent(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/bash")
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("seeding .bashrc: %v", err)
	}

	if _, _, err := run(t, "update", "install-hook"); err != nil {
		t.Fatalf("install-hook: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
		t.Error(".bash_profile was created even though .bashrc exists")
	}
	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatalf("reading .bashrc: %v", err)
	}
	if !strings.Contains(string(content), shellHookBegin) {
		t.Errorf(".bashrc = %q, want the marker", content)
	}
	if !strings.HasPrefix(string(content), "# existing\n") {
		t.Errorf(".bashrc = %q, want the pre-existing line preserved at the top", content)
	}
}

func TestInstallHookWiresBashUsingBashProfileWhenBashrcAbsent(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/bash")
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("seeding .bash_profile: %v", err)
	}

	if _, _, err := run(t, "update", "install-hook"); err != nil {
		t.Fatalf("install-hook: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
		t.Error(".bashrc was created even though only .bash_profile exists")
	}
	content, err := os.ReadFile(filepath.Join(home, ".bash_profile"))
	if err != nil {
		t.Fatalf("reading .bash_profile: %v", err)
	}
	if !strings.Contains(string(content), shellHookBegin) {
		t.Errorf(".bash_profile = %q, want the marker", content)
	}
}

// 4. Idempotence: two calls, marker appears exactly once.
func TestInstallHookIsIdempotent(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")

	for i := 0; i < 2; i++ {
		if _, _, err := run(t, "update", "install-hook"); err != nil {
			t.Fatalf("install-hook call %d: %v", i, err)
		}
	}

	content, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("reading .zshrc: %v", err)
	}
	if n := strings.Count(string(content), shellHookBegin); n != 1 {
		t.Errorf("BEGIN marker appears %d times, want exactly 1\n%s", n, content)
	}
	if n := strings.Count(string(content), shellHookEnd); n != 1 {
		t.Errorf("END marker appears %d times, want exactly 1\n%s", n, content)
	}
}

// 5. --uninstall: the block is gone, the user's neighbouring content (before
// AND after the block) is intact, and the snippet file is removed.
func TestInstallHookUninstallRemovesBlockAndSnippetKeepsNeighbours(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")

	before := "# my own aliases\nalias ll='ls -la'\n"
	after := "# my own prompt tweaks\nexport PS1='> '\n"
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte(before), 0o644); err != nil {
		t.Fatalf("seeding .zshrc: %v", err)
	}

	if _, _, err := run(t, "update", "install-hook"); err != nil {
		t.Fatalf("install-hook: %v", err)
	}
	// Append the user's own content after the block, the way it would land
	// if they edited their .zshrc themselves after wiring the hook.
	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("reading .zshrc: %v", err)
	}
	if err := os.WriteFile(rc, append(content, []byte(after)...), 0o644); err != nil {
		t.Fatalf("appending user content: %v", err)
	}

	if _, _, err := run(t, "update", "install-hook", "--uninstall"); err != nil {
		t.Fatalf("install-hook --uninstall: %v", err)
	}

	final, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("reading .zshrc after uninstall: %v", err)
	}
	if strings.Contains(string(final), shellHookBegin) || strings.Contains(string(final), shellHookEnd) {
		t.Errorf(".zshrc after uninstall = %q, still contains a marker", final)
	}
	if !strings.Contains(string(final), before) {
		t.Errorf(".zshrc after uninstall = %q, lost the content that came BEFORE the block", final)
	}
	if !strings.Contains(string(final), after) {
		t.Errorf(".zshrc after uninstall = %q, lost the content that came AFTER the block", final)
	}

	if _, err := os.Stat(snippetFilePath(home)); !os.IsNotExist(err) {
		t.Errorf("snippet file still present after --uninstall (stat err = %v)", err)
	}
}

// 6. --uninstall on a file without a block: succeeds, changes nothing (byte
// for byte).
func TestInstallHookUninstallWithoutBlockIsANoop(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")

	original := "# nothing pvecli-related here\nexport FOO=bar\n"
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatalf("seeding .zshrc: %v", err)
	}

	if _, _, err := run(t, "update", "install-hook", "--uninstall"); err != nil {
		t.Fatalf("install-hook --uninstall: %v", err)
	}

	final, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("reading .zshrc: %v", err)
	}
	if string(final) != original {
		t.Errorf(".zshrc = %q, want it byte-for-byte unchanged: %q", final, original)
	}
}

// 7. PVECLI_NO_SHELL_HOOK=1: nothing is written anywhere.
func TestInstallHookDoesNothingWhenDisabledByEnv(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")
	t.Setenv("PVECLI_NO_SHELL_HOOK", "1")

	if _, _, err := run(t, "update", "install-hook"); err != nil {
		t.Fatalf("install-hook: %v", err)
	}

	if files := listFilesUnder(t, home); len(files) != 0 {
		t.Errorf("PVECLI_NO_SHELL_HOOK=1 wrote files: %v", files)
	}
}

// 8. Unknown shell: no write anywhere, and the output carries the line to
// add by hand.
func TestInstallHookUnknownShellWritesNothingAndPrintsManualLine(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/usr/bin/fish")

	stdout, _, err := run(t, "update", "install-hook")
	if err != nil {
		t.Fatalf("install-hook: %v", err)
	}

	if files := listFilesUnder(t, home); len(files) != 0 {
		t.Errorf("unknown shell wrote files: %v", files)
	}
	if !strings.Contains(stdout, ". "+snippetFilePath(home)) {
		t.Errorf("stdout = %q, want the manual sourcing line for %s", stdout, snippetFilePath(home))
	}
}

// PVECLI_SHELL_RC is the one deliberate escape hatch that writes to a file
// this command did not deduce on its own.
func TestInstallHookHonoursShellRCOverride(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/usr/bin/fish") // would otherwise be unrecognised
	override := filepath.Join(home, "custom-rc")
	t.Setenv("PVECLI_SHELL_RC", override)

	if _, _, err := run(t, "update", "install-hook"); err != nil {
		t.Fatalf("install-hook: %v", err)
	}

	content, err := os.ReadFile(override)
	if err != nil {
		t.Fatalf("reading override rc: %v", err)
	}
	if !strings.Contains(string(content), shellHookBegin) {
		t.Errorf("override rc = %q, want the marker", content)
	}
}

// The atomic-write path must not clobber the identity of an existing rc
// file: a symlink target stays a symlink after wiring, and its content is
// updated through the link, not replaced by a plain file.
func TestInstallHookPreservesSymlinkedRCFile(t *testing.T) {
	home := t.TempDir()
	resetHookEnv(t, home, "/bin/zsh")

	real := filepath.Join(home, "real-zshrc")
	if err := os.WriteFile(real, []byte("# real file\n"), 0o644); err != nil {
		t.Fatalf("seeding real rc: %v", err)
	}
	link := filepath.Join(home, ".zshrc")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlinking .zshrc: %v", err)
	}

	if _, _, err := run(t, "update", "install-hook"); err != nil {
		t.Fatalf("install-hook: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat .zshrc: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal(".zshrc is no longer a symlink after install-hook")
	}
	content, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("reading real rc through its own path: %v", err)
	}
	if !strings.Contains(string(content), shellHookBegin) {
		t.Errorf("real rc file = %q, want the block written through the symlink", content)
	}
}
