package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/service"
	"github.com/spf13/cobra"
)

func newNetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "net",
		Short: "Inspecte la configuration réseau du nœud, et l'applique",
		Long: `Lit le chemin réseau du nœud, applique les modifications en attente, ou les annule.

PVE sépare deux gestes que l'interface web enchaîne sans le dire :

  ÉCRIRE la configuration    →  /etc/network/interfaces.new
  APPLIQUER la configuration →  PUT /nodes/{node}/network

Entre les deux, rien n'a bougé sur le réseau. C'est ce délai qui permet de se
rattraper : « net revert » jette le brouillon et il ne s'est rien passé. Le
réflexe s'apprend AVANT d'en avoir besoin, parce qu'après, on n'a plus la main
pour le taper.

En v1, cette CLI ne crée ni ne modifie d'interface : lecture, application et
annulation seulement. Écrire une interface se fait dans l'interface web ou avec
« pvesh », là où le formulaire vérifie ce qu'on saisit.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newNetListCmd(), newNetShowCmd(), newNetApplyCmd(), newNetRevertCmd())
	return c
}

func newNetListCmd() *cobra.Command {
	var kind string

	c := &cobra.Command{
		Use:     "ls [node]",
		Aliases: []string{"list"},
		Short:   "Liste les interfaces (GET /nodes/{node}/network)",
		Long: `Liste les interfaces réseau du nœud.

La colonne ATTENTE est la seule qui compte avant d'agir : elle marque les
interfaces qu'une modification non appliquée touche. Elle ne vient pas d'un
champ de la réponse — PVE rend le diff en attente HORS de « data », dans un
attribut « changes » posé à côté (PVE/API2/Network.pm:418). Un client qui
déballe « data » et s'arrête là est aveugle à l'essentiel.

ACTIF dit que le lien est monté ; PRÉSENT que le matériel existe. Ni l'un ni
l'autre ne dit que la configuration sur disque correspond à ce qui tourne.

Endpoint : GET /api2/json/nodes/{node}/network`,
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

			cfg, err := client.Network(cmd.Context(), node, kind)
			if err != nil {
				return err
			}
			pending := cfg.PendingIfaces()

			rows := output.Rows{Headers: []string{
				"INTERFACE", "TYPE", "MÉTHODE", "ADRESSE", "PORTS", "ACTIF", "AUTOSTART", "ATTENTE",
			}}
			for _, i := range cfg.Ifaces {
				rows.Cells = append(rows.Cells, []string{
					i.Iface, i.Type, firstNonEmpty(i.Method, "—"),
					firstNonEmpty(i.CIDR, i.Address, "—"), firstNonEmpty(i.Ports(), "—"),
					yesNo(i.Active == 1), yesNo(i.Autostart == 1),
					yesNo(pending[i.Iface]),
				})
			}

			if err := output.Render(cmd.OutOrStdout(), opts, cfg, rows); err != nil {
				return err
			}
			// On stderr: the table stays pipeable, and the warning survives a
			// « | grep ». A pending change nobody noticed is how a node gets
			// rebooted into a configuration nobody chose.
			if cfg.Pending() && opts.Format == output.Table {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nModifications réseau EN ATTENTE sur %s — écrites, pas appliquées :\n\n%s\n"+
						"  pvecli net apply %s     les applique (coupe l'accès si elles sont fausses)\n"+
						"  pvecli net revert %s    les jette\n",
					node, indent(cfg.Changes, "  "), node, node)
			}
			return nil
		},
	}

	c.Flags().StringVar(&kind, "type", "",
		"filtre : bridge, bond, eth, vlan, alias, any_bridge…")
	addRenderFlags(c)
	return c
}

// indent prefixes every line, so a diff pasted into a warning stays readable
// as a block rather than merging with the surrounding text.
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func newNetShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show <iface> [node]",
		Short: "Détaille une interface (GET /nodes/{node}/network/{iface})",
		Args:  usage(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, args[1:])
			if err != nil {
				return err
			}

			iface, err := client.NetworkIface(cmd.Context(), node, args[0])
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}}
			for _, kv := range [][2]string{
				{"interface", iface.Iface}, {"type", iface.Type},
				{"méthode", iface.Method}, {"méthode6", iface.Method6},
				{"adresse", firstNonEmpty(iface.CIDR, iface.Address)},
				{"passerelle", iface.Gateway},
				{"ports", iface.Ports()},
				{"mode bond", iface.BondMode},
				{"vlan", iface.VLANID},
				{"actif", boolField(iface.Active == 1)},
				{"présent", boolField(iface.Exists == 1)},
				{"autostart", boolField(iface.Autostart == 1)},
				{"commentaire", strings.TrimSpace(iface.Comments)},
			} {
				if kv[1] != "" {
					rows.Cells = append(rows.Cells, []string{kv[0], kv[1]})
				}
			}
			return output.Render(cmd.OutOrStdout(), opts, iface, rows)
		},
	}
	addRenderFlags(c)
	return c
}

// boolField renders a flag only when it is set, so « non » never fills a
// detail table with lines that say nothing.
func boolField(b bool) string {
	if b {
		return "oui"
	}
	return ""
}

func newNetApplyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "apply [node]",
		Short: "Applique les modifications réseau en attente (PUT /nodes/{node}/network)",
		Long: `Applique la configuration écrite dans /etc/network/interfaces.new.

