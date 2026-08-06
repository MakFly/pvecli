package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/service"
)

// EnvStoragePassword is where the password of a CIFS share or a PBS datastore
// comes from. Same rule as EnvNewUserPassword and the API token secret: the
// environment, never a flag — a flag is visible in `ps` to every user of the
// machine, and stays in the shell history.
const EnvStoragePassword = "PVE_STORAGE_PASSWORD"

// newStorageDefCmd pilote les DÉFINITIONS de stockage.
//
// Le sous-nom « def » n'est pas de la décoration. « pvecli storage rm <storage>
// <volid> » existe déjà et supprime un VOLUME ; poser à côté un « storage rm
// <storage> » à un seul argument fabriquerait le pire piège possible — oublier
// le volid supprimerait la DÉFINITION du stockage entier au lieu d'une ISO.
// Les définitions vivent donc dans un sous-nom, exactement comme « backup job »
// par rapport à « backup run » : le parent agit sur le contenu, le sous-nom
// décrit l'objet.
func newStorageDefCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "def",
		Aliases: []string{"definition"},
		Short:   "DÉFINITIONS de stockage : déclarer où le cluster peut écrire",
		Long: `Gère les définitions de stockage du cluster (/storage).

« pvecli storage ls / content / rm » agit sur le CONTENU d'un stockage — les
volumes, les ISO, les archives. Cette famille agit sur sa DÉCLARATION : ce qui
est écrit dans /etc/pve/storage.cfg et répliqué à tout le cluster.

LE SOUS-NOM « def » EXISTE POUR UNE RAISON. « pvecli storage rm <storage>
<volid> » supprime un volume. Si la suppression d'une définition s'appelait
« storage rm <storage> », oublier le volid ne rendrait pas une erreur : ça
supprimerait le stockage entier au lieu d'une ISO. Le parent agit sur le
contenu, le sous-nom décrit l'objet — comme « backup job » face à « backup run ».

CE QUE ÇA DÉBLOQUE : tant qu'aucun stockage ne déclare « backup » ailleurs que
sur le disque du nœud, la seule destination possible est « local » — et une
sauvegarde qui vit sur le disque de ce qu'elle protège meurt avec lui.

  pvecli storage def add nas-backup --type nfs \
      --server 192.168.1.50 --export /export/pve --content backup
  pvecli backup job create --vmid 220 --storage nas-backup \
      --schedule '02:30' --keep-last 3

CE QUE CETTE FAMILLE NE SUPPRIME JAMAIS, ce sont les données : « rm » retire
l'entrée de configuration, les fichiers du partage restent où ils sont.

Privilèges : « Datastore.Audit » (ou « Datastore.AllocateSpace ») sur
« /storage/<id> » pour lire, « Datastore.Allocate » sur « /storage » pour
écrire. Ce n'est PAS « Sys.Modify » — le rôle intégré « PVEDatastoreAdmin »
porte déjà « Datastore.Allocate ». Contrairement à « backup job », aucun rôle
sur mesure n'est nécessaire ici.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(
		newStorageDefListCmd(), newStorageDefShowCmd(),
		newStorageDefAddCmd(), newStorageDefSetCmd(), newStorageDefRmCmd(),
	)
	return c
}

func storageDefState(d pve.StorageDef) string {
	if d.IsEnabled() {
		return "actif"
	}
	return "désactivé"
}

// ---------------------------------------------------------------- ls

func newStorageDefListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les définitions de stockage (GET /storage)",
		Long: `Liste les stockages DÉCLARÉS dans le cluster.

La colonne CIBLE est celle qui compte : un nom comme « pbs-infra » ne dit pas
si le stockage pointe sur une baie distante ou sur la machine elle-même.

Cette liste est FILTRÉE par les droits de l'appelant. Une liste vide veut dire
« aucune définition VISIBLE », pas « aucune définition ».

