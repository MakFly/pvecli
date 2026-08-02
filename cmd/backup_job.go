package cmd

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/service"
)

// newBackupJobCmd pilote les jobs de sauvegarde PLANIFIÉS.
//
// Le nommage suit la famille : « backup » existe déjà, « job » est le mot que
// PVE emploie lui-même (« vzdump backup job »), et ls/show/create/set/rm sont
// les verbes du reste de la CLI (access token, vm snapshot, fw ipset). Le
// pluriel n'apparaît nulle part, comme ailleurs.
func newBackupJobCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "job",
		Short: "Sauvegardes PLANIFIÉES : lister, créer, modifier, supprimer",
		Long: `Gère les jobs de sauvegarde récurrents (/cluster/backup).

« backup run » lance une sauvegarde MAINTENANT : la preuve qu'elle a eu lieu,
c'est qu'on était là pour la lancer. Un job est l'inverse — il tourne quand
personne ne regarde. C'est donc la seule sauvegarde qui existera vraiment le
jour de la panne, et la seule dont l'échec est SILENCIEUX.

Trois conséquences, qui expliquent la forme de ces commandes :

  1. La définition vit au niveau CLUSTER, pas au niveau nœud. Elle est écrite
     dans /etc/pve et répliquée ; --run-on n'est qu'un filtre d'exécution.
  2. Un job sans RÉTENTION remplit le stockage jusqu'à la panne de disque que
     la sauvegarde devait éviter. « create » exige donc au moins un --keep-*.
  3. « ls » affiche la PROCHAINE EXÉCUTION, pas seulement le nom. Un job dont
     la planification n'a pas été comprise par le nœud n'en a aucune, et rien
     d'autre dans la liste ne le distingue d'un job sain.

Et la même règle qu'ailleurs : un job qui affiche « prochaine exécution » ne
prouve rien du contenu des archives. Seule une restauration testée le prouve —
voir « pvecli dr drill ».

Privilèges : Sys.Audit sur / pour lire, Sys.Modify sur / pour écrire.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(
		newBackupJobListCmd(), newBackupJobShowCmd(),
		newBackupJobCreateCmd(), newBackupJobSetCmd(), newBackupJobRmCmd(),
	)
	return c
}

// ---------------------------------------------------------------- ls

func newBackupJobListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les jobs de sauvegarde planifiés (GET /cluster/backup)",
		Long: `Liste les jobs planifiés du cluster.

La colonne PROCHAINE est celle qui compte. « désactivé » et « jamais » ne sont
pas des vides : le premier veut dire qu'on l'a éteint, le second que le nœud
n'a pas retenu la planification — le job est là, il ne tournera pas.

La colonne RÉTENTION vide veut dire que rien ne purge : les archives
s'accumulent jusqu'à saturation du stockage.

Endpoint : GET /api2/json/cluster/backup`,
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
			jobs, err := client.BackupJobs(cmd.Context())
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"aucun job de sauvegarde planifié — le RPO de ce cluster est INFINI par défaut.\n"+
						"  pvecli backup job create --vmid <id> --storage <stockage> --schedule 'daily' --keep-last 3")
			}

			rows := output.Rows{Headers: []string{"ID", "ÉTAT", "CIBLE", "STOCKAGE", "PLANIF", "PROCHAINE", "RÉTENTION", "COMMENTAIRE"}}
			for _, j := range jobs {
				rows.Cells = append(rows.Cells, []string{
					j.ID, jobState(j), j.Target(), orDash(j.Storage), orDash(j.Schedule),
					pve.FormatNextRun(j.NextRun.Int(), j.IsEnabled()),
					orDash(j.RetentionSummary()), j.Comment,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, jobs, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func jobState(j pve.BackupJob) string {
	if j.IsEnabled() {
		return "actif"
	}
	return "désactivé"
}

// ---------------------------------------------------------------- show

func newBackupJobShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show <id>",
		Short: "Affiche un job planifié (GET /cluster/backup/{id})",
		Long: `Affiche la définition complète d'un job.

