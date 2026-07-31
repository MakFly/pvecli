package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/service"
	"github.com/spf13/cobra"
)

// addWriteFlags declares the guardrails every write command carries.
func addWriteFlags(c *cobra.Command) {
	c.Flags().Bool("dry-run", false, "affiche la requête qui serait envoyée, sans l'envoyer")
	c.Flags().Bool("yes", false, "ne demande pas confirmation (usage script)")
}

// cliGate is the interactive half of PRD §7.6.
type cliGate struct {
	cmd *cobra.Command
	yes bool
}

func (g cliGate) Allow(target string, destructive bool) error {
	if g.yes {
		return nil
	}
	if !stdinIsTerminal() {
		return &exitError{
			code: pve.ExitConfirm,
			msg: "confirmation impossible : l'entrée standard n'est pas un terminal.\n" +
				"Relance depuis un terminal, ou passe --yes si tu assumes l'écriture.",
		}
	}

	if !destructive {
		return confirm(g.cmd, fmt.Sprintf("Appliquer sur %s ?", target))
	}

	// Retyping the identifier, not answering "y". The point is to make the
	// operator look at *which* target they are about to destroy — a reflexive
	// "y" costs nothing and protects nothing.
	_, _ = fmt.Fprintf(g.cmd.ErrOrStderr(),
		"Opération destructive sur %s.\nRetape son identifiant pour confirmer : ", target)

	answer, err := bufio.NewReader(g.cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return &exitError{code: pve.ExitConfirm, msg: "confirmation interrompue"}
	}
	if strings.TrimSpace(answer) != target {
		return &exitError{code: pve.ExitConfirm, msg: "identifiant non confirmé — rien n'a été fait"}
	}
	return nil
}

// newRunner assembles the mutation pipeline for one command invocation.
func newRunner(cmd *cobra.Command, client *pve.Client) *service.Runner {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")

	return &service.Runner{
		Progress: cmd.ErrOrStderr(),
		DryRun:   dryRun,
		Gate:     cliGate{cmd: cmd, yes: yes},
		Waiter: &service.TaskWaiter{
			API:      client,
			Progress: cmd.ErrOrStderr(),
			// A spinner in a CI log is noise, not progress.
			Quiet: !output.IsTerminal(cmd.ErrOrStderr()),
		},
	}
}

// cliGroup names the command family an operator actually types.
//
// The API calls the QEMU family "qemu"; this CLI calls it "vm". A message
// suggesting « pvecli qemu shutdown 900 » hands over a command that does not
// exist — the guest type is not a substitute for the command name.
func cliGroup(kind pve.GuestType) string {
	if kind == pve.TypeLXC {
		return "lxc"
	}
	return "vm"
}

// guestState adapts a guest's runtime status to what the pipeline needs.
func guestState(st *pve.GuestStatus) service.State {
	return service.State{
		Exists:  true,
		Lock:    st.Lock,
		Status:  st.Status,
		Summary: fmt.Sprintf("vmid %d — statut %s", st.VMID, st.Status),
		Raw:     st,
	}
}