Endpoint : GET /api2/json/storage (Datastore.Audit sur /storage/<id>)`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			defs, err := client.StorageDefs(cmd.Context())
			if err != nil {
				return err
			}

			offNode := false
			rows := output.Rows{Headers: []string{"NOM", "TYPE", "CONTENT", "CIBLE", "NŒUDS", "ÉTAT"}}
			for _, d := range defs {
				if d.IsOffNodeBackupTarget() {
					offNode = true
				}
				rows.Cells = append(rows.Cells, []string{
					d.Storage, d.Type, orDash(d.Content), orDash(d.Target()),
					orDash(d.Nodes), storageDefState(d),
				})
			}

			// Le trou que cette famille existe pour combler. Un « dir » peut être
			// un point de montage distant, mais rien dans l'API ne le dit : le
			// compter comme distant ferait passer « local » pour une destination
			// valable, ce qui est très exactement le faux positif à éviter.
			if !offNode {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"⚠ aucun stockage ne déclare « backup » AILLEURS que sur le disque du nœud.\n"+
						"  Une sauvegarde qui vit sur le disque de ce qu'elle protège meurt avec lui.\n"+
						"  pvecli storage def add nas-backup --type nfs --server <ip> --export <chemin> --content backup\n"+
						"  (un type « dir » peut être un montage distant — l'API ne permet pas de le savoir)")
			}
			return output.Render(cmd.OutOrStdout(), opts, defs, rows)
		},
	}
	addRenderFlags(c)
	return c
}

// ---------------------------------------------------------------- show

func newStorageDefShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show <storage>",
		Short: "Affiche une définition (GET /storage/{storage})",
		Long: `Affiche la déclaration complète d'un stockage.

Un identifiant INCONNU répond HTTP 500 ici, pas 404 (« storage 'x' does not
exist »). Le message parlera d'erreur interne du nœud alors que le nom est
simplement absent de /etc/pve/storage.cfg.

