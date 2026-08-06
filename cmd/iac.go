package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/pvecli/internal/config"
	"github.com/dev-toolings/pvecli/internal/iac"
	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/secret"
	"github.com/spf13/cobra"
)

func newIaCCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "iac",
		Short: "Fait le pont entre l'API PVE et Terraform / Ansible",
		Long: `Relie le nœud aux deux outils qui déclarent réellement l'infrastructure.

pvecli ne remplace ni Terraform ni Ansible. Il fait les deux choses qu'aucun des
deux ne sait faire seul :

  · il OBSERVE l'écart entre le déclaré et le réel  (iac state, iac drift)
  · il ALIMENTE Ansible en données que seule l'API connaît  (iac inventory)

Les répertoires du dépôt d'infrastructure se déclarent une fois :

  pvecli config set iac.terraform_dir /chemin/vers/infra/terraform
  pvecli config set iac.ansible_dir   /chemin/vers/infra/ansible`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newIaCInventoryCmd(), newIaCStateCmd(), newIaCDriftCmd(), newIaCAdoptCmd())
	c.AddCommand(newIaCPlanCmd(), newIaCApplyCmd(), newIaCConfigureCmd())
	c.AddCommand(newIaCScaffoldCmd())
	return c
}

func newIaCConfigureCmd() *cobra.Command {
	var (
		playbook    string
		limit       string
		tags        string
		check       bool
		idempotence bool
		verifyURL   string
		verifyText  string
		user        string
		tag         string
		cfTunnel    string
		cfHostname  string
	)

	c := &cobra.Command{
		Use:   "configure",
		Short: "Joue un playbook Ansible sur un inventaire régénéré à l'instant",
		Long: `Génère l'inventaire depuis l'API, puis exécute ansible-playbook dessus.

L'inventaire est produit dans un fichier temporaire à chaque appel et supprimé
ensuite. C'est délibéré : un inventaire persistant est un inventaire qui
vieillit, et un bail DHCP renouvelé suffit à le rendre faux sans que rien ne le
signale.

  --playbook site.yml     playbook à jouer, relatif à iac.ansible_dir
  --limit, --tags         passés tels quels à ansible-playbook
  --check                 mode check d'Ansible : simule, n'applique pas
  --idempotence           rejoue le playbook et ÉCHOUE si le second passage
                          rapporte le moindre « changed »
  --verify-url URL        après coup, appelle l'URL sur chaque hôte
                          ({{host}} est remplacé par son adresse)
  --verify-contains TXT   et exige ce texte dans la réponse

UN « 200 OK » NE PROUVE PAS QUE TON APPLICATION EST SERVIE. Ce lab l'a appris
en direct : le playbook activait bien son vhost, mais Debian livre un site
« default » déclaré « listen 80 default_server » qui remporte toute requête
sans server_name. Nginx répondait 200 — sa page d'accueil à lui. C'est la même
règle que pour les tâches PVE : une acceptation n'est pas un résultat. D'où
--verify-contains, qui regarde le corps de la réponse.

L'IDEMPOTENCE NE SE DÉCLARE PAS, ELLE SE MESURE. Un playbook qui réussit deux
fois de suite n'est pas idempotent : il l'est si le SECOND passage ne change
rien. Le code de retour ne dit pas la différence — un playbook qui réinstalle
tout à chaque fois sort 0 et ressemble à un succès. C'est « changed=0 » dans le
PLAY RECAP qui tranche, et c'est ce que --idempotence lit.

PRÉ-VOL : tous les hôtes de l'inventaire doivent répondre à « ansible -m ping ».
Jouer un playbook sur un hôte injoignable produit un rapport d'échec au milieu
d'un run à moitié fait, ce qui est le pire moment pour l'apprendre.

Endpoints : GET /api2/json/cluster/resources
            GET /api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces
            GET /api2/json/nodes/{node}/lxc/{vmid}/config`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			if err := iac.CheckDir("iac.ansible_dir", eff.IaC.AnsibleDir); err != nil {
				return err
			}
			play := iac.Tool{Name: iac.AnsiblePlaybookBin, Dir: eff.IaC.AnsibleDir}
			ping := iac.Tool{Name: iac.AnsibleBin, Dir: eff.IaC.AnsibleDir}
			if err := ensureTool(cmd, play); err != nil {
				return err
			}
			if err := ensureTool(cmd, ping); err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			inv, err := buildInventory(cmd.Context(), client, inventoryOptions{
				groupByTag: true, tag: tag, user: user,
			})
			if err != nil {
				return err
			}
			reportInventory(cmd, inv)
			if len(inv.Hosts) == 0 {
				return fmt.Errorf("inventaire vide : aucun hôte à configurer.\n" +
					"Les exclusions ci-dessus disent pourquoi — un agent QEMU absent côté VM,\n" +
					"ou, côté conteneur, une configuration qui ne désigne pas une IPv4 (DHCP, ou plusieurs)")
			}

			path, cleanup, err := writeTempInventory(inv, eff.Endpoint)
			if err != nil {
				return err
			}
			defer cleanup()
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "inventaire temporaire : %s (%d hôte(s))\n", path, len(inv.Hosts))

			if err := pingHosts(cmd, ping, path); err != nil {
				return err
			}

			// Where the roles publish what they made reachable. A directory the
			// roles write into rather than output parsed from the playbook's
			// stdout: Ansible's human output is not a data format, and one
			// `--verbose` away from changing shape.
			outDir, err := os.MkdirTemp("", "pvecli-outputs-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(outDir) }()

			playArgs := []string{"-i", path, "-e", iac.OutputDirVar + "=" + outDir}
			if cfTunnel != "" {
				extras, done, err := writeCloudflaredVars(cfTunnel, cfHostname)
				if err != nil {
					return err
				}
				defer done()
				// « -e @fichier » et non « -e clé=valeur » : un jeton passé en
				// argument est lisible par « ps » pendant toute la durée du run.
				playArgs = append(playArgs, "-e", "@"+extras)
			}
			playArgs = append(playArgs, playbook)
			if limit != "" {
				playArgs = append(playArgs, "--limit", limit)
			}
			if tags != "" {
				playArgs = append(playArgs, "--tags", tags)
			}
			if check {
				playArgs = append(playArgs, "--check")
			}

			if _, err := runPlaybook(cmd, play, playArgs, "passage 1"); err != nil {
				return err
			}

			if idempotence {
				if check {
					return &exitError{code: pve.ExitUsage,
						msg: "--idempotence et --check ne vont pas ensemble : en mode check, rien n'est appliqué,\ndonc le second passage rapporterait les mêmes changements que le premier"}
				}
				second, err := runPlaybook(cmd, play, playArgs, "passage 2 (mesure d'idempotence)")
				if err != nil {
					return err
				}
				if changed := iac.TotalChanged(second); changed > 0 {
					return fmt.Errorf("le playbook n'est PAS idempotent : le second passage a encore changé %d chose(s)\n"+
						"  hôtes concernés : %s\n"+
						"\n"+
						"Une tâche qui « change » à chaque passage décrit une action, pas un état :\n"+
						"cherche un command/shell sans « creates », un fichier écrit avec une date,\nou un service redémarré sans condition",
						changed, strings.Join(iac.ChangedHosts(second), ", "))
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nidempotence VÉRIFIÉE : second passage à changed=0 sur %d hôte(s).\n", len(second))
			}

			if verifyText != "" && verifyURL == "" {
				return &exitError{code: pve.ExitUsage, msg: "--verify-contains n'a de sens qu'avec --verify-url"}
			}
			if verifyURL != "" {
				// Verification first: a connection block printed after a failed
				// check would describe access to something that does not answer.
				if err := verifyHosts(cmd, inv, verifyURL, verifyText); err != nil {
					return err
				}
			}
			return reportConnections(cmd, inv, outDir)
		},
	}

	f := c.Flags()
	f.StringVar(&playbook, "playbook", "site.yml", "playbook à jouer, relatif à iac.ansible_dir")
	f.StringVar(&limit, "limit", "", "restreint aux hôtes correspondants (ansible --limit)")
	f.StringVar(&tags, "tags", "", "ne joue que ces tags (ansible --tags)")
	f.BoolVar(&check, "check", false, "mode check d'Ansible : simule sans appliquer")
	f.BoolVar(&idempotence, "idempotence", false, "rejoue le playbook et échoue si le second passage change quelque chose")
	f.StringVar(&verifyURL, "verify-url", "", "URL à appeler sur chaque hôte après coup ; {{host}} = son adresse")
	f.StringVar(&verifyText, "verify-contains", "", "texte exigé dans la réponse de --verify-url — un 200 ne dit pas QUI répond")
	f.StringVar(&user, "user", "", "force ansible_user dans l'inventaire généré")
	f.StringVar(&tag, "tag", "", "ne retient que les VM portant ce tag")
	f.StringVar(&cfTunnel, "cf-tunnel", "", "tunnel Cloudflare dont le jeton alimente le rôle cloudflared")
	f.StringVar(&cfHostname, "cf-hostname", "", "FQDN public routé vers ces hôtes, pour la vérification du rôle")
	// The connection block is data: it has to survive `-o json | jq` like every
	// other result this CLI produces.
	addRenderFlags(c)
	return c
}

