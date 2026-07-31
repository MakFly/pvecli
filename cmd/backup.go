package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MakFly/pvectl/internal/output"
	"github.com/MakFly/pvectl/internal/pve"
	"github.com/MakFly/pvectl/internal/service"
	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Sauvegardes : lancer, lister, restaurer",
		Long: `Sauvegarde et restauration de guests (vzdump).

Une sauvegarde n'est pas un snapshot. Le snapshot (PVX-028) est un point de
retour local qui vit sur le MÊME stockage que le disque qu'il protège ; si ce
stockage meurt, les deux meurent ensemble. Une sauvegarde est une copie
indépendante, qu'on peut poser ailleurs.

Et une sauvegarde n'est validée que par une restauration réellement testée. Un
« exitstatus OK » sur un vzdump ne prouve rien du contenu de l'archive.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newBackupRunCmd(), newBackupListCmd(), newBackupRestoreCmd())
	return c
}

// ---------------------------------------------------------------- run

func newBackupRunCmd() *cobra.Command {
	var (
		o      pve.VZDumpOptions
		mode   string
		detach bool
	)

	c := &cobra.Command{
		Use:   "run [vmid...]",
		Short: "Lance une sauvegarde (POST /nodes/{node}/vzdump)",
		Long: `Sauvegarde un ou plusieurs guests vers un stockage acceptant le type
« backup ».

LES TROIS MODES, et ce qu'ils coûtent :

  stop       le guest est arrêté le temps de la copie. Cohérence totale,
             indisponibilité totale.
  suspend    le guest est gelé au démarrage de la copie. Entre les deux.
  snapshot   le guest continue de tourner. La cohérence est alors AU NIVEAU
             BLOC, pas au niveau applicatif : l'archive contient ce que le
             disque contenait à un instant, ce qui n'est pas la même chose que
             ce que l'application avait fini d'écrire. Une base de données peut
             se restaurer incohérente malgré un « exitstatus OK ».

--prune applique la politique de rétention du stockage après la sauvegarde, et
supprime donc des archives plus anciennes. Éteint par défaut ici, contrairement
à l'API : une commande de sauvegarde qui détruit des sauvegardes est une
mauvaise surprise.

Tâche LONGUE : plusieurs minutes pour quelques gigaoctets. --detach rend la
main immédiatement en affichant l'UPID.