Endpoint : GET /api2/json/storage/{storage} (Datastore.Allocate sur /storage/{storage})`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			def, err := client.StorageDefByID(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}}
			for _, kv := range [][2]string{
				{"stockage", def.Storage},
				{"type", def.Type},
				{"état", storageDefState(*def)},
				{"content", def.Content},
				{"cible", def.Target()},
				{"server", def.Server},
				{"export", def.Export},
				{"share", def.Share},
				{"datastore", def.Datastore},
				{"path", def.Path},
				{"username", def.Username},
				{"domain", def.Domain},
				{"fingerprint", def.Fingerprint},
				{"namespace", def.Namespace},
				{"nodes", def.Nodes},
				{"options", def.Options},
				{"prune-backups", def.PruneBackups},
				{"digest", def.Digest},
			} {
				if kv[1] != "" {
					rows.Cells = append(rows.Cells, []string{kv[0], kv[1]})
				}
			}
			return output.Render(cmd.OutOrStdout(), opts, def, rows)
		},
	}
	addRenderFlags(c)
	return c
}

// ---------------------------------------------------------------- drapeaux

// storageDefFlags rassemble ce que add et set partagent. Les deux commandes
// lisent les MÊMES drapeaux, mais pas de la même façon : add envoie tout,
// set n'envoie que ce que l'opérateur a explicitement changé.
type storageDefFlags struct {
	opts pve.StorageDefOptions
	// guest monte un partage CIFS en invité, donc sans mot de passe. Sur le
	// modèle de --no-password de « access user create » : se passer d'un secret
	// doit être un choix énoncé, pas un oubli.
	guest bool
}

func (s *storageDefFlags) bind(c *cobra.Command, forCreate bool) {
	f := c.Flags()
	if forCreate {
		f.StringVar(&s.opts.Type, "type", "",
			"type de stockage : "+strings.Join(pve.KnownStorageTypes(), ", "))
	}
	f.StringVar(&s.opts.Content, "content", "",
		"types de contenu acceptés, séparés par des virgules : backup, iso, vztmpl, images, rootdir, snippets")
	f.StringVar(&s.opts.Server, "server", "", "hôte du serveur NFS / CIFS / PBS")
	f.StringVar(&s.opts.Export, "export", "", "chemin exporté par le serveur NFS")
	f.StringVar(&s.opts.Share, "share", "", "nom du partage CIFS")
	f.StringVar(&s.opts.Datastore, "datastore", "", "datastore du Proxmox Backup Server")
	f.StringVar(&s.opts.Path, "path", "", "répertoire sur le nœud (type dir)")
	f.StringVar(&s.opts.Username, "username", "",
		"compte d'accès — pour un PBS, avec son realm : archiver@pbs")
	f.StringVar(&s.opts.Domain, "domain", "", "domaine du compte CIFS")
	f.StringVar(&s.opts.Fingerprint, "fingerprint", "",
		"empreinte du certificat PBS — exigée en pratique dès qu'il est auto-signé")
	f.StringVar(&s.opts.Namespace, "namespace", "", "espace de noms dans le datastore PBS")
	f.StringVar(&s.opts.Options, "options", "", "options de montage passées telles quelles")
	f.StringVar(&s.opts.Nodes, "nodes", "", "restreint le stockage à ces nœuds, séparés par des virgules")
	f.StringVar(&s.opts.PruneBackups, "prune-backups", "",
		"politique de rétention ENTIÈRE du stockage : « keep-last=3,keep-daily=7 »")
	f.IntVar(&s.opts.Port, "port", 0, "port du serveur, quand il n'est pas celui par défaut")
}

// resolveStoragePassword applique la décision D1 du dépôt : un mot de passe ne
// passe NI par un drapeau, NI par le fichier de configuration.
//
// Il vient de l'environnement, sinon d'une saisie masquée si le terminal le
// permet, sinon la commande refuse. Refuser est délibéré : l'alternative est de
// déclarer un PBS sans mot de passe dans un script qui croyait en poser un — et
// la sauvegarde ne partira jamais, sans que rien ne le dise.
func resolveStoragePassword(cmd *cobra.Command, typ string, guest bool) (string, error) {
	required, accepted := pve.StorageTypeNeedsPassword(typ)
	if !accepted {
		if guest {
			return "", &exitError{
				code: pve.ExitUsage,
				msg:  fmt.Sprintf("--guest ne veut rien dire pour un stockage de type %q : seul cifs se monte en invité", typ),
			}
		}
		return "", nil
	}
	if guest {
		if required {
			return "", &exitError{
				code: pve.ExitUsage,
				msg: fmt.Sprintf("un stockage de type %q exige un mot de passe : --guest ne s'y applique pas.\n"+
					"  export "+EnvStoragePassword+"=\"…\"", typ),
			}
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"le partage sera monté en INVITÉ (username=guest) — aucun mot de passe n'est envoyé.")
		return "", nil
	}
	return readStoragePassword(cmd, typ, required)
}

func readStoragePassword(cmd *cobra.Command, typ string, required bool) (string, error) {
	if fromEnv := os.Getenv(EnvStoragePassword); fromEnv != "" {
		return fromEnv, nil
	}
	if !stdinIsTerminal() {
		hint := ""
		if !required {
			hint = "\n  ou --guest, pour monter le partage en invité"
		}
		return "", &exitError{
			code: pve.ExitConfirm,
			msg: fmt.Sprintf("un stockage de type %q a besoin d'un mot de passe, et il n'y a ni variable\n"+
				"ni terminal pour le demander. Sans lui, la déclaration est acceptée par le nœud\n"+
				"et rien n'y sera jamais écrit — l'échec serait silencieux et différé.\n"+
				"  export "+EnvStoragePassword+"=\"…\"%s", typ, hint),
		}
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Mot de passe du stockage (non affiché) : ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}
	password := string(raw)
	if password == "" {
		return "", &exitError{code: pve.ExitUsage, msg: "mot de passe vide — rien n'a été envoyé"}
	}
	return password, nil
}

// ---------------------------------------------------------------- add

func newStorageDefAddCmd() *cobra.Command {
	s := &storageDefFlags{}

	c := &cobra.Command{
		Use:     "add <nom> --type <type>",
		Aliases: []string{"create"},
		Short:   "Déclare un stockage (POST /storage)",
		Long: `Déclare un nouveau stockage dans le cluster.

  pvecli storage def add nas-backup --type nfs \
      --server 192.168.1.50 --export /export/pve --content backup

  pvecli storage def add pbs-infra --type pbs \
      --server pbs.lan --datastore archives --username archiver@pbs \
      --fingerprint AA:BB:… --content backup

  pvecli storage def add smb-iso --type cifs \
      --server nas.lan --share iso --content iso --guest