// writeCloudflaredVars hands the connector token to Ansible through a file.
//
// The token is what authorises a machine to join the Cloudflare account. On the
// command line it would be readable by every process on the host for the whole
// length of the run; in a 0600 file in a private temp directory it is not, and
// the file is removed whatever happens next.
func writeCloudflaredVars(tunnel, hostname string) (path string, cleanup func(), err error) {
	token, err := secret.Read(cfTokenRef(tunnel))
	if err != nil {
		return "", func() {}, fmt.Errorf(`%w

Le jeton du tunnel « %s » n'est pas dans le trousseau. Crée le tunnel :
  pvecli cf tunnel create %s`, err, tunnel, tunnel)
	}

	dir, err := os.MkdirTemp("", "pvecli-cf-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	doc := map[string]string{
		"pvecli_cf_token":       token,
		"pvecli_cf_tunnel_name": tunnel,
		"pvecli_cf_hostname":    hostname,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	path = filepath.Join(dir, "cloudflared.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// writeTempInventory renders the inventory to a file ansible can read.
func writeTempInventory(inv *iac.Inventory, endpoint string) (path string, cleanup func(), err error) {
	doc, err := inv.RenderYAML(iac.Header(endpoint, time.Now()))
	if err != nil {
		return "", func() {}, err
	}
	// The .yml suffix is not cosmetic: Ansible picks its inventory plugin from
	// the file extension, and a file with none is parsed by the `ini` plugin,
	// which reads this YAML as a list of hostnames with very strange names.
	f, err := os.CreateTemp("", "pvecli-inventory-*.yml")
	if err != nil {
		return "", func() {}, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(doc); err != nil {
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

// pingHosts refuses to start a run whose hosts are not all reachable.
func pingHosts(cmd *cobra.Command, ping iac.Tool, inventory string) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\npré-vol : ansible -m ping\n")

	var buf strings.Builder
	out := io.MultiWriter(cmd.ErrOrStderr(), &buf)
	runErr := ping.Run(cmd.Context(), out, out, "-i", inventory, "all", "-m", "ping")

	// An ad-hoc run prints no PLAY RECAP — that banner belongs to
	// ansible-playbook — so this output has its own parser.
	pings := iac.ParsePing(buf.String())

	var unreachable []string
	for _, p := range pings {
		if !p.OK() {
			unreachable = append(unreachable, fmt.Sprintf("%s (%s)", p.Host, p.Result))
		}
	}
	if len(unreachable) > 0 {
		return fmt.Errorf("hôte(s) injoignable(s) : %s\n\n"+
			"Rien n'a été joué. Un playbook lancé sur un inventaire à moitié joignable\n"+
			"s'arrête au milieu, et c'est le pire moment pour l'apprendre.\n"+
			"  · la VM répond-elle en SSH avec l'utilisateur de l'inventaire ?\n"+
			"  · la clé publique est-elle bien celle injectée par cloud-init ?",
			strings.Join(unreachable, ", "))
	}
	if runErr != nil {
		return runErr
	}
	// Zero answers is not success. Without this, an inventory ansible could not
	// parse would sail through the pre-check that exists to stop it.
	if len(pings) == 0 {
		return fmt.Errorf("aucun hôte n'a répondu au ping, et aucun n'a échoué non plus :\n" +
			"ansible n'a probablement rien lu dans l'inventaire généré")
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ✓ %d hôte(s) répondent\n", len(pings))
	return nil
}

// runPlaybook streams a run and parses its recap at the same time.
//
// Both halves are needed: the operator must see Ansible's own output, unfiltered
// and in real time, and pvecli must read the PLAY RECAP to measure idempotence.
// Capturing without streaming would replace a live run with a silence.
func runPlaybook(cmd *cobra.Command, play iac.Tool, args []string, label string) ([]iac.Recap, error) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n$ ansible-playbook %s   (%s)\n\n", strings.Join(args, " "), label)

	var buf strings.Builder
	stdout := io.MultiWriter(cmd.OutOrStdout(), &buf)
	if err := play.Run(cmd.Context(), stdout, cmd.ErrOrStderr(), args...); err != nil {
		return nil, err
	}
	return iac.ParseRecap(buf.String()), nil
}

// verifyHosts is the application-level proof: the service answers, and answers
// with the right thing. Neither Terraform nor Ansible ever checks either.
//
// want is optional, and it is the half that matters. A status code says a
// server is listening; only the body says WHICH server. This lab spent a run
// believing its application was deployed because Nginx's own welcome page
// answered 200 on the same port.
func verifyHosts(cmd *cobra.Command, inv *iac.Inventory, template, want string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	var failures []string

	for _, h := range inv.Hosts {
		url := strings.ReplaceAll(template, "{{host}}", h.IP)

		resp, err := client.Get(url) //nolint:gosec,noctx // the URL comes from the operator's own flag
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s : %v", h.Name, err))
			continue
		}
		// Bounded: a verification must not be turned into a download by a
		// server that answers with a disk image.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			failures = append(failures, fmt.Sprintf("%s : HTTP %d", h.Name, resp.StatusCode))
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ %s → %s\n", url, resp.Status)
		case want != "" && !strings.Contains(string(body), want):
			failures = append(failures, fmt.Sprintf("%s : HTTP %d mais la réponse ne contient pas %q", h.Name, resp.StatusCode, want))
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ %s → %s, mais ce n'est pas la bonne page\n", url, resp.Status)
		default:
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ✓ %s → %s\n", url, resp.Status)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("le playbook a réussi mais le service ne rend pas ce qui était attendu :\n  %s\n\n"+
			"Ansible rapporte ce que ses tâches ont fait, pas ce que l'application sert",
			strings.Join(failures, "\n  "))
	}
	return nil
}

// terraformEnv builds the environment Terraform runs with.
//
// The token reaches Terraform through TF_VAR_proxmox_api_token and nowhere
// else. Not terraform.tfvars: that file is committed by accident more often
// than any other in an infrastructure repository, and a token in it is a token
// in the history forever. The provider wants the whole credential in one
// string, « <token-id>=<secret> ».
//
// An operator who has already exported the variable keeps their value: they
// may well be driving Terraform with a different, more privileged identity
// than the one pvecli reads with, and silently substituting ours would be a
// surprise in the one place surprises are expensive.
func terraformEnv(cmd *cobra.Command, eff *config.Effective) []string {
	const varName = "TF_VAR_proxmox_api_token"

	if existing := os.Getenv(varName); existing != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"%s est déjà exporté : pvecli le laisse tel quel et n'impose pas son propre token.\n", varName)
		return nil
	}
	if eff.TokenSecret == "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"aucun secret de token : terraform devra trouver ses identifiants lui-même.\n"+
				"  export %s=…\n", config.EnvTokenSecret)
		return nil
	}
	return []string{fmt.Sprintf("%s=%s=%s", varName, eff.TokenID, eff.TokenSecret)}
}

