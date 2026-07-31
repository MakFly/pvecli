package iac

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Tool is an external command pvecli wraps rather than replaces.
//
// The whole design of the `iac` commands rests on one rule: pvecli does not
// reimplement Terraform or Ansible, and does not paraphrase them either. It
// runs them in the right directory, with the right environment, and relays
// their output and exit code untouched. Anything else would produce a CLI that
// is wrong in a different way from the tool it wraps — the worst of both.
type Tool struct {
	// Name is the binary, looked up in PATH.
	Name string
	// Dir is the working directory. The tools are directory-scoped: terraform
	// reads the .tf files where it stands, ansible reads ansible.cfg the same
	// way. Running them from pvecli's own cwd would silently use another
	// configuration.
	Dir string
	// Env is appended to the inherited environment.
	Env []string
}

// installHints says how to get each tool, because "command not found" is a
// dead end and the answer is different on every platform.
var installHints = map[string]string{
	"terraform": `  macOS   brew install hashicorp/tap/terraform
  autre   https://developer.hashicorp.com/terraform/install`,
	"ansible": `  macOS   brew install ansible
  autre   pipx install ansible  (ou : python3 -m pip install --user ansible)`,
}

func installHint(name string) string {
	if h, ok := installHints[name]; ok {
		return h
	}
	// ansible-playbook and ansible-inventory ship with ansible.
	if len(name) > 8 && name[:8] == "ansible-" {
		return installHints["ansible"]
	}
	return ""
}

// MissingToolError is a binary pvecli needs and cannot find.
type MissingToolError struct {
	Name string
}

func (e *MissingToolError) Error() string {
	msg := fmt.Sprintf("« %s » est introuvable dans le PATH.\n\n"+
		"pvecli n'implémente pas %s, il l'encadre : sans le binaire, il n'y a rien à encadrer.", e.Name, e.Name)
	if h := installHint(e.Name); h != "" {
		msg += "\n\n" + h
	}
	return msg
}

// ExitCode keeps a missing tool distinct from a tool that ran and refused.
func (e *MissingToolError) ExitCode() int { return 1 }

// MissingDirError is a directory the configuration does not name, or names
// wrongly. It is its own error because the fix is a `config set`, not a retry.
type MissingDirError struct {
	Key  string
	Path string
}

func (e *MissingDirError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("« %s » n'est pas configuré.\n\n"+
			"Indique où vit le dépôt d'infrastructure :\n"+
			"  pvecli config set %s /chemin/vers/le/dossier", e.Key, e.Key)
	}
	return fmt.Sprintf("« %s » pointe vers %s, qui n'est pas un dossier accessible.\n\n"+
		"  pvecli config set %s /chemin/vers/le/dossier", e.Key, e.Path, e.Key)
}

// ExitCode: a misconfiguration is a usage error, not a node failure.
func (e *MissingDirError) ExitCode() int { return 2 }

// ExitError carries the wrapped tool's own exit code.
//
// `pvecli iac plan` exits with what terraform exited with, deliberately: a CI
// job that branches on `terraform plan`'s status must keep working when the
// call is put behind pvecli. This is the one place where the exit table of PRD
// §7.5 does not apply, and the help says so.
type ExitError struct {
	Tool string
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s a terminé avec le code %d", e.Tool, e.Code)
}

func (e *ExitError) ExitCode() int { return e.Code }

// CheckDir verifies the working directory before anything is launched.
func CheckDir(key, dir string) error {
	if dir == "" {
		return &MissingDirError{Key: key}
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return &MissingDirError{Key: key, Path: dir}
	}
	return nil
}

// Look reports whether the binary exists, with a message that says how to get
// it. Called before the working directory is even considered: "terraform is not
// installed" and "terraform_dir is wrong" are different problems and must not
// be reported as one.
func (t Tool) Look() error {
	if _, err := exec.LookPath(t.Name); err != nil {
		return &MissingToolError{Name: t.Name}
	}
	return nil
}

// command builds the process. The binary name comes from this package's own
// constants, never from user input.
func (t Tool) command(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, t.Name, args...) //nolint:gosec // t.Name is a package constant, not user input
	c.Dir = t.Dir
	c.Env = append(os.Environ(), t.Env...)
	return c
}

// Run streams the tool's output straight through and relays its exit code.
//
// stdout and stderr are not captured, reordered or prefixed: a terraform plan
// keeps its colours, its progress and its line breaks. A wrapper that rewrites
// the wrapped tool's output teaches the operator the wrapper instead of the
// tool.
func (t Tool) Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	c := t.command(ctx, args...)
	c.Stdout, c.Stderr = stdout, stderr

	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Tool: t.Name, Code: ee.ExitCode()}
	}
	return err
}

// Output captures stdout for the commands whose result pvecli parses, and lets
// stderr through so the tool can still explain itself.
func (t Tool) Output(ctx context.Context, stderr io.Writer, args ...string) ([]byte, error) {
	var buf bytes.Buffer
	c := t.command(ctx, args...)
	c.Stdout, c.Stderr = &buf, stderr

	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return buf.Bytes(), &ExitError{Tool: t.Name, Code: ee.ExitCode()}
		}
		return buf.Bytes(), err
	}
	return buf.Bytes(), nil
}