--CONTENT EST OBLIGATOIRE, alors que l'API le donne pour optionnel. Sans lui,
PVE choisit un défaut — qui peut très bien ne pas contenir « backup ». On
obtient alors un stockage d'apparence parfaitement normale sur lequel aucune
sauvegarde n'atterrira jamais. Même logique que la rétention exigée par
« backup job create ».

UN PBS N'ACCEPTE QUE « backup ». Y déclarer « iso » est refusé ici, parce que
le nœud, lui, l'accepterait sans rien en faire.

LE MOT DE PASSE (cifs, pbs) NE SE DONNE PAS EN ARGUMENT. Il vient de
` + EnvStoragePassword + `, ou d'une saisie masquée si le terminal le permet :

  export ` + EnvStoragePassword + `="…"

Un drapeau serait visible dans « ps » par tout utilisateur de la machine, et
resterait dans l'historique du shell. Pour un CIFS public, --guest monte le
partage en invité et se passe explicitement de secret. Pour un PBS il n'y a pas
d'échappatoire : sans mot de passe, la sauvegarde ne partira jamais.

CHAMPS EXIGÉS PAR TYPE :
  nfs    --server --export
  cifs   --server --share            (mot de passe, ou --guest)
  pbs    --server --datastore --username   (mot de passe OBLIGATOIRE)
  dir    --path