Endpoint : POST /api2/json/nodes/{node}/vzdump`,
		Args: usage(cobra.ArbitraryArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				vmid, err := strconv.Atoi(arg)
				if err != nil {
					return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", arg)}
				}
				o.VMIDs = append(o.VMIDs, vmid)
			}
			if len(o.VMIDs) == 0 && !o.All {
				return &exitError{code: pve.ExitUsage, msg: "précise au moins un vmid, ou --all"}
			}
			o.Mode = pve.BackupMode(mode)

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

			// Counted before the write so the post-read can tell the new
			// archives from the ones that were already there.
			var before int
			target := strings.Join(args, ",")
			if o.All {
				target = "tous les guests"
			}
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.BackupPath(node),
					Payload:  o.Values(),
					Effect:   fmt.Sprintf("sauvegarde de %s vers %s (mode %s)", target, o.Storage, o.Mode),
					Rollback: "aucun — une sauvegarde n'altère pas la source",
					Verify:   "la nouvelle archive doit apparaître dans le contenu du stockage",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					if err := checkBackupStorage(ctx, client, node, o.Storage); err != nil {
						return service.State{}, err
					}
					archives, err := client.Backups(ctx, node, o.Storage)
					if err != nil {
						return service.State{}, err
					}
					before = len(archives)
					return service.State{Exists: true, Status: "stockage prêt"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					upid, err := client.Backup(ctx, node, o)
					if err != nil {
						return "", err
					}
					if detach && upid != "" {
						// Detaching means not waiting — so the pipeline must not
						// poll either. Returning an empty string is how it is
						// told there is nothing to follow.
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"tâche lancée, la main t'est rendue :\n  %s\n  pvectl task wait %s\n", upid, upid)
						return "", nil
					}
					return upid, nil
				},
				// The proof is the archive, not the exitstatus. A vzdump that
				// reports OK and leaves nothing on the storage is exactly the
				// failure this project exists to catch.
				PostRead: func(ctx context.Context) (service.State, error) {
					if detach {
						return service.State{Exists: true, Status: "détachée"}, nil
					}
					archives, err := client.Backups(ctx, node, o.Storage)
					if err != nil {
						return service.State{}, err
					}
					if len(archives) <= before {
						return service.State{}, fmt.Errorf(
							"aucune archive nouvelle sur %s — la tâche s'est terminée sans rien écrire", o.Storage)
					}
					return service.State{Exists: true, Status: "sauvegardé", Raw: archives[0]}, nil
				},
			})
			if err != nil {
				return err
			}

			archive, ok := result.Raw.(pve.Archive)
			if !ok {
				rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
					{"cible", target}, {"état", result.Status},
				}}
				return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
			}
			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"archive", archive.VolID},
				{"taille", output.Bytes(archive.Size)},
				{"créée", output.Timestamp(archive.CTime)},
			}}
			return output.Render(cmd.OutOrStdout(), opts, archive, rows)
		},
	}

	f := c.Flags()
	f.BoolVar(&o.All, "all", false, "sauvegarde tous les guests du nœud")
	f.StringVar(&o.Storage, "storage", "local", "stockage de destination — doit accepter le type « backup »")
	f.StringVar(&mode, "mode", string(pve.ModeSnapshot), "snapshot, suspend ou stop")
	f.StringVar(&o.Compress, "compress", "zstd", "compression : zstd, gzip, lzo, ou 0 pour aucune")
	f.StringVar(&o.Notes, "notes", "", "note attachée à l'archive ({{guestname}}, {{vmid}}, {{node}})")
	f.BoolVar(&o.Prune, "prune", false, "applique la rétention du stockage — SUPPRIME des archives plus anciennes")
	f.BoolVar(&detach, "detach", false, "rend la main sans attendre la fin de la tâche")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// checkBackupStorage refuses before the write when the target cannot hold
// backups, and says which ones can. PVE's own error names the content type and
// leaves the operator to guess where else to put it.
func checkBackupStorage(ctx context.Context, client *pve.Client, node, storage string) error {
	eligible, err := client.BackupStorages(ctx, node)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(eligible))
	for _, s := range eligible {
		if s.Storage == storage {
			return nil
		}
		names = append(names, s.Storage)
	}
	if len(names) == 0 {
		return fmt.Errorf("aucun stockage de ce nœud n'accepte le type « backup » — déclare-le dans la configuration du stockage")
	}
	return fmt.Errorf("le stockage %q n'accepte pas le type « backup ». Ceux qui l'acceptent :\n  %s",
		storage, strings.Join(names, "\n  "))
}

// ---------------------------------------------------------------- ls

func newBackupListCmd() *cobra.Command {
	var (
		storage string
		vmid    int
		check   bool
	)

	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les sauvegardes disponibles",
		Long: `Liste les archives, la plus récente en premier.

La colonne ÂGE est la seule qui compte vraiment : c'est la mesure directe du
RPO effectif. Tout ce qui a été écrit depuis n'est dans aucune archive. Un RPO
ne se décrète pas, il se lit ici.

--check fait l'inverse : il liste les guests qui n'ont AUCUNE sauvegarde. Leur
RPO est infini, et c'est l'information la plus utile de cette commande.