// warnIfTokenInTfvars looks for the mistake this project refuses to make in its
// own configuration file, in the one file it does not own.
func warnIfTokenInTfvars(cmd *cobra.Command, dir string) {
	for _, name := range []string{"terraform.tfvars", "terraform.tfvars.json"} {
		body, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // path comes from the operator's own config
		if err != nil {
			continue
		}
		if !strings.Contains(string(body), "proxmox_api_token") {
			continue
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"AVERTISSEMENT : %s contient « proxmox_api_token ».\n"+
				"  Un secret dans un fichier finit committé, sauvegardé, ou lu par un autre\n"+
				"  processus. pvecli passe le sien par l'environnement :\n"+
				"    export %s=…   puis   pvecli iac apply\n", name, config.EnvTokenSecret)
	}
}

// preflight is the check `terraform apply` does not do: that the node is
// reachable with the identity pvecli was given, and that nothing has drifted
// since the last apply.
//
// The order matters, and it is the order of `pvecli doctor`: a drift report
// built on a failed API call would be a list of "orphans" that are simply
// unreachable.
func preflight(cmd *cobra.Command, client *pve.Client, eff *config.Effective) error {
	err := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(err, "pré-vol\n")

	v, verr := client.Version(cmd.Context())
	if verr != nil {
		return fmt.Errorf("le nœud n'est pas joignable avec cette identité — « pvecli doctor » le détaille :\n%w", verr)
	}
	_, _ = fmt.Fprintf(err, "  ✓ %s — PVE %s, TLS %s, identité %s\n", eff.Endpoint, v.Version, client.TrustMode(), eff.TokenID)

	declared, serr := iac.ReadState(cmd.Context(), eff.IaC.TerraformDir, err)
	var empty *iac.EmptyStateError
	if errors.As(serr, &empty) {
		_, _ = fmt.Fprintf(err, "  · state vide — rien à comparer, c'est un premier apply\n")
		return nil
	}
	if serr != nil {
		return serr
	}

	live, lerr := readLive(cmd.Context(), client)
	if lerr != nil {
		return lerr
	}

	// Only « modified » matters here. « unmanaged » lists every guest the lab
	// created by hand, which is normal in this lab and would drown the warning
	// that is not.
	report := iac.Compare(declared, live).Only(iac.KindModified)
	if !report.HasDrift() {
		_, _ = fmt.Fprintf(err, "  ✓ aucune dérive : le state décrit ce que le nœud contient\n")
		return nil
	}

	_, _ = fmt.Fprintf(err, "  ⚠ %d ressource(s) ont dérivé depuis le dernier apply :\n", len(report.Findings))
	for _, f := range report.Findings {
		for _, d := range f.Differences {
			_, _ = fmt.Fprintf(err, "      %d %-24s déclaré %s · réel %s\n", f.VMID, d.Field, d.Declared, d.Live)
		}
	}
	_, _ = fmt.Fprintf(err, "    terraform va les ramener au déclaré. Si ce n'est pas ce que tu veux,\n"+
		"    arrête ici et corrige main.tf d'abord.\n")
	return nil
}