Endpoint : POST /api2/json/storage (Datastore.Allocate sur /storage)`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			s.opts.Storage = args[0]

			// Toute la validation est LOCALE et précède le réseau : une
			// combinaison type/drapeaux incohérente n'a aucune raison de coûter
			// un aller-retour, et le 400 du nœud serait moins clair.
			if err := pve.ValidateStorageDefOptions(s.opts); err != nil {
				return &exitError{code: pve.ExitUsage, msg: err.Error()}
			}
			password, err := resolveStoragePassword(cmd, s.opts.Type, s.guest)
			if err != nil {
				return err
			}
			s.opts.Password = password
			if s.guest {
				s.opts.Username = "guest"
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: s.opts.Storage,
				Plan: service.Plan{
					Method:  "POST",
					Path:    pve.StorageDefsPath(),
					Payload: s.opts.Values(),
					Effect: fmt.Sprintf("stockage %s déclaré : %s vers %s, contenu %s",
						s.opts.Storage, pve.StorageTypeLabel(s.opts.Type),
						orDash(storageDefTarget(s.opts)), s.opts.Content),
					Rollback: "pvecli storage def rm " + s.opts.Storage + " (n'efface aucune donnée)",
					Verify:   "le stockage doit répondre sur /storage/{storage}",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.StorageDefByID(ctx, s.opts.Storage); err == nil {
						return service.State{}, fmt.Errorf(
							"le stockage « %s » est déjà déclaré — modifie-le plutôt :\n"+
								"  pvecli storage def set %s …", s.opts.Storage, s.opts.Storage)
					}
					// Exists=true veut dire « la précondition tient », pas « le
					// stockage existe » : le pipeline refuse d'écrire sur un
					// false, et ce qui doit exister ici, c'est un nom libre.
					// Attention : l'erreur ci-dessus est un HTTP 500 sur un nom
					// inconnu, pas un 404 — c'est le nœud qui parle ainsi.
					return service.State{Exists: true, Status: "nom libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.CreateStorageDef(ctx, s.opts)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					def, err := client.StorageDefByID(ctx, s.opts.Storage)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "déclaré", Raw: *def}, nil
				},
			})
			if err != nil {
				return err
			}

			// Un --dry-run n'a pas de résultat. Le pipeline rend alors l'état
			// AVANT l'écriture ; le passer au rendu ferait sortir sur stdout un
			// stockage inchangé présenté comme le stockage créé — et
			// « -o json | jq » lirait cette fiction comme un fait. Le plan est
			// déjà sur stderr, il est la seule sortie honnête de ce mode.
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return nil
			}

			def, ok := result.Raw.(pve.StorageDef)
			if !ok {
				rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
					{"stockage", s.opts.Storage}, {"état", result.Status},
				}}
				return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
			}

			// Un stockage déclaré n'est pas un stockage joignable : PVE écrit la
			// configuration sans monter le partage. « storage ls » interroge le
			// nœud et dit s'il répond vraiment.
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"\n« %s » est DÉCLARÉ, ce qui ne prouve pas qu'il soit joignable.\n"+
					"  pvecli storage ls        (colonne ACTIF : le nœud a-t-il réussi à le monter ?)\n", def.Storage)

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"stockage", def.Storage},
				{"type", def.Type},
				{"content", orDash(def.Content)},
				{"cible", orDash(def.Target())},
				{"état", storageDefState(def)},
			}}
			return output.Render(cmd.OutOrStdout(), opts, def, rows)
		},
	}

	s.bind(c, true)
	c.Flags().BoolVar(&s.guest, "guest", false,
		"monte un partage CIFS en INVITÉ, sans mot de passe — à assumer")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// storageDefTarget rend la cible demandée, avant que le nœud n'ait rien confirmé.
func storageDefTarget(o pve.StorageDefOptions) string {
	return pve.StorageDef{
		Server: o.Server, Export: o.Export, Share: o.Share,
		Datastore: o.Datastore, Path: o.Path,
	}.Target()
}

// ---------------------------------------------------------------- set

func newStorageDefSetCmd() *cobra.Command {
	s := &storageDefFlags{}
	var askPassword bool

	c := &cobra.Command{
		Use:     "set <storage>",
		Aliases: []string{"update"},
		Short:   "Modifie une définition (PUT /storage/{storage})",
		Long: `Modifie un stockage déclaré.

SEULS LES DRAPEAUX EXPLICITEMENT PASSÉS sont envoyés — le PUT est partiel.

QUATRE CHAMPS SONT IMMUABLES : --export, --share, --datastore et --path sont
dans le schéma du POST et ABSENTS de celui du PUT. On ne peut donc PAS
repointer un NFS, un CIFS, un PBS ou un répertoire ailleurs. Il faut supprimer
la définition et la recréer — ce qui n'efface AUCUNE donnée, « rm » ne retirant
que l'entrée de configuration. La commande refuse ces drapeaux ici plutôt que
de laisser le nœud rendre un 400 illisible.

--CONTENT EST REMPLACÉ, PAS FUSIONNÉ. Ici c'est honnête : l'unité du drapeau
CLI est la même que celle de l'API — une chaîne à virgules pour une chaîne à
virgules. Passer « --content backup » sur un stockage qui portait
« iso,vztmpl,backup » lui retire iso et vztmpl. Écris la liste complète.

--PRUNE-BACKUPS remplace de même la politique de rétention ENTIÈRE du stockage.
Il n'y a volontairement pas six drapeaux --keep-* ici : ils promettraient une
unité de mise à jour que l'API n'a pas.

DEUX MODIFICATIONS SONT TRAITÉES COMME DESTRUCTIVES, parce qu'elles en ont la
conséquence : retirer « backup » du contenu, et --disable. Les jobs planifiés
qui écrivaient ici ne cassent pas tout de suite — ils restent, leur prochaine
exécution reste annoncée, et ils échouent à chaque passage sans que personne ne
regarde. La commande les nomme avant, et demande de retaper l'identifiant.

