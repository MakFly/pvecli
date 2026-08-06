package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

func newVMAgentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Interroge l'agent QEMU d'une VM",
		Long: `Interroge l'agent invité.

PVE ne connaît pas l'adresse IP d'une VM en DHCP. L'hyperviseur voit une adresse
MAC sur un pont, et rien de plus : c'est l'invité qui sait dans quel réseau il
s'est inséré. L'agent est le seul canal par lequel il peut le dire.

C'est le lien entre l'hyperviseur et l'inventaire d'automatisation : sans agent,
« iac inventory » (PVX-042) ne peut pas savoir où se connecter.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newAgentIfacesCmd(), newAgentExecCmd())
	return c
}

func newAgentIfacesCmd() *cobra.Command {
	var all bool

	c := &cobra.Command{
		Use:   "ifaces <vmid>",
		Short: "Liste les interfaces vues depuis l'invité",
		Long: `Affiche les interfaces réseau telles que l'invité les voit, avec leurs adresses.

L'interface de loopback est masquée par défaut : elle est toujours là et
n'apprend rien. --all la montre.

Endpoint : GET /api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			ifaces, err := client.AgentInterfaces(cmd.Context(), node, vmid)
			if err != nil {
				return err
			}

			kept := make([]pve.AgentInterface, 0, len(ifaces))
			for _, i := range ifaces {
				if !all && i.IsLoopback() {
					continue
				}
				kept = append(kept, i)
			}

			rows := output.Rows{Headers: []string{"INTERFACE", "MAC", "ADRESSES"}}
			for _, i := range kept {
				addrs := make([]string, 0, len(i.IPAddresses))
				for _, a := range i.IPAddresses {
					addrs = append(addrs, fmt.Sprintf("%s/%d", a.Address, a.Prefix))
				}
				rows.Cells = append(rows.Cells, []string{
					i.Name, firstNonEmpty(i.HardwareAddress, "—"), strings.Join(addrs, "  "),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, kept, rows)
		},
	}

	c.Flags().BoolVar(&all, "all", false, "montre aussi l'interface de loopback")
	addRenderFlags(c)
	return c
}

func newVMIPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ip <vmid>",
		Short: "Affiche la première IPv4 non-loopback de la VM",
		Long: `Affiche une seule adresse, sans en-tête ni décoration, pour être utilisable
directement dans un script :

    ssh debian@$(pvecli vm ip 212)

Si l'agent ne répond pas, la commande échoue avec le message qui explique quoi
installer — elle ne renvoie jamais une chaîne vide qu'un script prendrait pour
une adresse.

Endpoint : GET /api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			ifaces, err := client.AgentInterfaces(cmd.Context(), node, vmid)
			if err != nil {
				return err
			}
			for _, i := range ifaces {
				if ip := i.FirstIPv4(); ip != "" {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), ip)
					return nil
				}
			}
			return fmt.Errorf("la VM %d n'expose aucune adresse IPv4 non-loopback — l'agent répond mais l'invité n'a pas d'adresse", vmid)
		},
	}
}

// newAgentExecCmd runs a command inside the guest, through the hypervisor.
//
// Ce que ça remplace : un SSH. Et ce n'est pas une commodité — administrer une
// VM par SSH suppose un compte, une clé déposée, un port ouvert et un réseau
// qui marche. Quatre choses qui peuvent manquer, et qui manquent précisément le
// jour où l'on a besoin d'entrer. L'agent passe par l'hyperviseur : il ne
// dépend ni du réseau de l'invité, ni de sshd, ni d'une clé.
func newAgentExecCmd() *cobra.Command {
	var (
		shell   bool
		timeout time.Duration
		poll    time.Duration
	)

	c := &cobra.Command{
		Use:   "exec <vmid> -- <commande> [args…]",
		Short: "Exécute une commande DANS la VM, sans SSH",
		Long: `Lance une commande dans l'invité et rend sa sortie et son code de retour.

Il n'y a pas de shell derrière cet appel. « exec » lance un exécutable avec des
arguments : « cd /x && y | z » ne veut rien dire pour lui. Pour une ligne de
shell, il faut la donner à un shell — c'est ce que fait --shell :

  pvecli vm agent exec 210 -- hostname
  pvecli vm agent exec 210 --shell 'cd /opt/app && docker compose ps'

La sortie n'est pas un flux : l'agent la bufferise et la rend à la fin. Rien ne
défile, on attend, puis on lit tout. Le code de retour de la commande devient
celui de pvecli, pour qu'un script puisse en tenir compte.

Le délai --wait borne l'ATTENTE, pas la commande : si le client renonce, le
processus continue de tourner dans l'invité, et le message le dit avec son PID.

Prérequis : qemu-guest-agent installé et démarré dans la VM, et « agent=1 » sur
la VM côté PVE. Sans lui, l'hyperviseur n'a aucun canal vers l'intérieur.

Endpoints : POST /nodes/{node}/qemu/{vmid}/agent/exec puis GET …/agent/exec-status`,
		Args: usage(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			rest := args[1:]

			argv := rest
			if shell {
				// Une seule chaîne, donnée à sh -c. Découper nous-mêmes serait
				// réinventer un analyseur de shell, et le réinventer mal.
				argv = []string{"/bin/sh", "-c", strings.Join(rest, " ")}
			}

			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			res, err := client.AgentExec(ctx, node, vmid, argv, poll)
			if err != nil {
				return err
			}

			// La sortie de l'invité va sur NOS flux, chacun sur le sien : un
			// script qui redirige stdout ne doit pas récolter les diagnostics.
			if res.OutData != "" {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), res.OutData)
			}
			if res.ErrData != "" {
				_, _ = fmt.Fprint(cmd.ErrOrStderr(), res.ErrData)
			}
			if res.OutTruncated || res.ErrTruncated {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"note : sortie tronquée par l'agent — redirige vers un fichier dans l'invité pour tout garder")
			}
			if res.ExitCode != 0 {
				// Le code de la commande devient le nôtre : sans ça, un script
				// qui pilote une VM verrait réussir ce qui a échoué dedans.
				return &exitError{code: res.ExitCode,
					msg: fmt.Sprintf("la commande a rendu %d dans la VM %d", res.ExitCode, vmid)}
			}
			return nil
		},
	}

	f := c.Flags()
	f.BoolVar(&shell, "shell", false, "passer l'argument à « /bin/sh -c » au lieu de l'exécuter directement")
	f.DurationVar(&timeout, "wait", 15*time.Minute, "combien de temps attendre la fin (un build est long)")
	f.DurationVar(&poll, "poll", 2*time.Second, "intervalle entre deux demandes d'état")
	return c
}