Endpoint : GET /api2/json/cluster/backup/{id}`,
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
			job, err := client.BackupJobByID(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			retention := job.RetentionSummary()
			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"id", job.ID},
				{"état", jobState(*job)},
				{"cible", job.Target()},
				{"stockage", orDash(job.Storage)},
				{"planification", orDash(job.Schedule)},
				{"prochaine", pve.FormatNextRun(job.NextRun.Int(), job.IsEnabled())},
				{"mode", orDash(job.Mode)},
				{"compression", orDash(job.Compress)},
				{"rétention", orDash(retention)},
				{"nœud", orDash(job.Node)},
				{"notification", orDash(job.MailNotification)},
				{"mailto", orDash(job.Mailto)},
				{"commentaire", orDash(job.Comment)},
			}}
			// Deux façons de ne rien purger, et une seule se voit dans la
			// politique : pas de rétention du tout, ou une rétention écrite
			// mais désarmée par remove=0. La seconde est la pire — elle
			// rassure.
			switch {
			case retention == "":
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"⚠ aucune rétention sur ce job : rien ne purge, le stockage se remplit indéfiniment.\n"+
						"  pvecli backup job set "+job.ID+" --keep-last 3 --prune")
			case !job.Prunes():
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"⚠ la rétention de ce job est INERTE : elle est écrite, mais remove=0 la désarme.\n"+
						"  rien n'est purgé, malgré la politique affichée.\n"+
						"  pvecli backup job set "+job.ID+" --prune")
			}
			return output.Render(cmd.OutOrStdout(), opts, job, rows)
		},
	}
	addRenderFlags(c)
	return c
}

// ---------------------------------------------------------------- create

// backupJobFlags rassemble ce que create et set partagent. Les deux commandes
// lisent les MÊMES drapeaux, mais pas de la même façon : create envoie tout,
// set n'envoie que ce que l'opérateur a explicitement changé.
type backupJobFlags struct {
	opts pve.BackupJobOptions
	mode string
	// vmids est à part parce que cobra remplit un []int, que BackupJobOptions
	// n'expose qu'après validation de la cible.
	vmids []int
}

func (b *backupJobFlags) bind(c *cobra.Command) {
	f := c.Flags()
	f.StringVar(&b.opts.ID, "id", "", "identifiant du job (défaut : généré par PVE)")
	f.StringVar(&b.opts.Schedule, "schedule", "", "planification, format calendrier systemd : « daily », « 03:30 », « mon..fri 22:00 »")
	f.StringVar(&b.opts.Storage, "storage", "", "stockage de destination — doit accepter le type « backup »")
	f.IntSliceVar(&b.vmids, "vmid", nil, "guests à sauvegarder (répétable, ou en liste : --vmid 220,221)")
	f.StringVar(&b.opts.Pool, "pool", "", "sauvegarde tous les guests de ce pool")
	f.BoolVar(&b.opts.All, "all", false, "sauvegarde tous les guests connus du cluster")
	f.StringVar(&b.mode, "mode", string(pve.ModeSnapshot), "snapshot, suspend ou stop")
	f.StringVar(&b.opts.Compress, "compress", "zstd", "compression : zstd, gzip, lzo, ou 0 pour aucune")
	f.StringVar(&b.opts.Comment, "comment", "", "description du job")
	// « run-on » et non « node » : --node est déjà un drapeau persistant de la
	// racine, et il désigne le nœud à qui l'on PARLE. Ici il s'agirait du nœud
	// qui EXÉCUTE le job — deux choses différentes sur un endpoint de cluster.
	// Réutiliser le nom masquerait le drapeau racine et rendrait la commande
	// illisible pour qui a lu l'aide globale.
	f.StringVar(&b.opts.Node, "run-on", "", "n'exécute le job que sur ce nœud (défaut : n'importe lequel)")
	f.StringVar(&b.opts.Notes, "notes", "", "note attachée aux archives ({{guestname}}, {{vmid}}, {{node}})")
	f.StringVar(&b.opts.MailNotification, "mailnotification", "", "always ou failure — déprécié par PVE au profit des cibles de notification")
	f.StringVar(&b.opts.Mailto, "mailto", "", "destinataires, séparés par des virgules — déprécié au profit des cibles de notification")
	f.BoolVar(&b.opts.Enabled, "enabled", true, "job actif (--enabled=false le crée éteint)")

	f.IntVar(&b.opts.Retention.Last, "keep-last", 0, "garde les N dernières archives")
	f.IntVar(&b.opts.Retention.Hourly, "keep-hourly", 0, "garde N archives horaires")
	f.IntVar(&b.opts.Retention.Daily, "keep-daily", 0, "garde N archives quotidiennes")
	f.IntVar(&b.opts.Retention.Weekly, "keep-weekly", 0, "garde N archives hebdomadaires")
	f.IntVar(&b.opts.Retention.Monthly, "keep-monthly", 0, "garde N archives mensuelles")
	f.IntVar(&b.opts.Retention.Yearly, "keep-yearly", 0, "garde N archives annuelles")
}

// resolve valide la cohérence des drapeaux et remplit ce qui en découle.
func (b *backupJobFlags) resolve() error {
	b.opts.VMIDs = b.vmids
	b.opts.Mode = pve.BackupMode(b.mode)

	chosen := 0
	for _, on := range []bool{len(b.opts.VMIDs) > 0, b.opts.Pool != "", b.opts.All} {
		if on {
			chosen++
		}
	}
	if chosen > 1 {
		return &exitError{
			code: pve.ExitUsage,
			msg: "--vmid, --pool et --all désignent trois cibles différentes et s'excluent.\n" +
				"Côté API elles ne cohabitent pas non plus : « all » écrase les deux autres.",
		}
	}
	return nil
}

func newBackupJobCreateCmd() *cobra.Command {
	b := &backupJobFlags{}

	c := &cobra.Command{
		Use:   "create",
		Short: "Crée un job de sauvegarde planifié (POST /cluster/backup)",
		Long: `Crée une sauvegarde récurrente.

  pvecli backup job create --vmid 220,221 --storage <stockage> \
      --schedule '02:30' --mode snapshot --compress zstd \
      --keep-last 3 --keep-daily 7 --comment 'guests critiques'