LE DIGEST lu au pre-read est renvoyé avec l'écriture. Il couvre TOUT
/etc/pve/storage.cfg, pas cette seule entrée : si N'IMPORTE QUEL stockage a
changé entre la lecture et l'écriture, le PUT échoue. L'échec est bruyant et la
commande rejouable — c'est le comportement voulu, pas un défaut.

  pvecli storage def set nas-backup --content backup,iso
  pvecli storage def set nas-backup --prune-backups 'keep-last=3,keep-daily=7'
  pvecli storage def set nas-backup --disable          # suspendre sans supprimer
  pvecli storage def set smb-iso --password            # resaisir le mot de passe

--password est un BOOLÉEN, pas une valeur : il déclenche la resaisie depuis
` + EnvStoragePassword + ` ou une saisie masquée. Sans lui, aucun mot de passe
n'est envoyé.

Endpoint : PUT /api2/json/storage/{storage} (Datastore.Allocate sur /storage)`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			f := cmd.Flags()

			// Le refus des immuables précède TOUT appel réseau : demander au nœud
			// d'exécuter ce qu'on sait impossible ne rendrait qu'un 400.
			for _, key := range pve.StorageDefPostOnlyKeys {
				if !f.Changed(key) {
					continue
				}
				return &exitError{
					code: pve.ExitUsage,
					msg: fmt.Sprintf("--%s ne peut pas être modifié : ce champ est dans le schéma du POST et\n"+
						"ABSENT de celui du PUT. Un stockage ne se repointe pas ailleurs, il se redéclare :\n"+
						"  pvecli storage def show %s        (relève les valeurs actuelles)\n"+
						"  pvecli storage def rm %s          (n'efface AUCUNE donnée du partage)\n"+
						"  pvecli storage def add %s --%s … (avec la nouvelle cible)", key, id, id, id, key),
				}
			}

			changed := map[string]bool{}
			for _, key := range pve.StorageDefUpdatableKeys {
				// « password » est le seul drapeau dont la présence ne dit PAS
				// qu'il faut l'envoyer : il est booléen, et sa valeur ne vient
				// pas de la ligne de commande. Le lire par Flags().Changed
				// comme les autres ferait envoyer « password= » — donc EFFACER
				// le mot de passe enregistré — sur un simple
				// « --password=false », qui demande exactement l'inverse.
				if key == "password" {
					continue
				}
				if f.Changed(key) {
					changed[key] = true
				}
			}
			// Seul un --password VRAI déclenche la resaisie et l'envoi.
			if askPassword {
				changed["password"] = true
			}
			if len(changed) == 0 {
				return &exitError{
					code: pve.ExitUsage,
					msg:  "aucune modification demandée — passe au moins un drapeau (--content, --nodes, --disable…)",
				}
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			if askPassword {
				password, err := readStoragePassword(cmd, "", false)
				if err != nil {
					return err
				}
				s.opts.Password = password
			}

			// La lecture précède la composition du payload, et pas seulement le
			// pre-read : le digest anti-concurrence en vient, et il doit être
			// celui qui sera AFFICHÉ dans le plan.
			current, err := client.StorageDefByID(cmd.Context(), id)
			if err != nil {
				return err
			}
			payload := s.opts.UpdateValues(changed, current.Digest)

			// Deux modifications ont exactement la conséquence d'une suppression,
			// sans en porter le nom : retirer « backup » du contenu, et éteindre
			// le stockage. Dans les deux cas, les jobs planifiés qui écrivaient
			// ici échouent à chaque passage — la panne silencieuse que « rm »
			// garde déjà contre. La garde suit donc la conséquence, pas le verbe,
			// comme partout ailleurs dans ce dépôt.
			// La liste demandée se relit avec le même prédicat que celle du nœud
			// — l'ordre de « content » n'est pas stable, une comparaison de
			// chaînes serait fausse.
			wanted := pve.StorageDef{Content: s.opts.Content}
			breaksBackups := changed["content"] &&
				current.Accepts("backup") && !wanted.Accepts("backup")
			if changed["disable"] && s.opts.Disable {
				breaksBackups = true
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target:      id,
				Destructive: breaksBackups,
				Plan: service.Plan{
					Method:   "PUT",
					Path:     pve.StorageDefPath(id),
					Payload:  payload,
					Effect:   fmt.Sprintf("stockage %s modifié : %s", id, strings.Join(sortedKeys(payload), ", ")),
					Rollback: "réappliquer les anciennes valeurs — pvecli storage def show " + id + " avant de modifier",
					Verify:   "relecture de la définition",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					// Le stockage vient d'être lu, juste au-dessus : le relire ici
					// n'ajouterait pas de preuve, seulement une fenêtre de plus
					// entre la décision et l'écriture — et le digest en deviendrait
					// différent de celui affiché dans le plan.
					if breaksBackups {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"\n« %s » cesse d'accepter les sauvegardes.\n", id)
						printStorageDefDependents(ctx, cmd, client, id)
					}
					return service.State{Exists: true, Status: storageDefState(*current), Raw: *current}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.UpdateStorageDef(ctx, id, payload)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					def, err := client.StorageDefByID(ctx, id)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "modifié", Raw: *def}, nil
				},
			})
			if err != nil {
				return err
			}

			// Un --dry-run n'a pas de résultat. Le pipeline rend alors l'état
			// AVANT l'écriture ; le passer au rendu ferait sortir sur stdout un
			// stockage inchangé présenté comme le stockage modifié — et
			// « -o json | jq » lirait cette fiction comme un fait. Le plan est
			// déjà sur stderr, il est la seule sortie honnête de ce mode.
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return nil
			}

			def, ok := result.Raw.(pve.StorageDef)
			if !ok {
				rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
					{"stockage", id}, {"état", result.Status},
				}}
				return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
			}

			// Le contenu se compare comme un ENSEMBLE : PVE ne garantit pas
			// l'ordre de cette liste, et le même stockage la rend dans deux ordres
			// différents à une seconde d'intervalle.
			if changed["content"] && !pve.SameContentTypes(def.Content, s.opts.Content) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"⚠ le nœud rend « content=%s » alors que %s était demandé.\n", def.Content, s.opts.Content)
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"stockage", def.Storage},
				{"type", def.Type},
				{"content", orDash(def.Content)},
				{"cible", orDash(def.Target())},
				{"état", storageDefState(def)},
			}}
			return output.Render(cmd.OutOrStdout(), opts, def, rows)
		},
	}

	s.bind(c, false)
	// --disable est une VALEUR, pas un effacement : --disable=false réactive un
	// stockage suspendu. Il n'existe pas sur « add » — déclarer un stockage
	// déjà éteint n'a jamais servi à personne.
	c.Flags().BoolVar(&s.opts.Disable, "disable", false,
		"suspend le stockage sans supprimer sa déclaration — --disable=false le réactive")
	c.Flags().BoolVar(&askPassword, "password", false,
		"resaisit le mot de passe depuis "+EnvStoragePassword+" ou une saisie masquée (pas une valeur)")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// ---------------------------------------------------------------- rm

func newStorageDefRmCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <storage>",
		Aliases: []string{"delete"},
		Short:   "Supprime une définition de stockage (DELETE /storage/{storage})",
		Long: `Supprime la DÉCLARATION d'un stockage.

