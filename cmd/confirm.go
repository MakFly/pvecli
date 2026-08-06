package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// exitError attaches an exit code of PRD §7.5 to a plain message, for failures
// that originate in the CLI layer rather than in the API client.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

// usage wraps a positional-argument validator so a misuse exits with code 2
// (usage) instead of 1 (generic). Cobra reports both the same way; a script
// cannot tell "I typed it wrong" from "the node said no" without this.
func usage(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := v(c, args); err != nil {
			return &exitError{code: pve.ExitUsage, msg: err.Error()}
		}
		return nil
	}
}

// confirm asks a yes/no question on stderr and reads the answer from stdin.
//
// It refuses cleanly, with exit code 5, when there is no terminal to ask on —
// rather than blocking forever or silently assuming yes. This is the lesson the
// lab already taught the hard way with `ssh host passwd`: a command that needs
// a TTY must check for one up front, not fail halfway through.
//
// PVX-020 generalises this for every write command; `config trust` is its first
// caller.
func confirm(cmd *cobra.Command, question string) error {
	if !stdinIsTerminal() {
		return &exitError{
			code: pve.ExitConfirm,
			msg: "confirmation impossible : l'entrée standard n'est pas un terminal.\n" +
				"Relance depuis un terminal, ou passe --yes si tu assumes la réponse.",
		}
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [o/N] ", question)

	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return &exitError{code: pve.ExitConfirm, msg: "confirmation interrompue"}
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "o", "oui", "y", "yes":
		return nil
	default:
		return &exitError{code: pve.ExitConfirm, msg: "confirmation refusée"}
	}
}

// stdinIsTerminal reports whether stdin is an actual terminal.
//
// The tempting shortcut — os.Stdin.Stat() and a test on os.ModeCharDevice — is
// wrong, and this project found out the practical way: `pvecli config trust
// </dev/null` sailed straight past it, because /dev/null *is* a character
// device. The command then asked its question into the void and failed on the
// read instead of refusing up front. term.IsTerminal issues the ioctl that
// actually answers the question.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