// postflight re-reads through the API what Terraform says it did.
//
// This is the step that makes the wrapper worth having. Terraform reports
// success when its provider's write returned without error; pvecli asks the
// node, independently, what is actually there — the same "HTTP 200 is an
// acceptance, not a success" rule the rest of this CLI lives by.
func postflight(cmd *cobra.Command, client *pve.Client, eff *config.Effective) error {
	out, errW := cmd.OutOrStdout(), cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(errW, "\npost-vol — relecture par l'API, pas par terraform\n")

	declared, err := iac.ReadState(cmd.Context(), eff.IaC.TerraformDir, errW)
	if err != nil {
		return err
	}
	live, err := readLive(cmd.Context(), client)
	if err != nil {
		return err
	}

	byVMID := map[int]iac.Live{}
	for _, l := range live {
		byVMID[l.VMID] = l
	}
	rows := output.Rows{Headers: []string{"VMID", "NOM", "STATUT RÉEL", "CŒURS", "RAM", "TAGS"}}
	for _, d := range declared {
		l, ok := byVMID[d.VMID]
		if !ok {
			rows.Cells = append(rows.Cells, []string{strconv.Itoa(d.VMID), d.Name, "ABSENT DU NŒUD", "—", "—", "—"})
			continue
		}
		rows.Cells = append(rows.Cells, []string{
			strconv.Itoa(l.VMID), l.Name, "présent",
			strconv.Itoa(l.Cores), fmt.Sprintf("%d Mio", l.Memory), strings.Join(l.Tags, ","),
		})
	}
	if err := output.Render(out, output.Options{Format: output.Table}, declared, rows); err != nil {
		return err
	}

	if report := iac.Compare(declared, live).Only(iac.KindModified); report.HasDrift() {
		return fmt.Errorf("apply terminé, mais %d ressource(s) divergent encore du déclaré — pvecli iac drift", len(report.Findings))
	}
	_, _ = fmt.Fprintln(errW, "aucune dérive après apply : le déclaré et le réel concordent.")
	return nil
}

const wrapperNote = `
pvecli NE REMPLACE PAS TERRAFORM et ne le masque pas. Il l'exécute dans
« iac.terraform_dir », relaie sa sortie et son code de retour tels quels, et
ajoute les deux vérifications que Terraform ne fait pas lui-même : que le nœud
est joignable avec l'identité attendue avant, et ce que l'API contient vraiment
après.

CODE DE SORTIE : celui de terraform, pas la table du PRD §7.5. Un script qui
teste le statut de « terraform plan » continue de fonctionner derrière pvecli.

Le secret du token part par l'environnement (TF_VAR_proxmox_api_token), jamais
dans terraform.tfvars.`

func newIaCPlanCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                "plan [-- args terraform…]",
		Short:              "Exécute « terraform plan », encadré par des vérifications API",
		Long:               "Exécute « terraform plan » dans le dossier d'infrastructure." + wrapperNote,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTerraform(cmd, "plan", args, false)
		},
	}
	return c
}

func newIaCApplyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "apply [-- args terraform…]",
		Short: "Exécute « terraform apply », encadré par des vérifications API",
		Long: `Exécute « terraform apply » dans le dossier d'infrastructure.

  --dry-run   exécute « terraform plan » à la place : montre ce qui serait
              appliqué, sans rien appliquer.
  --yes       ne demande pas confirmation, et passe -auto-approve à terraform.

Sans --yes, DEUX confirmations sont demandées : celle de pvecli, puis celle de
terraform. Ce n'est pas une redondance — la première porte sur ce que le
pré-vol vient d'afficher (dérive comprise), la seconde sur le plan lui-même.` + wrapperNote,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "--dry-run : « terraform plan » au lieu de « apply ».")
				return runTerraform(cmd, "plan", args, false)
			}
			return runTerraform(cmd, "apply", args, true)
		},
	}
	addWriteFlags(c)
	return c
}

// runTerraform is the whole wrapper: pre-flight, gate, run, post-flight.
func runTerraform(cmd *cobra.Command, verb string, extra []string, confirmFirst bool) error {
	eff, err := resolveConfig(cmd)
	if err != nil {
		return err
	}
	if err := iac.CheckDir("iac.terraform_dir", eff.IaC.TerraformDir); err != nil {
		return err
	}
	tf := iac.Tool{Name: iac.TerraformBin, Dir: eff.IaC.TerraformDir}
	if err := ensureTool(cmd, tf); err != nil {
		return err
	}
	client, err := newClient(cmd)
	if err != nil {
		return err
	}

	warnIfTokenInTfvars(cmd, eff.IaC.TerraformDir)
	if err := preflight(cmd, client, eff); err != nil {
		return err
	}

	yes, _ := cmd.Flags().GetBool("yes")
	if confirmFirst && !yes {
		if err := confirm(cmd, fmt.Sprintf("Lancer « terraform apply » dans %s ?", eff.IaC.TerraformDir)); err != nil {
			return err
		}
	}

	args := append([]string{verb}, extra...)
	if verb == "apply" && yes {
		args = append([]string{verb, "-auto-approve"}, extra...)
	}
	tf.Env = terraformEnv(cmd, eff)

	// The exact command line, so the operator can rerun it by hand. A wrapper
	// that hides what it launched is a wrapper you cannot debug.
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n$ terraform %s   (dans %s)\n\n", strings.Join(args, " "), tf.Dir)
	if err := tf.Run(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args...); err != nil {
		// Relayed untouched: terraform has already explained itself, and a
		// second wording would only compete with the first.
		return err
	}

	if verb != "apply" {
		return nil
	}
	return postflight(cmd, client, eff)
}

func newIaCAdoptCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "adopt <vmid>",
		Short: "Génère les blocs Terraform qui adoptent un guest existant",
		Long: `Produit un bloc « import » et une ébauche de bloc « resource » pour faire
passer sous Terraform une ressource créée à la main — sans la recréer.

N'ÉCRIT JAMAIS DANS UN FICHIER .tf. La sortie va sur stdout ; c'est à toi de la
relire et de la coller. Ce n'est pas une précaution excessive : un bloc d'import
faux ne provoque pas une erreur, il provoque un « plan » qui propose de détruire
et recréer la ressource qu'on voulait justement préserver.

La séquence, et l'étape qui compte :

  1. pvecli iac adopt 211 >> /tmp/adopt.tf
  2. coller les deux blocs dans main.tf
  3. terraform plan    → tant que « plan » propose un changement, c'est le CODE
                         qui est faux, pas le nœud. On ajuste jusqu'à
                         « No changes ».
  4. terraform apply   → la ressource entre dans le state
  5. la taguer « managed » pour que la garde de propriété la protège

Fonctionne pour les VM QEMU et les conteneurs LXC ; le type de ressource et les
noms d'attributs diffèrent entre les deux, et la sortie s'y adapte.

Endpoints : GET /api2/json/cluster/resources
            GET /api2/json/nodes/{node}/{type}/{vmid}/config`,
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

			resources, err := client.Resources(cmd.Context(), "vm")
			if err != nil {
				return err
			}
			var target *pve.Resource
			for i := range resources {
				if resources[i].VMID == vmid {
					target = &resources[i]
					break
				}
			}
			if target == nil {
				return fmt.Errorf("aucun guest %d sur le cluster — pvecli guest ls", vmid)
			}

			kind := pve.TypeQEMU
			if target.Type == string(pve.TypeLXC) {
				kind = pve.TypeLXC
			}
			cfg, err := client.GuestConfig(cmd.Context(), target.Node, kind, vmid)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), iac.Adopt(iac.LiveFromPVE(*target, cfg), kind == pve.TypeLXC))
			return err
		},
	}
	return c
}

func newIaCDriftCmd() *cobra.Command {
	var only string

	c := &cobra.Command{
		Use:   "drift",
		Short: "Compare ce que Terraform déclare et ce que le nœud contient",
		Long: `Confronte le state Terraform à l'état réel lu par l'API, attribut par attribut.

TROIS CATÉGORIES, trois problèmes différents :

  modifié    déclaré ET présent, mais les valeurs divergent
             → quelqu'un a écrit en dehors de Terraform
  orphelin   dans le state, absent du nœud
             → détruit à la main ; terraform va tenter de modifier un fantôme
  non géré   sur le nœud, absent du state
             → personne ne le possède ; « iac adopt » propose de l'adopter

CODE DE SORTIE : 0 si tout concorde, 1 sinon. C'est ce qui rend la commande
utilisable en tâche planifiée — une dérive n'est un problème que si on
l'apprend.

CE QUI N'EST PAS COMPARÉ, et pourquoi. La liste est ici plutôt que dans le code
parce qu'une exclusion invisible est un angle mort :
` + ignoredHelp() + `
Endpoints : terraform show -json  ·  GET /api2/json/cluster/resources
            GET /api2/json/nodes/{node}/{type}/{vmid}/config`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch only {
			case "", iac.KindModified, iac.KindOrphan, iac.KindUnmanaged:
			default:
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf(
					"--only attend « %s », « %s » ou « %s », reçu %q",
					iac.KindModified, iac.KindOrphan, iac.KindUnmanaged, only)}
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			if err := iac.CheckDir("iac.terraform_dir", eff.IaC.TerraformDir); err != nil {
				return err
			}
			if err := ensureTool(cmd, iac.Tool{Name: iac.TerraformBin, Dir: eff.IaC.TerraformDir}); err != nil {
				return err
			}
			declared, err := iac.ReadState(cmd.Context(), eff.IaC.TerraformDir, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			live, err := readLive(cmd.Context(), client)
			if err != nil {
				return err
			}

			report := iac.Compare(declared, live)
			if only != "" {
				report = report.Only(only)
			}

			if err := output.Render(cmd.OutOrStdout(), opts, report.Findings, driftRows(report)); err != nil {
				return err
			}
			if report.HasDrift() {
				// Exit 1 with no extra message: the table above already said
				// what diverged, and a second sentence would only be noise in a
				// cron log.
				return &exitError{code: pve.ExitGeneric, msg: fmt.Sprintf("%d dérive(s) détectée(s)", len(report.Findings))}
			}
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "aucune dérive : le déclaré et le réel concordent.")
			return nil
		},
	}

	c.Flags().StringVar(&only, "only", "", "ne garde qu'une catégorie : modifié, orphelin ou non géré")
	addRenderFlags(c)
	return c
}

func ignoredHelp() string {
	var b strings.Builder
	for _, i := range iac.Ignored {
		fmt.Fprintf(&b, "  · %-32s %s\n", i.Field, i.Why)
	}
	return b.String()
}

// readLive reads every guest's real configuration.
//
// The cluster index alone is not enough: it reports a guest's memory as bytes
// currently allocated, not the `memory` key Terraform declares. Only the
// configuration endpoint holds what was asked for, as opposed to what is
// happening.
func readLive(ctx context.Context, client *pve.Client) ([]iac.Live, error) {
	resources, err := client.Resources(ctx, "vm")
	if err != nil {
		return nil, err
	}

	live := make([]iac.Live, 0, len(resources))
	for _, r := range resources {
		// A template is not a resource Terraform declares; listing it as
		// « non géré » at every run would train the operator to ignore the
		// category that matters.
		if r.Template == 1 {
			continue
		}
		kind := pve.TypeQEMU
		if r.Type == string(pve.TypeLXC) {
			kind = pve.TypeLXC
		}
		cfg, err := client.GuestConfig(ctx, r.Node, kind, r.VMID)
		if err != nil {
			return nil, fmt.Errorf("configuration du guest %d illisible : %w", r.VMID, err)
		}
		live = append(live, iac.LiveFromPVE(r, cfg))
	}
	return live, nil
}