LA PLANIFICATION est un calendrier systemd, pas une crontab :
  daily            tous les jours à minuit
  02:30            tous les jours à 02h30
  mon..fri 22:00   du lundi au vendredi à 22h00
  *-*-* 04:00      équivalent explicite de « daily » à 04h00

LA RÉTENTION est obligatoire ici, alors qu'elle est facultative côté API. La
raison n'est pas le zèle : un job sans « prune-backups » écrit indéfiniment, et
finit par saturer le stockage — c'est-à-dire par provoquer la panne de disque
que la sauvegarde existait pour absorber. Il faut donc au moins un --keep-*.
Les compteurs se cumulent : --keep-last 3 --keep-daily 7 garde les 3 dernières
ET une par jour sur 7 jours.

LA DESTINATION doit vivre AILLEURS que le disque des guests. Une sauvegarde sur
le même disque que la source ne protège que de l'erreur humaine, pas de la
panne matérielle. Pour voir les candidats :

  pvecli storage ls

L'identifiant est généré par PVE si --id n'est pas donné ; la commande relit
alors la liste pour dire lequel vient d'apparaître.

Endpoint : POST /api2/json/cluster/backup (Sys.Modify sur /)`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := b.resolve(); err != nil {
				return err
			}
			if len(b.opts.VMIDs) == 0 && b.opts.Pool == "" && !b.opts.All {
				return &exitError{code: pve.ExitUsage, msg: "précise une cible : --vmid, --pool ou --all"}
			}
			if err := pve.ValidateSchedule(b.opts.Schedule); err != nil {
				return &exitError{code: pve.ExitUsage, msg: err.Error()}
			}
			if b.opts.ID != "" {
				if err := pve.ValidateJobID(b.opts.ID); err != nil {
					return &exitError{code: pve.ExitUsage, msg: err.Error()}
				}
			}
			if b.opts.Storage == "" {
				return &exitError{code: pve.ExitUsage, msg: "--storage est obligatoire — un job sans destination n'écrit nulle part"}
			}
			if b.opts.Retention.Empty() {
				return &exitError{
					code: pve.ExitUsage,
					msg: "aucune rétention : ce job écrirait sans jamais purger, jusqu'à saturer le stockage.\n" +
						"  ajoute par exemple : --keep-last 3 --keep-daily 7",
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

			// L'identifiant généré n'est pas rendu par le POST. On relève donc
			// les identifiants existants AVANT, pour pouvoir nommer celui qui
			// est apparu APRÈS — plutôt que d'annoncer une création sans savoir
			// laquelle.
			before := map[string]bool{}
			target := b.opts.ID
			if target == "" {
				target = "nouveau job"
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Method:  "POST",
					Path:    pve.BackupJobsPath(),
					Payload: b.opts.Values(),
					Effect: fmt.Sprintf("sauvegarde de %s vers %s, %s, rétention %s",
						jobTargetLabel(b.opts), b.opts.Storage, b.opts.Schedule, b.opts.Retention),
					Rollback: "pvecli backup job rm <id>",
					Verify:   "le job doit apparaître dans /cluster/backup avec une prochaine exécution",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					jobs, err := client.BackupJobs(ctx)
					if err != nil {
						return service.State{}, err
					}
					for _, j := range jobs {
						before[j.ID] = true
						if b.opts.ID != "" && j.ID == b.opts.ID {
							return service.State{}, fmt.Errorf(
								"le job %q existe déjà — modifie-le plutôt :\n  pvecli backup job set %s …", j.ID, j.ID)
						}
					}
					// Exists=true veut dire « la précondition tient », pas « le
					// job existe » : le pipeline refuse d'écrire sur un false,
					// et ce qui doit exister ici, c'est un identifiant libre.
					return service.State{Exists: true, Status: "identifiant libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.CreateBackupJob(ctx, b.opts)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					jobs, err := client.BackupJobs(ctx)
					if err != nil {
						return service.State{}, err
					}
					for _, j := range jobs {
						if before[j.ID] {
							continue
						}
						if b.opts.ID != "" && j.ID != b.opts.ID {
							continue
						}
						return service.State{Exists: true, Status: "créé", Raw: j}, nil
					}
					return service.State{}, fmt.Errorf(
						"aucun job nouveau dans /cluster/backup — la création n'a rien écrit")
				},
			})
			if err != nil {
				return err
			}

			// Un --dry-run n'a pas de résultat. Le pipeline rend alors l'état
			// AVANT l'écriture ; le passer au rendu ferait sortir sur stdout un
			// job inchangé présenté comme le job modifié — et « -o json | jq »
			// lirait cette fiction comme un fait. Le plan est déjà sur stderr,
			// il est la seule sortie honnête de ce mode.
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return nil
			}

			job, ok := result.Raw.(pve.BackupJob)
			if !ok {
				rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
					{"cible", target}, {"état", result.Status},
				}}
				return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
			}

			// Un job créé n'est pas un job vérifié : PVE accepte une
			// planification qu'il n'appliquera pas. « prochaine » est la seule
			// preuve que le planificateur l'a retenue.
			if !job.IsEnabled() || job.NextRun.Int() <= 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\n⚠ le job %s n'a AUCUNE prochaine exécution : la planification %q n'a pas été retenue,\n"+
						"  ou le job est désactivé. Il ne sauvegardera rien en l'état.\n",
					job.ID, b.opts.Schedule)
			}
			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"id", job.ID},
				{"cible", job.Target()},
				{"stockage", orDash(job.Storage)},
				{"planification", orDash(job.Schedule)},
				{"prochaine", pve.FormatNextRun(job.NextRun.Int(), job.IsEnabled())},
				{"rétention", orDash(job.RetentionSummary())},
			}}
			return output.Render(cmd.OutOrStdout(), opts, job, rows)
		},
	}

	b.bind(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func jobTargetLabel(o pve.BackupJobOptions) string {
	switch {
	case o.All:
		return "tous les guests"
	case o.Pool != "":
		return "pool " + o.Pool
	default:
		ids := make([]string, 0, len(o.VMIDs))
		for _, id := range o.VMIDs {
			ids = append(ids, strconv.Itoa(id))
		}
		return strings.Join(ids, ",")
	}
}

// ---------------------------------------------------------------- set

func newBackupJobSetCmd() *cobra.Command {
	b := &backupJobFlags{}

	c := &cobra.Command{
		Use:     "set <id>",
		Aliases: []string{"update"},
		Short:   "Modifie un job planifié (PUT /cluster/backup/{id})",
		Long: `Modifie un job existant.