Endpoint : GET /api2/json/nodes/{node}/storage/{storage}/content?content=backup`,
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
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			archives, err := collectArchives(cmd.Context(), client, node, storage)
			if err != nil {
				return err
			}
			if check {
				return reportUnprotected(cmd, opts, client, node, archives)
			}

			now := time.Now()
			rows := output.Rows{Headers: []string{"ÂGE", "VMID", "ARCHIVE", "TAILLE", "CRÉÉE", "NOTES"}}
			kept := archives[:0]
			for _, a := range archives {
				id, _ := pve.ArchiveVMID(a.VolID)
				if vmid > 0 && id != vmid {
					continue
				}
				kept = append(kept, a)
				rows.Cells = append(rows.Cells, []string{
					pve.FormatAge(a.Age(now)), strconv.Itoa(id), a.VolID,
					output.Bytes(a.Size), output.Timestamp(a.CTime), a.Notes,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, kept, rows)
		},
	}

	f := c.Flags()
	f.StringVar(&storage, "storage", "", "n'interroge que ce stockage (défaut : tous ceux qui acceptent « backup »)")
	f.IntVar(&vmid, "vmid", 0, "ne garde que les archives de ce guest")
	f.BoolVar(&check, "check", false, "liste au contraire les guests SANS aucune sauvegarde")
	addRenderFlags(c)
	return c
}

func collectArchives(ctx context.Context, client *pve.Client, node, storage string) ([]pve.Archive, error) {
	names := []string{storage}
	if storage == "" {
		eligible, err := client.BackupStorages(ctx, node)
		if err != nil {
			return nil, err
		}
		names = names[:0]
		for _, s := range eligible {
			names = append(names, s.Storage)
		}
	}

	var all []pve.Archive
	for _, name := range names {
		archives, err := client.Backups(ctx, node, name)
		if err != nil {
			return nil, err
		}
		all = append(all, archives...)
	}
	return all, nil
}

// reportUnprotected crosses the guest inventory with the archive listing. A
// guest missing from the archives has an infinite RPO — the one fact a backup
// listing cannot show by listing what exists.
func reportUnprotected(cmd *cobra.Command, opts output.Options, client *pve.Client, node string, archives []pve.Archive) error {
	protected := map[int]time.Time{}
	for _, a := range archives {
		if id, ok := pve.ArchiveVMID(a.VolID); ok {
			when := time.Unix(a.CTime, 0)
			if best, seen := protected[id]; !seen || when.After(best) {
				protected[id] = when
			}
		}
	}

	vms, err := client.VMs(cmd.Context(), node)
	if err != nil {
		return err
	}
	cts, err := client.Containers(cmd.Context(), node)
	if err != nil {
		return err
	}

	now := time.Now()
	type unprotected struct {
		VMID int    `json:"vmid"`
		Name string `json:"name"`
		Type string `json:"type"`
		RPO  string `json:"rpo"`
	}
	var report []unprotected

	rows := output.Rows{Headers: []string{"VMID", "NOM", "TYPE", "RPO"}}
	for _, g := range append(vms, cts...) {
		// A template holds no live data: it is a mould, and losing it costs a
		// rebuild rather than a loss.
		if g.IsTemplate() {
			continue
		}
		when, ok := protected[g.VMID]
		rpo := "INFINI — aucune sauvegarde"
		if ok {
			rpo = pve.FormatAge(now.Sub(when))
		}
		if ok {
			continue
		}
		report = append(report, unprotected{g.VMID, g.Name, string(g.Type), rpo})
		rows.Cells = append(rows.Cells, []string{strconv.Itoa(g.VMID), g.Name, string(g.Type), rpo})
	}

	if len(report) == 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "tous les guests non-template ont au moins une sauvegarde.")
	}
	return output.Render(cmd.OutOrStdout(), opts, report, rows)
}

// ---------------------------------------------------------------- restore

func newBackupRestoreCmd() *cobra.Command {
	var (
		o      pve.RestoreOptions
		newID  int
		detach bool
	)

	c := &cobra.Command{
		Use:   "restore <volid>",
		Short: "Restaure une sauvegarde vers un nouveau vmid",
		Long: `Recrée un guest depuis une archive.