func driftRows(report iac.Report) output.Rows {
	rows := output.Rows{Headers: []string{"VMID", "NOM", "CATÉGORIE", "ATTRIBUT", "DÉCLARÉ", "RÉEL"}}
	for _, f := range report.Findings {
		if len(f.Differences) == 0 {
			rows.Cells = append(rows.Cells, []string{
				strconv.Itoa(f.VMID), firstNonEmpty(f.Name, "—"), f.Kind, "—", "—", "—",
			})
			continue
		}
		for i, d := range f.Differences {
			vmid, name, kind := "", "", ""
			if i == 0 {
				vmid, name, kind = strconv.Itoa(f.VMID), firstNonEmpty(f.Name, "—"), f.Kind
			}
			rows.Cells = append(rows.Cells, []string{vmid, name, kind, d.Field, d.Declared, d.Live})
		}
	}
	return rows
}

func newIaCStateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "state",
		Short: "Affiche les ressources Proxmox déclarées dans le state Terraform",
		Long: `Extrait du state Terraform les ressources Proxmox qu'il déclare.

Le state est une BASE DE DONNÉES, pas un fichier de configuration. On l'interroge
par l'outil qui la possède :

    terraform show -json

et jamais en lisant terraform.tfstate à la main. C'est la décision D2 du PRD :
le format interne du state appartient à Terraform, il a déjà changé entre deux
versions mineures, et un outil qui le parse casse à une mise à jour à laquelle
il n'a pas participé.

LECTURE SEULE, sans exception. « terraform show » ne peut rien écrire ; c'est
« terraform refresh » qui écrit, et il n'est jamais appelé ici. Un test épingle
l'argv exact pour que ça reste vrai.

Ressources lues : ` + iac.TypeVM + `
                  ` + iac.TypeContainer + `

Le dossier interrogé est « iac.terraform_dir » :
  pvecli config set iac.terraform_dir /chemin/vers/infra/terraform`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}

			if err := iac.CheckDir("iac.terraform_dir", eff.IaC.TerraformDir); err != nil {
				return err
			}
			if err := ensureTool(cmd, iac.Tool{Name: iac.TerraformBin, Dir: eff.IaC.TerraformDir}); err != nil {
				return err
			}
			declared, err := iac.ReadState(cmd.Context(), eff.IaC.TerraformDir, cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			return output.Render(cmd.OutOrStdout(), opts, declared, declaredRows(declared))
		},
	}
	addRenderFlags(c)
	return c
}

func declaredRows(declared []iac.Declared) output.Rows {
	rows := output.Rows{Headers: []string{"VMID", "NOM", "NŒUD", "CŒURS", "RAM", "ON_BOOT", "TAGS", "ADRESSE TERRAFORM"}}
	for _, d := range declared {
		rows.Cells = append(rows.Cells, []string{
			strconv.Itoa(d.VMID),
			firstNonEmpty(d.Name, "—"),
			firstNonEmpty(d.Node, "—"),
			strconv.Itoa(d.Cores),
			fmt.Sprintf("%d Mio", d.Memory),
			onBootLabel(d.OnBoot),
			firstNonEmpty(strings.Join(d.Tags, ","), "—"),
			d.Address,
		})
	}
	return rows
}

// onBootLabel keeps "not declared" distinct from "declared false". Terraform
// leaving an attribute unset and Terraform setting it to false are different
// statements, and drift detection depends on the difference.
func onBootLabel(b *bool) string {
	if b == nil {
		return "—"
	}
	return yesNo(*b)
}