Le verbe est « set », comme « vm set » et « access acl set » : c'est la même
opération — écrire des champs sur un objet qui existe déjà. « update » est
accepté en alias.

SEULS LES DRAPEAUX EXPLICITEMENT PASSÉS sont envoyés — à une exception près,
qui est la raison d'être de cette commande :

LA RÉTENTION SE FUSIONNE, ELLE NE SE REMPLACE PAS. Côté API, « prune-backups »
est UNE valeur entière (« keep-last=3,keep-daily=7 »), pas six champs. Envoyer
le seul compteur qu'on vient de changer effacerait les cinq autres, et la
prochaine exécution supprimerait des archives que personne n'avait demandé de
supprimer. La commande relit donc la rétention du nœud, n'écrase que les
--keep-* reçus, et affiche la politique COMPLÈTE dans le plan.

  pvecli backup job set vzdump-abc --schedule '04:00'
  pvecli backup job set vzdump-abc --keep-last 5       # keep-daily est préservé
  pvecli backup job set vzdump-abc --enabled=false     # suspendre sans supprimer
  pvecli backup job set vzdump-abc --all               # change de cible

VIDER UN CHAMP passe par le paramètre « delete » de l'API, pas par une valeur
vide : --run-on '' ou --all=false effacent la clé au lieu d'envoyer une chaîne
vide, que le nœud rejetterait avec un 400 illisible.