Il n'existe pas d'endpoint « restore » : une restauration est une CRÉATION qui
porte le paramètre « archive ». C'est pour ça que la famille (QEMU ou LXC) se
déduit du nom du volume — l'archive sait déjà ce qu'elle contient.

RESTAURE VERS UN NOUVEAU VMID. C'est le défaut recommandé, et la raison est
qu'une sauvegarde n'est validée que par une restauration testée : restaurer
par-dessus l'original détruit la chose même qu'on cherchait à protéger, avant
d'avoir vérifié que l'archive valait quelque chose. --overwrite existe, exige
de retaper le vmid, et ne devrait servir qu'à une vraie reprise.

ATTENTION AUX ADRESSES. Le guest restauré reprend l'adresse MAC de l'original,
donc potentiellement son adresse IP. Restaurer un doublon sur un réseau où
l'original tourne encore provoque un conflit qui se diagnostique mal.

Une restauration n'est PAS une reprise : le démarrage et la vérification
applicative restent à faire, et cette commande le rappelle à la fin.

Endpoint : POST /api2/json/nodes/{node}/qemu (ou /lxc), paramètre « archive »`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Archive = args[0]
			if newID == 0 {
				return &exitError{code: pve.ExitUsage, msg: "--newid est obligatoire"}
			}
			kind, ok := pve.ArchiveGuestType(o.Archive)
			if !ok {
				return &exitError{
					code: pve.ExitUsage,
					msg: fmt.Sprintf("impossible de déduire le type de guest de %q.\n"+
						"Un volume de sauvegarde ressemble à « local:backup/vzdump-qemu-212-….vma.zst » :\n"+
						"  pvectl backup ls", o.Archive),
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
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			target := strconv.Itoa(newID)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				// Destructive only when it may replace something: the
				// confirmation level follows the consequence.
				Destructive: o.Overwrite,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.CreatePath(kind, node),
					Payload:  o.Values(),
					Effect:   fmt.Sprintf("guest %d recréé depuis %s", newID, o.Archive),
					Rollback: fmt.Sprintf("pvectl %s rm %d", cliGroup(kind), newID),
					Verify:   fmt.Sprintf("relecture de la configuration de %d", newID),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					_, err := client.GuestStatus(ctx, node, kind, newID)
					if err == nil && !o.Overwrite {
						return service.State{}, fmt.Errorf(
							"le vmid %d est déjà pris — une restauration n'écrase rien par défaut.\n"+
								"  restaure ailleurs :   --newid <libre>\n"+
								"  ou assume l'écrasement : --overwrite (confirmation renforcée)", newID)
					}
					return service.State{Exists: true, Status: "destination prête"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					upid, err := client.Restore(ctx, node, kind, newID, o)
					if err != nil {
						return "", err
					}
					if detach && upid != "" {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"restauration lancée, la main t'est rendue :\n  %s\n  pvectl task wait %s\n", upid, upid)
						return "", nil
					}
					return upid, nil
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					if detach {
						return service.State{Exists: true, Status: "détachée"}, nil
					}
					cfg, err := client.GuestConfig(ctx, node, kind, newID)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "restauré", Raw: cfg}, nil
				},
			})
			if err != nil {
				return err
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun && !detach {
				// The command says what it has NOT done. A restoration that
				// reports success while the service is still down is how an
				// RTO gets measured wrong.
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nle guest %d est restauré, il n'est pas rétabli. Restent à faire :\n"+
						"  pvectl %s start %d\n"+
						"  puis la vérification applicative — c'est elle qui arrête le chronomètre du RTO\n",
					newID, cliGroup(kind), newID)
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"vmid", target}, {"type", string(kind)},
				{"archive", o.Archive}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.IntVar(&newID, "newid", 0, "vmid du guest restauré (obligatoire)")
	f.StringVar(&o.Storage, "storage", "", "stockage des disques restaurés")
	f.BoolVar(&o.Overwrite, "overwrite", false, "écrase un guest existant — confirmation renforcée")
	f.BoolVar(&o.Start, "start", false, "démarre le guest restauré")
	f.BoolVar(&detach, "detach", false, "rend la main sans attendre la fin de la tâche")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// ---------------------------------------------------------------- dr drill

func newDRCmd() *cobra.Command {
	var vmid int

	drill := &cobra.Command{
		Use:   "drill",
		Short: "Déroule un exercice de reprise (simulation par défaut)",
		Long: `Affiche le scénario complet d'un exercice de reprise, chronomètres compris.