func newIaCInventoryCmd() *cobra.Command {
	var (
		groupBy string
		tag     string
		user    string
		outFile string
		format  string
	)

	c := &cobra.Command{
		Use:   "inventory",
		Short: "Génère un inventaire Ansible depuis l'API (GET /cluster/resources + agent QEMU)",
		Long: `Produit un inventaire Ansible dont les adresses viennent du nœud, pas d'un
fichier édité à la main.

C'est le point de jonction fragile entre provisionnement et configuration.
Terraform crée une VM mais ne sait pas quelle adresse le DHCP lui a donnée ;
PVE ne le sait pas non plus — seul l'agent invité le sait. Un inventaire tenu à
la main est donc juste le jour où on l'écrit, et faux au prochain bail.

CE QUI EST EXCLU, ET POURQUOI. Un guest sans adresse découvrable n'entre pas
dans l'inventaire, et la raison part sur stderr :

  · une VM arrêtée         : rien à interroger
  · un agent qui ne répond pas : l'adresse serait une supposition
  · un conteneur arrêté    : Ansible ne pourrait pas s'y connecter
  · un conteneur en DHCP, ou qui déclare plusieurs IPv4 statiques :
                             sa configuration ne désigne pas UNE adresse
  · un template            : ce n'est pas un hôte

UN CONTENEUR N'EST PAS EXCLU PAR NATURE. Il n'a pas d'agent QEMU, mais son
adresse est écrite dans sa configuration (net0, « ip=192.168.1.222/24 ») : c'est
là qu'elle est lue, et le masque est retiré. Ce qui manque à un conteneur, ce
n'est pas l'adresse — c'est le bail DHCP, que rien ne permet de deviner.

Aucune de ces exclusions n'est silencieuse. Un inventaire plus court que la
flotte donne un « ok=0 changed=0 » qui ressemble à un succès.

  --group-by tag   une VM taguée « lab_apps » va dans le groupe « lab_apps »
  --group-by none  tous les hôtes dans un seul groupe
  --tag <t>        ne retient que les VM portant ce tag
  --user <u>       force ansible_user (défaut : « ciuser » de la cloud-init
                   pour une VM, « root » pour un conteneur — PVE n'y enregistre
                   pas de compte, et les plays sont « become: true »)
  --format         yaml (défaut) ou json
  -o <fichier>     écrit le fichier au lieu de stdout

Note : « -o » désigne ici un FICHIER, pas un format comme ailleurs dans la CLI.
Cette commande ne rend pas un tableau, elle produit un document destiné à un
autre outil.

Endpoints : GET /api2/json/cluster/resources
            GET /api2/json/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces
            GET /api2/json/nodes/{node}/qemu/{vmid}/config
            GET /api2/json/nodes/{node}/lxc/{vmid}/config`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if groupBy != "tag" && groupBy != "none" {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("--group-by attend « tag » ou « none », reçu %q", groupBy)}
			}
			if format != "yaml" && format != "json" {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("--format attend « yaml » ou « json », reçu %q", format)}
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			eff, err := resolveConfig(cmd)
			if err != nil {
				return err
			}

			inv, err := buildInventory(cmd.Context(), client, inventoryOptions{
				groupByTag: groupBy == "tag",
				tag:        tag,
				user:       user,
			})
			if err != nil {
				return err
			}

			reportInventory(cmd, inv)

			var doc []byte
			if format == "json" {
				doc, err = inv.RenderJSON()
			} else {
				doc, err = inv.RenderYAML(iac.Header(eff.Endpoint, time.Now()))
			}
			if err != nil {
				return err
			}

			if outFile == "" {
				_, err = cmd.OutOrStdout().Write(doc)
				return err
			}
			// 0644, not 0600: an inventory holds addresses and a login name, no
			// secret. Making it unreadable would only push the next operator to
			// run the playbook as root.
			if err := os.WriteFile(outFile, doc, 0o644); err != nil { //nolint:gosec // see above
				return fmt.Errorf("écriture de %s : %w", outFile, err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "inventaire écrit dans %s (%d hôte(s))\n", outFile, len(inv.Hosts))
			return nil
		},
	}

	f := c.Flags()
	f.StringVar(&groupBy, "group-by", "tag", "groupement des hôtes : tag ou none")
	f.StringVar(&tag, "tag", "", "ne retient que les VM portant ce tag")
	f.StringVar(&user, "user", "", "force ansible_user pour tous les hôtes")
	f.StringVarP(&outFile, "out", "o", "", "fichier de sortie (défaut : stdout)")
	f.StringVar(&format, "format", "yaml", "format du document : yaml ou json")
	return c
}

type inventoryOptions struct {
	groupByTag bool
	tag        string
	user       string
}

// buildInventory reads the cluster once, then asks each running VM's agent
// where it actually is.
//
// The order matters: /cluster/resources is one call for the whole fleet, and
// the per-VM calls only happen for guests that survived the filters. Querying
// the agent of a stopped VM would cost a five-second timeout to learn something
// the index already said.
func buildInventory(ctx context.Context, client *pve.Client, o inventoryOptions) (*iac.Inventory, error) {
	resources, err := client.Resources(ctx, "vm")
	if err != nil {
		return nil, err
	}

	inv := &iac.Inventory{}
	for _, r := range resources {
		if o.tag != "" && !pve.HasTag(r.Tags, o.tag) {
			continue
		}
		// A template is not a host whatever its type — an LXC template exists
		// too, so this cannot live inside the QEMU branch.
		if r.Template == 1 {
			inv.Skip(r.VMID, r.Name, "template — ce n'est pas un hôte")
			continue
		}

		var ip, user string
		switch r.Type {
		case string(pve.TypeQEMU):
			if r.Status != "running" {
				inv.Skip(r.VMID, r.Name, "arrêtée — l'agent ne peut pas répondre")
				continue
			}
			ifaces, err := client.AgentInterfaces(ctx, r.Node, r.VMID)
			if err != nil {
				// The agent not answering is a fact about this guest, not a
				// failure of the command: one silent VM must not deprive the
				// inventory of the twenty that did answer.
				inv.Skip(r.VMID, r.Name, "agent QEMU muet — « pvecli vm agent ifaces "+fmt.Sprint(r.VMID)+" » dit pourquoi")
				continue
			}
			if ip = firstAgentIPv4(ifaces); ip == "" {
				inv.Skip(r.VMID, r.Name, "agent joignable mais aucune IPv4 non-loopback")
				continue
			}
			user = o.user
			if user == "" {
				// cloud-init's ciuser is the account the image was built to be
				// reached as. Reading it beats defaulting to whoever runs pvecli.
				if cfg, err := client.GuestConfig(ctx, r.Node, pve.TypeQEMU, r.VMID); err == nil {
					user = cfg.String("ciuser")
				}
			}

		case string(pve.TypeLXC):
			// A container has no QEMU agent, but it does not need one: its
			// address is declared in its own configuration, in net0.
			if r.Status != "running" {
				inv.Skip(r.VMID, r.Name, "conteneur arrêté — Ansible ne pourrait pas s'y connecter")
				continue
			}
			cfg, err := client.GuestConfig(ctx, r.Node, pve.TypeLXC, r.VMID)
			if err != nil {
				// Same reading as a mute agent: a fact about this container,
				// not a failure of the command.
				inv.Skip(r.VMID, r.Name, "configuration illisible — « pvecli lxc show "+fmt.Sprint(r.VMID)+" » dit pourquoi")
				continue
			}
			var reason string
			if ip, reason = lxcStaticIPv4(cfg); ip == "" {
				inv.Skip(r.VMID, r.Name, reason)
				continue
			}
			user = o.user
			if user == "" {
				// No ciuser equivalent for a container: PVE does not record a
				// login account for it. « root » is not a guess — it is the only
				// account PVE guarantees inside a container, and the catalogue's
				// plays are « become: true ». Leaving ansible_user empty would
				// silently mean « whoever runs pvecli », which is worse.
				user = "root"
			}

		default:
			inv.Skip(r.VMID, r.Name, "type d'invité inconnu : "+r.Type)
			continue
		}

		inv.Add(iac.Host{
			Name:   firstNonEmpty(r.Name, fmt.Sprintf("vm-%d", r.VMID)),
			VMID:   r.VMID,
			Node:   r.Node,
			IP:     ip,
			User:   user,
			Groups: inventoryGroups(r.Tags, o.groupByTag),
		})
	}
	return inv, nil
}