Endpoint : PUT /api2/json/cluster/backup/{id} (Sys.Modify sur /)`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := b.resolve(); err != nil {
				return err
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			// « rien à faire » se décide AVANT la lecture : interroger le nœud
			// pour ne rien lui demander ensuite serait un aller-retour gratuit,
			// et une erreur réseau y ferait échouer une commande qui n'avait
			// de toute façon rien à écrire.
			if !anyBackupJobFlagChanged(cmd) {
				return &exitError{
					code: pve.ExitUsage,
					msg:  "aucune modification demandée — passe au moins un drapeau (--schedule, --keep-last, --enabled…)",
				}
			}

			// La lecture précède la composition du payload, et pas seulement le
			// pre-read : sans elle, la fusion de la rétention serait impossible
			// et « --keep-last 5 » effacerait « keep-daily=7 ».
			current, err := client.BackupJobByID(cmd.Context(), id)
			if err != nil {
				return err
			}
			payload, err := changedBackupJobValues(cmd, b, current)
			if err != nil {
				return err
			}
			if len(payload) == 0 {
				return &exitError{
					code: pve.ExitUsage,
					msg:  "aucune modification demandée — passe au moins un drapeau (--schedule, --keep-last, --enabled…)",
				}
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: id,
				Plan: service.Plan{
					Method:   "PUT",
					Path:     pve.BackupJobPath(id),
					Payload:  payload,
					Effect:   fmt.Sprintf("job %s modifié : %s", id, strings.Join(sortedKeys(payload), ", ")),
					Rollback: "réappliquer les anciennes valeurs — pvecli backup job show " + id + " avant de modifier",
					Verify:   "relecture du job et de sa prochaine exécution",
				},
				PreRead: func(_ context.Context) (service.State, error) {
					// Le job vient d'être lu, juste au-dessus : le relire ici
					// n'ajouterait pas de preuve, seulement une fenêtre de plus
					// entre la décision et l'écriture.
					return service.State{Exists: true, Status: jobState(*current), Raw: *current}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.UpdateBackupJob(ctx, id, payload)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					job, err := client.BackupJobByID(ctx, id)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "modifié", Raw: *job}, nil
				},
			})
			if err != nil {
				return err
			}

			// Un --dry-run n'a pas de résultat. Le pipeline rend alors l'état
			// AVANT l'écriture ; le passer au rendu ferait sortir sur stdout un
			// job inchangé présenté comme le job modifié — et « -o json | jq »
			// lirait cette fiction comme un fait. Le plan est déjà sur stderr,
			// il est la seule sortie honnête de ce mode.
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return nil
			}

			job, ok := result.Raw.(pve.BackupJob)
			if !ok {
				rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
					{"id", id}, {"état", result.Status},
				}}
				return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
			}
			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"id", job.ID},
				{"état", jobState(job)},
				{"cible", job.Target()},
				{"planification", orDash(job.Schedule)},
				{"prochaine", pve.FormatNextRun(job.NextRun.Int(), job.IsEnabled())},
				{"rétention", orDash(job.RetentionSummary())},
			}}
			return output.Render(cmd.OutOrStdout(), opts, job, rows)
		},
	}

	b.bind(c)
	// --id n'a pas de sens sur un objet qu'on désigne déjà par son identifiant :
	// le laisser exposerait un moyen de renommer un job par accident.
	_ = c.Flags().MarkHidden("id")
	// --prune n'existe que sur « set » : à la création, la rétention est
	// obligatoire, donc la purge est forcément active et un interrupteur n'y
	// serait qu'un moyen de créer un job dont la rétention ne sert à rien.
	c.Flags().Bool("prune", false,
		"(ré)active la purge selon la rétention — --prune=false la gèle sans effacer la politique")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// backupJobMutableFlags nomme tout ce que « set » sait écrire. La liste sert
// deux fois : à refuser une commande sans effet, et à ne jamais laisser un
// drapeau exister sans être relié à une clé de l'API.
var backupJobMutableFlags = append([]string{
	"schedule", "storage", "mode", "compress", "comment", "run-on", "notes",
	"mailnotification", "mailto", "enabled", "vmid", "pool", "all", "prune",
}, pve.RetentionKeys...)

func anyBackupJobFlagChanged(cmd *cobra.Command) bool {
	for _, name := range backupJobMutableFlags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// changedBackupJobValues compose le PUT à partir des seuls drapeaux passés.
//
// Trois règles, chacune payée par un piège du schéma :
//
//  1. PARTIEL. cobra ne distingue pas « --compress zstd » d'un défaut nommé
//     zstd ; seul Flags().Changed le fait. Sans ça, replanifier un job
//     remettrait au passage sa compression et sa rétention aux défauts de la
//     CLI.
//  2. FUSION de la rétention. « prune-backups » est UNE valeur côté API, pas
//     six champs : n'envoyer que le compteur modifié effacerait les autres, et
//     la prochaine exécution supprimerait des archives que personne n'avait
//     demandé de supprimer. On part donc de la rétention lue sur le nœud.
//  3. EFFACEMENT explicite. Values() omet les clés à leur valeur nulle, donc
//     « --all=false » y rendrait la chaîne vide — que le nœud refuse (« type
//     check ('boolean') failed »). Vider passe par le paramètre « delete » du
//     PUT, prévu pour ça.
func changedBackupJobValues(cmd *cobra.Command, b *backupJobFlags, current *pve.BackupJob) (url.Values, error) {
	f := cmd.Flags()
	v := url.Values{}
	rendered := b.opts.Values()
	var deletes []string

	// ---- rétention : lue, puis surchargée compteur par compteur
	retentionTouched := false
	merged := current.Retention()
	for _, key := range pve.RetentionKeys {
		if !f.Changed(key) {
			continue
		}
		retentionTouched = true
		n, err := f.GetInt(key)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("--%s ne peut pas être négatif", key)}
		}
		merged.Set(key, n)
	}
	if retentionTouched {
		if merged.Empty() {
			return nil, &exitError{
				code: pve.ExitUsage,
				msg: "cette modification ne laisserait AUCUNE purge active : le stockage se\n" +
					"remplirait indéfiniment. Garde au moins un --keep-* > 0.",
			}
		}
		v.Set("prune-backups", merged.String())

		// « remove » n'est PAS forcé à 1. Un job où il vaut 0 a pu être réglé
		// ainsi exprès (purge déléguée au PBS, gel volontaire) ; le rallumer en
		// douce au détour d'un --keep-last ferait supprimer des archives sans
		// que rien ne l'annonce. On refuse et on nomme la sortie.
		if !current.Prunes() {
			return nil, &exitError{
				code: pve.ExitUsage,
				msg: "ce job porte remove=0 : sa rétention est INERTE, la changer ne purgera\n" +
					"toujours rien. Rallume la purge explicitement dans le même geste :\n" +
					"  --prune",
			}
		}
	}
	if f.Changed("prune") {
		on, err := f.GetBool("prune")
		if err != nil {
			return nil, err
		}
		if on {
			v.Set("remove", "1")
		} else {
			v.Set("remove", "0")
		}
	}

	// ---- cible : les trois clés coexistent, le nœud efface les deux autres
	// lui-même (PVE::API2::Backup::update_job). Il suffit donc d'envoyer celle
	// qu'on veut — mais l'ÉTEINDRE demande un « delete », pas une valeur vide.
	if f.Changed("vmid") {
		v.Set("vmid", rendered.Get("vmid"))
	}
	if f.Changed("pool") {
		if b.opts.Pool == "" {
			deletes = append(deletes, "pool")
		} else {
			v.Set("pool", b.opts.Pool)
		}
	}
	if f.Changed("all") {
		if b.opts.All {
			v.Set("all", "1")
		} else {
			deletes = append(deletes, "all")
		}
	}

	// ---- champs simples
	for flag, key := range map[string]string{
		"schedule":         "schedule",
		"storage":          "storage",
		"mode":             "mode",
		"compress":         "compress",
		"comment":          "comment",
		"run-on":           "node",
		"notes":            "notes-template",
		"mailnotification": "mailnotification",
		"mailto":           "mailto",
	} {
		if !f.Changed(flag) {
			continue
		}
		value, err := f.GetString(flag)
		if err != nil {
			return nil, err
		}
		if value == "" {
			// Deux champs ne peuvent pas être vidés : un job sans planification
			// ne tourne jamais, un job sans stockage n'écrit nulle part. Les
			// effacer produirait un job d'apparence normale et parfaitement
			// inutile.
			if flag == "schedule" || flag == "storage" {
				return nil, &exitError{
					code: pve.ExitUsage,
					msg: fmt.Sprintf("--%s ne peut pas être vidé : un job sans %s existe mais ne sauvegarde rien.\n"+
						"  suspends-le plutôt : pvecli backup job set <id> --enabled=false", flag, flag),
				}
			}
			deletes = append(deletes, key)
			continue
		}
		v.Set(key, value)
	}

	// enabled est un booléen à part : 0 est une VALEUR (job suspendu, réversible),
	// pas un effacement.
	if f.Changed("enabled") {
		v.Set("enabled", rendered.Get("enabled"))
	}

	if len(deletes) > 0 {
		sort.Strings(deletes)
		v.Set("delete", strings.Join(deletes, ","))
	}
	return v, nil
}

// ---------------------------------------------------------------- rm

func newBackupJobRmCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "rm <id>",
		Short: "Supprime un job planifié (DELETE /cluster/backup/{id})",
		Long: `Supprime la définition d'un job.

