package cmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/service"
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
	c.AddCommand(newNodeListCmd(), newNodeShowCmd(), newNodeRebootCmd(), newNodeNagCmd())
	return c
}

// nodeReturnProbe waits for a node to come back from a reboot.
//
// It takes a status function rather than a client, and its interval rather than
// a constant, for one reason: the guarantee below is the whole point of the
// command, and a guarantee no test can contradict is not a guarantee. With a
// client and a hardcoded 5s sleep, `wait` could only be exercised against a
// real rebooting node — that is to say, never.
type nodeReturnProbe struct {
	status   func(context.Context) (*pve.NodeStatus, error)
	interval time.Duration
	timeout  time.Duration
	progress io.Writer
}

// wait polls until the node answers with an uptime LOWER than the one measured
// before the reboot.
//
// Why uptime and not reachability: the node keeps answering for several seconds
// after accepting the command, while systemd walks down its units. A probe that
// stops at the first successful GET therefore reports "back up" about a machine
// that has not gone down yet — the single most misleading answer this command
// could give. Uptime rises monotonically and can only fall across a boot, so a
// lower value is the one observation a node that never rebooted cannot produce.
func (p nodeReturnProbe) wait(ctx context.Context, node string, before int64) (*pve.NodeStatus, error) {
	deadline := time.Now().Add(p.timeout)
	for {
		st, err := p.status(ctx)
		// Errors are expected here, and are the normal course of events: the
		// node is unreachable while it reboots. Only the deadline ends the loop.
		if err == nil && st.Uptime < before {
			return st, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"le nœud « %s » n'est pas revenu au bout de %s.\n"+
					"la demande de redémarrage a bien été acceptée — ce délai ne dit PAS qu'elle a échoué,\n"+
					"il dit que je ne peux pas le prouver d'ici. Regarde la console physique,\n"+
					"puis : pvecli node show %s", node, p.timeout, node)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.interval):
		}
		if p.progress != nil {
			_, _ = fmt.Fprint(p.progress, ".")
		}
	}
}

func newNodeRebootCmd() *cobra.Command {
	var (
		wait     time.Duration
		skipWait bool
	)

	c := &cobra.Command{
		Use:   "reboot [node]",
		Short: "Redémarre le nœud (POST /nodes/{node}/status)",
		Long: `Redémarre le nœud entier — donc arrête TOUS ses invités.

CE N'EST PAS « pvecli vm reboot ». Celui-ci redémarre une machine virtuelle ;
celui-là redémarre l'hyperviseur qui les porte toutes. Les invités sont arrêtés
par « pve-guests », et seuls ceux qui portent « onboot=1 » repartiront ensuite.
Vérifie-le AVANT :

  pvecli vm show <vmid>    et    pvecli lxc show <vmid>

Un invité en onboot=0 ne redémarrera pas tout seul, et personne ne te le dira.

PRIVILÈGE : « Sys.PowerMgmt » sur /nodes/{node}. Ce n'est pas « Sys.Modify » —
un token qui peut réécrire les dépôts APT du nœud ne peut pas pour autant le
redémarrer, et cette séparation est voulue. Aucun rôle intégré ne le porte hors
« Administrator » : il faut un rôle sur mesure (voir « pvecli access role add »).

CE QUE LA COMMANDE PROUVE. L'API ne rend aucun UPID ici — un nœud ne peut pas
rapporter sur une tâche dont l'objet est qu'il cesse de répondre. Le HTTP 200
est donc une acceptation, pas un succès. La preuve est prise de l'extérieur :
on relève l'uptime avant, puis on attend qu'il REDESCENDE. Un simple « le nœud
répond de nouveau » ne prouverait rien, parce qu'il répond encore pendant les
premières secondes de son arrêt.

Endpoint : POST /api2/json/nodes/{node}/status  (command=reboot)`,
		Args: usage(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, args)
			if err != nil {
				return err
			}

			var uptimeBefore int64
			payload := url.Values{}
			payload.Set("command", "reboot")

			result, err := newRunner(cmd, client).Run(cmd.Context(), service.Mutation{
				Target: node,
				// Retaper le nom du nœud, pas « oui ». Cette commande coupe
				// tous les invités à la fois : elle a le rayon de souffle le
				// plus large de toute la CLI.
				Destructive: true,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     "/nodes/" + node + "/status",
					Payload:  payload,
					Effect:   "le nœud " + node + " redémarre — TOUS ses invités sont arrêtés",
					Rollback: "aucun : un redémarrage ne se défait pas. Seuls les invités en onboot=1 repartiront",
					Verify:   "attente du retour du nœud, prouvé par un uptime plus bas qu'avant",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.NodeStatus(ctx, node)
					if err != nil {
						return service.State{}, err
					}
					uptimeBefore = st.Uptime
					return service.State{
						Exists:  true,
						Status:  "online",
						Summary: "uptime " + output.Uptime(st.Uptime),
						Raw:     st,
					}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.RebootNode(ctx, node)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					if skipWait {
						// Assumé et dit : on ne prouve rien. Le champ Status
						// ne ment pas sur ce qu'on sait.
						return service.State{
							Exists: true, Status: "redémarrage demandé",
							Summary: "--no-wait : retour du nœud non vérifié",
						}, nil
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"le nœud redémarre — attente de son retour (uptime < %s), %s au plus",
						output.Uptime(uptimeBefore), wait)
					probe := nodeReturnProbe{
						status:   func(ctx context.Context) (*pve.NodeStatus, error) { return client.NodeStatus(ctx, node) },
						interval: 5 * time.Second,
						timeout:  wait,
						progress: cmd.ErrOrStderr(),
					}
					st, err := probe.wait(ctx, node, uptimeBefore)
					_, _ = fmt.Fprintln(cmd.ErrOrStderr())
					if err != nil {
						return service.State{}, err
					}
					return service.State{
						Exists:  true,
						Status:  "online",
						Summary: "revenu — uptime " + output.Uptime(st.Uptime) + ", noyau " + st.KVersion,
						Raw:     st,
					}, nil
				},
			})
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"nœud", node}, {"statut", result.Status}, {"détail", result.Summary},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	c.Flags().DurationVar(&wait, "wait", 10*time.Minute, "délai accordé au nœud pour revenir")
	c.Flags().BoolVar(&skipWait, "no-wait", false, "rend la main sans attendre le retour — ne prouve alors rien")
	addWriteFlags(c)
	addRenderFlags(c)
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
		return "", fmt.Errorf("aucun nœud indiqué et aucun nœud par défaut — lance « pvecli node ls », ou passe --node")
	}
	return eff.Node, nil
}
