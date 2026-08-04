package nag

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SSH reaches the node through the operator's own ssh client.
//
// The system binary is used rather than a Go ssh library, and that is a choice,
// not laziness. It means this command inherits ~/.ssh/config, the agent, jump
// hosts, per-host keys and known_hosts as they are already configured — the
// operator's existing way into their hypervisor keeps working, and pvecli does
// not become a second place where host keys are trusted.
type SSH struct {
	Host string
	User string
}

// Target is what appears in messages: what pvecli is about to log into.
func (s SSH) Target() string { return s.User + "@" + s.Host }

// Look reports whether an ssh client exists at all.
func (s SSH) Look() error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return errors.New("« ssh » est introuvable dans le PATH.\n\n" +
			"Cette commande modifie un fichier sur le disque du nœud : aucun endpoint de\n" +
			"l'API PVE n'y donne accès, il faut donc un shell")
	}
	return nil
}

// Runner feeds the script to `sh -s` on the node.
//
// The script travels on stdin instead of being passed as an argument: it spans
// several lines and carries quotes, and an argument would be re-parsed by the
// remote login shell — one more shell to reason about, for nothing.
//
// BatchMode=yes is deliberate. Without it, a node that does not accept the key
// drops into an interactive password prompt in the middle of a pipeline; with
// it, the failure is immediate and says what is wrong.
func (s SSH) Runner() Runner {
	return func(ctx context.Context, script string) (string, error) {
		var stdout, stderr bytes.Buffer

		c := exec.CommandContext(ctx, "ssh",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=10",
			s.Target(), "sh", "-s")
		c.Stdin = strings.NewReader(script)
		c.Stdout, c.Stderr = &stdout, &stderr
		c.Env = os.Environ()

		err := c.Run()
		if err == nil {
			return stdout.String(), nil
		}
		return stdout.String(), s.failure(err, stderr.String())
	}
}

// failure turns ssh's terse exit into something actionable.
//
// ssh exits 255 for its own failures and relays the remote exit code otherwise,
// so the two cases get different advice: one is about getting in, the other is
// about what happened once in.
func (s SSH) failure(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)

	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 255 {
		return fmt.Errorf("connexion ssh à %s impossible.\n\n%s\n\n"+
			"pvecli n'ouvre pas de session interactive (BatchMode) : il faut une clé déjà\n"+
			"autorisée sur le nœud.\n"+
			"  ssh-copy-id %s", s.Target(), indent(msg), s.Target())
	}
	if msg != "" {
		return fmt.Errorf("le script a échoué sur %s :\n\n%s", s.Target(), indent(msg))
	}
	return fmt.Errorf("le script a échoué sur %s : %w", s.Target(), err)
}

func indent(s string) string {
	if s == "" {
		return "  (aucun message)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
