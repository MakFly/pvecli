// Package secret stores generated credentials outside the filesystem.
//
// The project's own API token has always lived in the OS keychain and reached
// the process through the environment. A password this tool generates for a
// service it installed deserves the same treatment: printing it into a terminal
// scrollback, or worse into a table that `-o json` would capture, turns a
// credential into something that ends up in a log.
package secret

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Ref names one entry. Service groups a host's secrets, Account is the key
// inside it, so `security find-generic-password -s pvecli-app-01 -a
// postgresql.password -w` reads back exactly one value.
type Ref struct {
	Service string
	Account string
}

func (r Ref) String() string { return r.Service + " / " + r.Account }

// ReadCommand is what the operator types to get the value back. Returned rather
// than executed: this tool never needs to read these secrets, only to put them
// somewhere the operator can.
func (r Ref) ReadCommand() string {
	return fmt.Sprintf("security find-generic-password -s %q -a %q -w", r.Service, r.Account)
}

// Available reports whether a keychain this package can write to exists.
//
// macOS only, deliberately. Linux has several competing secret services and
// picking one silently would mean the operator does not know where their
// password went — on that platform the value is printed once instead, which is
// at least honest about where it now is.
func Available() bool {
	return runtime.GOOS == "darwin" && lookPath("security") == nil
}

// Store writes a secret, replacing any previous value under the same Ref.
//
// The value goes through stdin-free arguments, so it is visible to `ps` for the
// lifetime of the call. That is a real weakness of the `security` CLI and the
// reason this is used for generated service passwords and never for the PVE
// token, which the CLI refuses to accept as an argument at all.
func Store(ref Ref, value string) error {
	if !Available() {
		return fmt.Errorf("aucun trousseau accessible sur %s", runtime.GOOS)
	}
	// -U updates in place rather than failing on an existing entry, which is
	// what makes re-running a playbook idempotent from the keychain's point of
	// view too.
	cmd := exec.Command("security", "add-generic-password",
		"-U", "-s", ref.Service, "-a", ref.Account, "-w", value,
		"-D", "pvecli generated service credential")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("écriture dans le trousseau (%s) : %w — %s", ref, err, out)
	}
	return nil
}

// lookPath is a variable so tests can pretend the tool is missing.
var lookPath = func(name string) error {
	_, err := exec.LookPath(name)
	return err
}
