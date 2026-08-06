package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dev-toolings/pvecli/internal/pve"
)

func newLXCExecCmd() *cobra.Command {
	var timeout time.Duration

	c := &cobra.Command{
		Use:   "exec <vmid> -- <commande shell…>",
		Short: "Exécute une commande DANS un conteneur, sans SSH",
		Long: `Lance une ligne de shell dans le conteneur et rend sa sortie et son code de retour.

Ce n'est PAS « vm agent exec ». Une VM porte un agent qui parle l'API ; un
conteneur partage le noyau de l'hôte et son « exec » vit côté hôte (pct exec),
hors de l'API REST. Le seul canal que l'API laisse ouvert vers l'intérieur est
la console : termproxy fabrique un ticket, vncwebsocket ouvre un PTY, et on y
tape la commande. D'où deux différences à connaître :

  - il y a un VRAI shell derrière : « cd /x && y | z » fonctionne, sans --shell.
    L'argument est toujours donné au shell du conteneur.
  - c'est un terminal, pas un execve : stdout et stderr y sont mêlés, et une
    sortie binaire ou colossale n'est pas ce pour quoi c'est fait. Pour du
    volumineux, redirige vers un fichier dans le conteneur.

  pvecli lxc exec 221 -- hostname
  pvecli lxc exec 221 -- 'apt-get update && apt-get install -y postgresql'

Le code de retour de la commande devient celui de pvecli, pour qu'un script en
tienne compte. Le --wait borne l'attente côté client.

Endpoints : POST /nodes/{node}/lxc/{vmid}/termproxy puis GET …/vncwebsocket`,
		Args: usage(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			script := strings.Join(args[1:], " ")

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

			res, err := client.LXCExec(ctx, node, vmid, script)
			if err != nil {
				return err
			}

			if res.Output != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Output)
			}
			if res.Truncated {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"note : sortie tronquée (dépasse 8 Mio) — redirige vers un fichier dans le conteneur pour tout garder")
			}
			if res.ExitCode != 0 {
				return &exitError{code: res.ExitCode,
					msg: fmt.Sprintf("la commande a rendu %d dans le conteneur %d", res.ExitCode, vmid)}
			}
			return nil
		},
	}

	f := c.Flags()
	f.DurationVar(&timeout, "wait", 15*time.Minute, "combien de temps attendre la fin")
	return c
}