CE QUI N'EST PAS SUPPRIMÉ : les données. Cet appel retire l'entrée de
/etc/pve/storage.cfg, rien d'autre. Les archives sur le partage NFS ou CIFS,
les snapshots dans le datastore PBS, les fichiers du répertoire : tout reste en
place. C'est le miroir exact de « backup job rm », qui supprime la
planification et pas les archives. Redéclarer le même stockage plus tard
retrouve son contenu.

CE QUI CASSE, en revanche, ce sont les jobs qui écrivaient dessus. Le pre-read
lit /cluster/backup et nomme tout job planifié dont le stockage est celui-ci :
supprimer la destination d'un job le fait échouer à chaque exécution, en
silence. Si cette lecture est refusée (403 — elle exige Sys.Audit sur /), la
suppression reste possible, mais l'absence d'avertissement ne prouve alors
rien.

Opération destructive : la confirmation exige de retaper l'identifiant.

Pour SUSPENDRE au lieu de supprimer — réversible, et c'est presque toujours ce
qu'on voulait :

  pvecli storage def set <storage> --disable

Attention à ne pas confondre avec « pvecli storage rm <storage> <volid> », qui
supprime UN VOLUME dans un stockage. Ici on supprime la déclaration entière.

Endpoint : DELETE /api2/json/storage/{storage} (Datastore.Allocate sur /storage)`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target:      id,
				Destructive: true,
				Plan: service.Plan{
					Method: "DELETE",
					Path:   pve.StorageDefPath(id),
					Effect: fmt.Sprintf("la déclaration de %s disparaît — les DONNÉES du partage restent intactes", id),
					Rollback: "redéclarer le stockage (pvecli storage def add " + id +
						" …) ; son contenu est retrouvé",
					Verify: "le stockage ne doit plus répondre sur /storage/{storage}",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					def, err := client.StorageDefByID(ctx, id)
					if err != nil {
						return service.State{}, err
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"stockage visé : %s → %s (%s), contenu %s\n",
						def.Storage, orDash(def.Target()), def.Type, orDash(def.Content))
					printStorageDefDependents(ctx, cmd, client, id)
					return service.State{Exists: true, Status: storageDefState(*def), Raw: *def}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.DeleteStorageDef(ctx, id)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.StorageDefByID(ctx, id); err == nil {
						return service.State{}, fmt.Errorf(
							"le stockage « %s » répond encore — la suppression n'a pas pris", id)
					}
					return service.State{Exists: false, Status: "supprimé"}, nil
				},
			})
			if err != nil {
				return err
			}

			// Un --dry-run n'a pas de résultat. Le pipeline rend alors l'état
			// AVANT l'écriture ; le passer au rendu ferait sortir sur stdout un
			// stockage intact présenté comme supprimé — et « -o json | jq »
			// lirait cette fiction comme un fait. Le plan est déjà sur stderr,
			// il est la seule sortie honnête de ce mode.
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return nil
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"stockage", id}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// printStorageDefDependents nomme les jobs de sauvegarde qui écrivent ici.
//
// Supprimer la destination d'un job planifié ne casse rien tout de suite : le
// job reste, sa prochaine exécution reste annoncée, et il échoue à chaque
// passage sans que personne ne regarde. C'est précisément le mode de panne que
// ce dépôt existe pour rendre visible.
func printStorageDefDependents(ctx context.Context, cmd *cobra.Command, client *pve.Client, id string) {
	jobs, err := client.BackupJobs(ctx)
	if err != nil {
		// Un 403 sur /cluster/backup (il exige Sys.Audit sur /) n'empêche pas de
		// supprimer le stockage : il empêche seulement de dire qui en pâtira.
		// Échouer ici transformerait un manque d'information en blocage ; se
		// taire le transformerait en illusion.
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"⚠ /cluster/backup illisible (%v) : impossible de vérifier si un job planifié\n"+
				"  écrit sur « %s ». Ce n'est PAS la preuve qu'aucun n'en dépend.\n", err, id)
		return
	}
	var dependents []string
	for _, j := range jobs {
		if j.Storage == id {
			dependents = append(dependents, fmt.Sprintf("%s (%s, %s)", j.ID, j.Target(), orDash(j.Schedule)))
		}
	}
	if len(dependents) == 0 {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"⚠ %d job(s) de sauvegarde écrivent sur « %s » et ÉCHOUERONT à chaque exécution :\n",
		len(dependents), id)
	for _, d := range dependents {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "    "+d)
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
		"  repointe-les d'abord : pvecli backup job set <id> --storage <autre stockage>")
}