CE QUI DISPARAÎT, c'est la PLANIFICATION — donc les sauvegardes à venir. Les
archives déjà écrites ne sont pas touchées : elles restent sur le stockage, et
plus rien ne les purgera puisque la rétention partait avec le job.

Opération destructive : la confirmation exige de retaper l'identifiant, parce
qu'un « y » réflexe sur une liste de jobs supprime rarement celui qu'on croyait.
--dry-run montre la requête sans l'envoyer.

Pour SUSPENDRE au lieu de supprimer — réversible, et c'est presque toujours ce
qu'on voulait :

  pvecli backup job set <id> --enabled=false

Endpoint : DELETE /api2/json/cluster/backup/{id} (Sys.Modify sur /)`,
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
					Method:   "DELETE",
					Path:     pve.BackupJobPath(id),
					Effect:   fmt.Sprintf("le job %s ne s'exécutera plus — les archives existantes restent, sans purge", id),
					Rollback: "aucun : la définition est perdue, il faut la recréer (pvecli backup job create …)",
					Verify:   "le job doit avoir disparu de /cluster/backup",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					job, err := client.BackupJobByID(ctx, id)
					if err != nil {
						return service.State{}, err
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"job visé : %s → %s vers %s (%s)\n", job.ID, job.Target(), orDash(job.Storage), orDash(job.Schedule))
					return service.State{Exists: true, Status: jobState(*job), Raw: *job}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.DeleteBackupJob(ctx, id)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					jobs, err := client.BackupJobs(ctx)
					if err != nil {
						return service.State{}, err
					}
					for _, j := range jobs {
						if j.ID == id {
							return service.State{}, fmt.Errorf(
								"le job %s est toujours dans /cluster/backup — la suppression n'a pas pris", id)
						}
					}
					return service.State{Exists: false, Status: "supprimé"}, nil
				},
			})
			if err != nil {
				return err
			}

			// Un --dry-run n'a pas de résultat. Le pipeline rend alors l'état
			// AVANT l'écriture ; le passer au rendu ferait sortir sur stdout un
			// job inchangé présenté comme le job modifié — et « -o json | jq »
			// lirait cette fiction comme un fait. Le plan est déjà sur stderr,
			// il est la seule sortie honnête de ce mode.
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return nil
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"id", id}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}