DANGER — C'EST LA COMMANDE QUI PEUT COUPER L'ACCÈS AU NŒUD.

Une adresse fausse, un pont sans port, une passerelle absente : le nœud reste
allumé et devient injoignable. Ni SSH ni cette CLI ne peuvent alors le
rattraper, parce que les deux passent par le réseau qu'on vient de casser.

  AVANT d'appliquer, assure-toi d'avoir un accès console : IPMI/iDRAC/iLO,
  ou un écran et un clavier physiquement branchés sur la machine.

La confirmation exige de retaper le nom du nœud. Ce n'est pas une formalité :
c'est le moment prévu pour relire le diff affiché par « pvecli net ls ».

Le geste de secours, à connaître avant d'en avoir besoin :

  pvecli net revert <node>    jette les modifications, tant qu'elles ne sont
                              pas appliquées

Endpoint : PUT /api2/json/nodes/{node}/network`,
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
			return runNetworkChange(cmd, client, node, netApply)
		},
	}
	addWriteFlags(c)
	return c
}

func newNetRevertCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "revert [node]",
		Short: "Annule les modifications réseau non appliquées (DELETE /nodes/{node}/network)",
		Long: `Jette /etc/network/interfaces.new sans rien appliquer.

C'est le réflexe de secours du chapitre : tant qu'un changement n'est pas
appliqué, il n'a rien cassé et il s'annule intégralement. Après « net apply »,
cette commande n'a plus rien à annuler — d'où l'intérêt de la connaître avant.

Endpoint : DELETE /api2/json/nodes/{node}/network`,
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
			return runNetworkChange(cmd, client, node, netRevert)
		},
	}
	addWriteFlags(c)
	return c
}

type netAction int

const (
	netApply netAction = iota
	netRevert
)

// runNetworkChange drives both writes through the same pipeline. The pre-read
// is the pending diff itself: applying nothing, or reverting nothing, is a
// mistake worth naming before the confirmation rather than after.
func runNetworkChange(cmd *cobra.Command, client *pve.Client, node string, action netAction) error {
	read := func(ctx context.Context) (*pve.NetworkConfig, error) {
		return client.Network(ctx, node, "")
	}

	plan := service.Plan{
		Node:   node,
		Method: "PUT",
		Path:   pve.NetworkApplyPath(node),
		Effect: "applique la configuration réseau en attente — peut rendre le nœud injoignable",
		Rollback: "AUCUN retour arrière par l'API : une fois appliquée, une configuration fausse\n" +
			"           se corrige depuis la console physique du nœud",
		Verify: "relecture de GET /nodes/" + node + "/network",
	}
	if action == netRevert {
		plan = service.Plan{
			Node:     node,
			Method:   "DELETE",
			Path:     pve.NetworkRevertPath(node),
			Effect:   "jette les modifications en attente — rien n'est appliqué",
			Rollback: "les modifications jetées sont perdues, il faut les ressaisir",
			Verify:   "relecture de GET /nodes/" + node + "/network",
		}
	}

	runner := newRunner(cmd, client)
	_, err := runner.Run(cmd.Context(), service.Mutation{
		Target: node,
		// Retyping the node name, for both. A revert is not destructive for
		// the node, but it destroys work someone did — and it is typed on the
		// same keyboard, minutes apart, as the apply.
		Destructive: true,
		Plan:        plan,

		PreRead: func(ctx context.Context) (service.State, error) {
			cfg, err := read(ctx)
			if err != nil {
				return service.State{}, err
			}
			if !cfg.Pending() {
				verb := "à appliquer"
				if action == netRevert {
					verb = "à annuler"
				}
				return service.State{}, fmt.Errorf(
					"aucune modification réseau en attente sur %s — rien %s.\n"+
						"Les modifications s'écrivent dans l'interface web ou avec « pvesh », "+
						"cette CLI ne fait que les appliquer ou les jeter", node, verb)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"Modifications en attente sur %s :\n\n%s\n", node, indent(cfg.Changes, "  "))
			return service.State{
				Exists: true, Status: "en attente",
				Summary: fmt.Sprintf("nœud %s — modifications réseau en attente", node),
				Raw:     cfg,
			}, nil
		},

		Write: func(ctx context.Context) (string, error) {
			if action == netRevert {
				// DELETE is synchronous: no UPID, nothing to poll.
				return "", client.RevertNetwork(ctx, node)
			}
			return client.ApplyNetwork(ctx, node)
		},

		PostRead: func(ctx context.Context) (service.State, error) {
			cfg, err := read(ctx)
			if err != nil {
				return service.State{}, err
			}
			if cfg.Pending() {
				return service.State{}, fmt.Errorf(
					"des modifications sont toujours en attente sur %s après l'opération", node)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"plus aucune modification en attente sur %s\n", node)
			return service.State{
				Exists: true, Status: "appliqué",
				Summary: fmt.Sprintf("nœud %s — configuration réseau à jour", node),
				Raw:     cfg,
			}, nil
		},
	})
	return err
}