CETTE COMMANDE NE FAIT RIEN SANS --execute. C'est la seule de la CLI dont
l'exécution détruit quelque chose qui fonctionne, donc la seule dont la
simulation est le défaut plutôt qu'une option.

Ce que l'exercice mesure :

  RPO   l'écart entre la dernière sauvegarde et la panne. Ce qui a été écrit
        dans cet intervalle n'existe plus. Il se LIT sur l'âge de l'archive.
  RTO   la durée entre la panne et le service à nouveau rendu. Pas la fin de
        la restauration : le service RENDU. La différence entre les deux est
        précisément ce que l'exercice sert à découvrir.

Le RTO réel est presque toujours dominé par ce qui n'était PAS dans la
sauvegarde : règles de pare-feu, entrées DNS, secrets hors image, adhésion à un
réseau overlay. Une archive restaure un disque, pas une infrastructure.`,
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if vmid == 0 {
				return &exitError{code: pve.ExitUsage, msg: "--vmid est obligatoire"}
			}
			execute, _ := cmd.Flags().GetBool("execute")
			if execute {
				return &exitError{
					code: pve.ExitUsage,
					msg: "l'exécution automatique n'est volontairement pas implémentée.\n" +
						"Un exercice de reprise se conduit à la main, en notant les temps et ce qui\n" +
						"manque à chaque étape — c'est ça, l'exercice. Le scénario ci-dessus est la\n" +
						"procédure ; suis-la commande par commande.",
				}
			}

			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			archives, err := collectArchives(cmd.Context(), client, node, "")
			if err != nil {
				return err
			}
			rpo := "INFINI — aucune sauvegarde, l'exercice commence par en faire une"
			latest := ""
			for _, a := range archives {
				if id, ok := pve.ArchiveVMID(a.VolID); ok && id == vmid {
					rpo = pve.FormatAge(a.Age(time.Now()))
					latest = a.VolID
					break
				}
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Exercice de reprise — guest %d sur %s\n\n", vmid, node)
			_, _ = fmt.Fprintf(out, "  RPO actuel   %s\n", rpo)
			if latest != "" {
				_, _ = fmt.Fprintf(out, "  archive      %s\n", latest)
			}
			_, _ = fmt.Fprintf(out, `
  0. relever l'état de référence — le service répond-il AVANT la panne ?
  1. pvectl backup run %d --storage local --mode snapshot --compress zstd
  2. pvectl backup ls --vmid %d            ← noter l'heure : c'est le RPO
  3. pvectl vm rm %d --force               ← panne simulée, chronomètre lancé
  4. pvectl backup restore <volid> --newid %d
  5. pvectl vm start %d
  6. vérifier le SERVICE, pas la VM       ← c'est ici que le RTO s'arrête

  puis, dans docs/LEARNING-LOG.md : les deux nombres, et surtout la liste de ce
  qui n'a PAS été restauré.

`, vmid, vmid, vmid, vmid, vmid)
			return nil
		},
	}
	drill.Flags().IntVar(&vmid, "vmid", 0, "guest sur lequel porte l'exercice")
	drill.Flags().Bool("execute", false, "réservé — un exercice se conduit à la main")
	addRenderFlags(drill)

	c := &cobra.Command{
		Use:   "dr",
		Short: "Plan de reprise : exercices et mesures",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(drill)
	return c
}
