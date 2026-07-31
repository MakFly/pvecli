package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakFly/pvectl/internal/config"
	"github.com/MakFly/pvectl/internal/pve"
)

// TestMain isolates the package from whatever the developer has on their own
// machine. Without it, a test that forgets --config reads ~/.config/pvectl and
// passes or fails depending on whose laptop runs it — which is how the version
// test below was found to be lying.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pvectl-cmd-test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("PVECTL_CONFIG", filepath.Join(dir, "config.yaml"))
	for _, name := range []string{
		config.EnvEndpoint, config.EnvTokenID, config.EnvTokenSecret, config.EnvInsecure,
	} {
		_ = os.Unsetenv(name)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// run executes the command tree with the given arguments and captures both
// streams.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	root := NewRootCmd("dev", "abc1234")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	err = root.Execute()
	return out.String(), errOut.String(), err
}

// The --version flag describes the binary, and its output goes to stdout.
func TestVersionFlagPrintsBuildMetadata(t *testing.T) {
	stdout, _, err := run(t, "--version")
	if err != nil {
		t.Fatalf("--version returned an error: %v", err)
	}
	if got, want := stdout, "pvectl dev (commit abc1234)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// -v belongs to --verbose (PVX-009), not to --version.
//
// Cobra claims the shorthand for --version unless the flag is declared before
// the Version field is set. This test was written at PVX-001, when --verbose
// did not exist yet, to keep the slot free; it now asserts who ended up in it.
// Reversing the two is a silent regression: -v would suddenly print a version
// string instead of tracing, and no other test would notice.
func TestShorthandVBelongsToVerbose(t *testing.T) {
	root := NewRootCmd("dev", "abc1234")
	root.InitDefaultVersionFlag()

	f := root.Flags().ShorthandLookup("v")
	if f == nil {
		t.Fatal("-v n'est attribué à rien")
	}
	if f.Name != "verbose" {
		t.Errorf("-v est pris par --%s, il appartient à --verbose", f.Name)
	}
}

// No argument: help on stdout, exit 0.
func TestNoArgsPrintsHelp(t *testing.T) {
	stdout, _, err := run(t)
	if err != nil {
		t.Fatalf("bare invocation returned an error: %v", err)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout does not look like help output: %q", stdout)
	}
}

// The `version` verb reads the node, not the binary. Without credentials it
// must fail — and fail with exit code 3, before any socket is opened.
func TestVersionCommandTargetsTheNodeNotTheBinary(t *testing.T) {
	t.Setenv("PVECTL_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvEndpoint, "https://pve.invalid:8006")
	t.Setenv(config.EnvTokenID, "automation@pve!pvectl")
	t.Setenv(config.EnvTokenSecret, "")

	stdout, _, err := run(t, "version")
	if err == nil {
		t.Fatal("`version` sans secret doit échouer")
	}
	if strings.Contains(stdout, "commit abc1234") {
		t.Errorf("`version` a affiché la version du binaire: %q", stdout)
	}

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != pve.ExitAuth {
		t.Errorf("le code de sortie doit être %d, got: %v", pve.ExitAuth, err)
	}
}
