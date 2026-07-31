package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/MakFly/pvectl/internal/output"
	"github.com/MakFly/pvectl/internal/pve"
	"github.com/spf13/cobra"
)

func newNodeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "node",
		Short: "Inspecte les nœuds du cluster",
		Long: `Liste et décrit les nœuds.

Presque tous les chemins de l'API PVE sont préfixés « /nodes/{node}/ » : le nom
du nœud n'est pas un détail d'affichage, c'est une donnée structurante que
toutes les autres commandes exigeront.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newNodeListCmd(), newNodeShowCmd())
	return c
}

func newNodeListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Short:   "Liste les nœuds (GET /nodes)",
		Aliases: []string{"list"},
		Long: `Liste les nœuds du cluster avec leur charge.

Si le cluster n'a qu'un seul nœud et qu'aucun nœud par défaut n'est configuré,
celui-ci le devient : sur un mono-nœud, retaper --node à chaque commande n'a
aucune valeur pédagogique.

Endpoint : GET /api2/json/nodes`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			nodes, err := client.Nodes(cmd.Context())
			if err != nil {
				return err
			}

			// json/yaml serialise the typed API value itself, so that
			// `| jq '.[0].node'` sees the same field names the node returned.
			if err := output.Render(cmd.OutOrStdout(), opts, nodes, nodeRows(nodes)); err != nil {
				return err
			}

			adoptSingleNode(cmd, nodes)
			return nil
		},
	}
	addRenderFlags(c)
	return c
}

// nodeRows is the human projection of a node listing.
func nodeRows(nodes []pve.Node) output.Rows {
	rows := output.Rows{Headers: []string{"NOM", "STATUT", "CPU", "RAM", "DISQUE", "UPTIME"}}
	for _, n := range nodes {
		rows.Cells = append(rows.Cells, []string{
			n.Node,
			n.Status,
			fmt.Sprintf("%s (%d cœurs)", output.Ratio(n.CPU), n.MaxCPU),
			fmt.Sprintf("%s / %s", output.Bytes(n.Mem), output.Bytes(n.MaxMem)),
			fmt.Sprintf("%s / %s", output.Bytes(n.Disk), output.Bytes(n.MaxDisk)),
			output.Uptime(n.Uptime),
		})
	}
	return rows
}

// adoptSingleNode records the only node as the default, once. Failing to write
// the config must not turn a successful listing into an error.
func adoptSingleNode(cmd *cobra.Command, nodes []pve.Node) {
	if len(nodes) != 1 {
		return
	}
	eff, err := resolveConfig(cmd)
	if err != nil || eff.Node != "" {
		return
	}
	if _, err := writeKey(cmd, "node", nodes[0].Node); err != nil {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"nœud par défaut fixé à « %s » (seul nœud du cluster)\n", nodes[0].Node)
}

func newNodeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [node]",
		Short: "Décrit un nœud (GET /nodes/{node}/status)",
		Long: `Affiche le détail d'un nœud : noyau, version PVE, charge, mémoire, rootfs.

Sans argument, utilise le nœud par défaut de la configuration.

Endpoint : GET /api2/json/nodes/{node}/status`,
		Args: usage(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			node, err := targetNode(cmd, args)
			if err != nil {
				return err
			}

			st, err := client.NodeStatus(cmd.Context(), node)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			line := func(k, v string) { _, _ = fmt.Fprintf(w, "%s\t%s\n", k, v) }

			line("nœud", node)
			line("version PVE", st.PVEVersion)
			line("noyau", st.KVersion)
			line("processeur", fmt.Sprintf("%s — %d cœurs / %d threads",
				st.CPUInfo.Model, st.CPUInfo.Cores, st.CPUInfo.CPUs))
			line("charge CPU", output.Ratio(st.CPU))
			line("load average", strings.Join(st.LoadAvg, " "))
			line("mémoire", fmt.Sprintf("%s utilisés sur %s",
				output.Bytes(st.Memory.Used), output.Bytes(st.Memory.Total)))
			line("rootfs", fmt.Sprintf("%s utilisés sur %s (%s libres)",
				output.Bytes(st.RootFS.Used), output.Bytes(st.RootFS.Total), output.Bytes(st.RootFS.Avail)))
			line("uptime", output.Uptime(st.Uptime))

			return w.Flush()
		},
	}
}

// targetNode resolves which node a command acts on: the argument, else the
// configured default. Failing early with a pointer to `node ls` beats a 404
// three layers down.
func targetNode(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	eff, err := resolveConfig(cmd)
	if err != nil {
		return "", err
	}
	if eff.Node == "" {
		return "", fmt.Errorf("aucun nœud indiqué et aucun nœud par défaut — lance « pvectl node ls », ou passe --node")
	}
	return eff.Node, nil
}
