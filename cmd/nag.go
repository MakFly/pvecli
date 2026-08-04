package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/MakFly/pvecli/internal/nag"
	"github.com/MakFly/pvecli/internal/output"
	"github.com/spf13/cobra"
)

// newNodeNagCmd groups the three states of the subscription dialog.
func newNodeNagCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "nag",
		Short: "Neutralise (ou rétablit) la pop-up « no valid subscription »",
		Long: `Agit sur la boîte de dialogue « You do not have a valid subscription for this
server » que l'interface web affiche à chaque connexion.

CETTE COMMANDE NE PASSE PAS PAR L'API. C'est la seule de pvecli dans ce cas, et
ce n'est pas un oubli : la pop-up est produite par un fichier JavaScript posé sur
le disque du nœud,

  ` + nag.File + `

qu'aucun endpoint de /api2/json n'expose, quels que soient les privilèges du
token. Il faut donc un shell. pvecli se sert du client « ssh » du poste, avec ta
configuration existante (~/.ssh/config, agent, known_hosts) et sans mot de passe
interactif : la clé doit déjà être autorisée sur le nœud.

CE QUE LE PATCH FAIT. Une seule insertion textuelle, repérée par un marqueur :

  checked_command: function (orig_cmd) { orig_cmd(); return; /* pvecli:nag-off */

« orig_cmd() » est appelé avant de sortir : la commande que l'utilisateur a
réellement demandée s'exécute toujours, seule la vérification d'abonnement est
court-circuitée. Puis « pveproxy » est redémarré (l'interface web se recharge,
les invités ne bougent pas).

CE QUE LE PATCH NE FAIT PAS. Aucune sauvegarde « .bak » n'est laissée, aucun
hook APT n'est installé. Ce sont deux pièges classiques : un .bak restauré après
un « apt upgrade » réinstallerait une version périmée du fichier, et un hook APT
est un script qui réécrit en silence des fichiers de paquet pour toujours. Ici
« nag on » est l'inverse exact de « nag off », et « apt --reinstall install
proxmox-widget-toolkit » reste le filet de sécurité en toutes circonstances.

Conséquence assumée : une mise à jour de « proxmox-widget-toolkit » remet la
pop-up. « pvecli node nag status » le dit, « pvecli node nag off » le refait.

Enfin, le rappel honnête : c'est un contournement d'une vérification de licence.
Sur un homelab, sans conséquence. En production, l'abonnement Community reste la
voie propre, et il donne accès au dépôt « pve-enterprise ».`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newNodeNagStatusCmd(), newNodeNagOffCmd(), newNodeNagOnCmd())
	return c
}

func newNodeNagStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "status",
		Short:   "Dit si la pop-up est active ou neutralisée",
		Aliases: []string{"show"},
		Long: `Lit ` + nag.File + ` sur le nœud, sans le modifier.

La détection cherche le marqueur « pvecli:nag-off », jamais un nombre
d'occurrences. Le test qui circule, « grep -c "orig_cmd();" », rend 2 sur un
fichier vierge — ce sont les appels légitimes de la fonction. Il annonce donc
« déjà patché » à propos d'un nœud qui ne l'est pas.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNag(cmd, nag.Status, "")
		},
	}
	addNagFlags(c)
	addRenderFlags(c)
	return c
}

func newNodeNagOffCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "off",
		Short: "Fait disparaître la pop-up (patch + redémarrage de pveproxy)",
		Long: `Injecte le court-circuit dans ` + nag.File + `, puis redémarre « pveproxy ».

Idempotent : sur un nœud déjà patché, rien n'est réécrit et « pveproxy » n'est
pas redémarré. Le marqueur est revérifié APRÈS l'écriture et avant le
redémarrage — un patch annoncé mais non appliqué est précisément ce que ce
marqueur existe pour rendre impossible.

Après coup, recharge l'interface sans le cache : Ctrl+Shift+R.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNag(cmd, nag.Off, "neutraliser la pop-up d'abonnement")
		},
	}
	addNagFlags(c)
	addRenderFlags(c)
	return c
}

func newNodeNagOnCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "on",
		Short: "Rétablit la pop-up (retire le patch)",
		Long: `Retire du fichier exactement ce que « nag off » y avait inséré, puis redémarre
« pveproxy ».

C'est un retrait textuel, pas la restauration d'une copie : le résultat est donc
la version du fichier actuellement installée par le paquet, et non celle qui
existait au moment du patch.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNag(cmd, nag.On, "rétablir la pop-up d'abonnement")
		},
	}
	addNagFlags(c)
	addRenderFlags(c)
	return c
}

func addNagFlags(c *cobra.Command) {
	c.Flags().String("ssh-host", "", "hôte SSH du nœud (défaut : l'hôte de l'endpoint)")
	c.Flags().String("ssh-user", "root", "utilisateur SSH — l'écriture exige les droits root")
	c.Flags().Bool("dry-run", false, "affiche le script qui serait exécuté, sans l'exécuter")
	c.Flags().Bool("yes", false, "ne demande pas confirmation (usage script)")
}

// nagOp is one of nag.Status / nag.Off / nag.On.
type nagOp func(context.Context, nag.Runner) (nag.Report, error)

// runNag wires the shared plumbing: where to log in, whether to ask first, and
// how to report. An empty `effect` marks a read-only operation.
func runNag(cmd *cobra.Command, op nagOp, effect string) error {
	opts, err := renderOptions(cmd)
	if err != nil {
		return err
	}
	ssh, err := nagSSH(cmd)
	if err != nil {
		return err
	}
	if err := ssh.Look(); err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")

	if dryRun {
		// The script itself is the plan. Printing a paraphrase of it would be
		// the one thing a dry run must not do.
		return dumpNagScript(cmd, op, ssh)
	}

	if effect != "" && !yes {
		if err := confirm(cmd, fmt.Sprintf("%s sur %s (%s) ?", effect, ssh.Target(), nag.File)); err != nil {
			return err
		}
	}

	rep, err := op(cmd.Context(), ssh.Runner())
	if err != nil {
		return err
	}
	return renderNag(cmd, opts, ssh, rep)
}

// dumpNagScript runs the operation against a Runner that captures the script
// instead of executing it — so what is printed is byte for byte what would have
// been sent, not a second implementation of it.
func dumpNagScript(cmd *cobra.Command, op nagOp, ssh nag.SSH) error {
	var script string
	capture := func(_ context.Context, s string) (string, error) {
		script = s
		return "", nil
	}
	// The report is discarded: with no output to parse it says nothing. Only
	// the captured script matters here, and the error is expected.
	_, _ = op(cmd.Context(), capture)

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "# à exécuter sur %s via « ssh … sh -s »\n", ssh.Target())
	_, err := fmt.Fprint(cmd.OutOrStdout(), script)
	return err
}

func renderNag(cmd *cobra.Command, opts output.Options, ssh nag.SSH, rep nag.Report) error {
	changed := "non — rien à faire"
	if rep.Changed {
		changed = "oui — pveproxy redémarré, recharge l'interface avec Ctrl+Shift+R"
	}
	rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
		{"nœud", ssh.Target()},
		{"version PVE", rep.Version},
		{"fichier", rep.File},
		{"pop-up", string(rep.State)},
		{"modifié", changed},
	}}
	return output.Render(cmd.OutOrStdout(), opts, rep, rows)
}

// nagSSH decides which machine to log into.
//
// The endpoint already names the node's address, so requiring --ssh-host would
// make the operator repeat what the configuration knows. It stays available for
// the cases the URL cannot express: a management IP that differs from the API
// one, or a name that only ~/.ssh/config resolves.
func nagSSH(cmd *cobra.Command) (nag.SSH, error) {
	host, _ := cmd.Flags().GetString("ssh-host")
	user, _ := cmd.Flags().GetString("ssh-user")

	if host == "" {
		eff, err := resolveConfig(cmd)
		if err != nil {
			return nag.SSH{}, err
		}
		if eff.Endpoint == "" {
			return nag.SSH{}, &exitError{code: 2, msg: "aucun endpoint configuré et aucun --ssh-host : " +
				"je ne sais pas à quelle machine me connecter.\n" +
				"  pvecli config set endpoint https://<ip>:8006   ou   --ssh-host <hôte>"}
		}
		u, err := url.Parse(eff.Endpoint)
		if err != nil || u.Hostname() == "" {
			return nag.SSH{}, &exitError{code: 2, msg: fmt.Sprintf(
				"impossible de déduire un hôte SSH de l'endpoint %q — passe --ssh-host.", eff.Endpoint)}
		}
		host = u.Hostname()
	}
	return nag.SSH{Host: host, User: user}, nil
}