// inventoryGroups maps a guest's PVE tags to Ansible group names.
func inventoryGroups(tags string, byTag bool) []string {
	if !byTag {
		return nil
	}
	var groups []string
	for _, t := range strings.Split(tags, ";") {
		if name := iac.GroupName(t); name != "" {
			groups = append(groups, name)
		}
	}
	sort.Strings(groups)
	return groups
}

func firstAgentIPv4(ifaces []pve.AgentInterface) string {
	for _, i := range ifaces {
		if ip := i.FirstIPv4(); ip != "" {
			return ip
		}
	}
	return ""
}

// lxcStaticIPv4 renvoie l'adresse à laquelle joindre un conteneur, lue dans sa
// configuration. Le second retour est le motif d'exclusion quand il n'y en a pas.
//
// A container has no agent to ask, so what is declared is all there is. Which
// means the two cases where the configuration does not decide — a DHCP lease,
// and two static addresses — are excluded rather than guessed: an inventory
// that points Ansible at an arbitrary interface is worse than a short one,
// because nothing says it happened.
//
// An IPv4 that parses is not yet an address that reaches the container:
// loopback, the unspecified address and link-local designate the machine
// running pvecli, or nothing at all, so they are refused too — `ssh 0.0.0.0`
// would run the roles on the operator's own workstation.
func lxcStaticIPv4(cfg pve.GuestConfig) (ip, reason string) {
	keys := cfg.KeysWithPrefix("net")
	if len(keys) == 0 {
		return "", "aucune interface réseau déclarée dans sa configuration"
	}

	var (
		candidates []string // "net0=192.168.1.222", for the ambiguous case
		addrs      []string
		seen       []string // "net0 ip=dhcp", for the empty case
		unusable   []string // "net0 ip=0.0.0.0/0", an IPv4 that designates nothing
		malformed  []string // "net0 ip=pas-une-adresse", not an address at all
		v6only     bool
	)
	for _, key := range keys {
		raw := pve.ParseOptionString(cfg.String(key)).Get("ip")
		if raw == "" {
			continue
		}
		seen = append(seen, key+" ip="+raw)
		// « dhcp » and « manual » are not addresses: pvecli does not guess a
		// lease, and « manual » says the container configures itself.
		if raw == "dhcp" || raw == "manual" {
			continue
		}
		addr, ok := parseConfiguredAddr(raw)
		if !ok {
			malformed = append(malformed, key+" ip="+raw)
			continue
		}
		if !addr.Is4() {
			v6only = true
			continue
		}
		// Une IPv4 qui parse ne désigne pas forcément ce conteneur : 127.0.0.1
		// et 0.0.0.0 pointent la machine qui lance pvecli, 169.254.x.y ne
		// survit pas au premier saut. Les joindre ferait jouer les rôles
		// ailleurs que sur la cible — le chemin QEMU les écarte déjà.
		if addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() {
			unusable = append(unusable, key+" ip="+raw)
			continue
		}
		candidates = append(candidates, key+"="+addr.String())
		addrs = append(addrs, addr.String())
	}

	switch len(addrs) {
	case 1:
		return addrs[0], ""
	case 0:
		// Ordre de précédence : ne jamais annoncer « seulement une IPv6 » ni
		// « bail DHCP » quand une IPv4 était bel et bien déclarée mais refusée
		// — le motif doit nommer ce qui a été lu, pas envoyer chercher ailleurs.
		if len(unusable) > 0 {
			return "", "une adresse IPv4 inutilisable pour joindre l'hôte (" + strings.Join(unusable, ", ") +
				") — loopback, adresse non spécifiée ou lien-local ne désignent pas ce conteneur"
		}
		if len(malformed) > 0 {
			return "", "une valeur illisible là où une adresse est attendue (" + strings.Join(malformed, ", ") +
				") — ce n'est ni une adresse IP, ni « dhcp », ni « manual »"
		}
		if v6only {
			return "", "seulement une IPv6 statique dans sa configuration (" + strings.Join(seen, ", ") +
				") — l'inventaire ne pose que de l'IPv4"
		}
		if len(seen) == 0 {
			return "", "aucune adresse déclarée sur ses interfaces (" + strings.Join(keys, ", ") + ")"
		}
		return "", "aucune IPv4 statique dans sa configuration (" + strings.Join(seen, ", ") +
			") — pvecli ne devine pas une adresse obtenue au bail"
	default:
		return "", "plusieurs IPv4 statiques (" + strings.Join(candidates, ", ") +
			") — laquelle joindre n'est pas décidable depuis la configuration"
	}
}

// parseConfiguredAddr reads « 192.168.1.222/24 » or a bare « 192.168.1.222 ».
// The mask has no business in ansible_host, and a string that does not parse is
// dropped as a candidate rather than propagated as an address.
func parseConfiguredAddr(raw string) (netip.Addr, bool) {
	if p, err := netip.ParsePrefix(raw); err == nil {
		return p.Addr(), true
	}
	if a, err := netip.ParseAddr(raw); err == nil {
		return a, true
	}
	return netip.Addr{}, false
}

// reportInventory puts everything that is not the document itself on stderr, so
// that `pvecli iac inventory > inv.yml` writes a clean file.
func reportInventory(cmd *cobra.Command, inv *iac.Inventory) {
	err := cmd.ErrOrStderr()

	for _, r := range inv.Resolve() {
		_, _ = fmt.Fprintf(err, "nom en double, renommé : %s\n", r)
	}
	for _, e := range inv.Excluded {
		_, _ = fmt.Fprintf(err, "exclu %d (%s) : %s\n", e.VMID, e.Name, e.Reason)
	}
	if len(inv.Hosts) == 0 {
		_, _ = fmt.Fprintf(err, "aucun hôte dans l'inventaire — le document produit est vide, pas cassé.\n")
	}
}
