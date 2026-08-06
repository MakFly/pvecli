package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/service"
	"github.com/spf13/cobra"
)

func newMigrateCmd(kind pve.GuestType) *cobra.Command {
	var o pve.MigrateOptions

	noun := "la VM"
	moveHelp := `  --online              migration à chaud : la VM continue de servir pendant
                        la copie de sa mémoire. Ignoré sur une VM arrêtée.
  --with-local-disks    copie les disques que le nœud cible ne voit pas. Sans
                        stockage partagé, c'est la seule voie — et c'est ce qui
                        transforme une migration de deux secondes en une
                        migration de plusieurs minutes.`
	if kind == pve.TypeLXC {
		noun = "le conteneur"
		moveHelp = `  --restart             UN CONTENEUR NE MIGRE PAS À CHAUD. Il est arrêté,
                        déplacé, puis redémarré. C'est une interruption de
                        service, pas une migration transparente : la différence
                        avec « vm migrate --online » est réelle et il vaut mieux
                        la connaître avant la fenêtre de maintenance.
  --timeout             secondes accordées à l'arrêt avant abandon.`
	}

	c := &cobra.Command{
		Use:   "migrate <vmid>",
		Short: fmt.Sprintf("Déplace %s vers un autre nœud (POST …/%s/{vmid}/migrate)", noun, kind),
		Long: fmt.Sprintf(`Déplace %s vers un autre nœud du cluster.

La commande est triviale ; ses PRÉCONDITIONS ne le sont pas. C'est pourquoi
elle commence toujours par les lire, avec « GET …/migrate », l'endpoint que PVE
appelle lui-même « Get preconditions for migration ». Il répond :

  allowed_nodes      les nœuds qui peuvent accueillir ce guest
  not_allowed_nodes  les autres, ET la raison de leur refus
  local_disks        les disques qu'il faudrait recopier
  local_resources    ce qui cloue le guest ici (matériel passé au travers)

%s

Ce que la migration exige vraiment, dans l'ordre où on s'y heurte :

  1. un second nœud, dans le même cluster ;
  2. soit un stockage PARTAGÉ visible des deux côtés, soit --with-local-disks
     pour recopier les volumes ;
  3. aucune ressource locale attachée (carte PCI, périphérique USB) ;
  4. VM.Migrate sur le guest, et l'accès au stockage des deux côtés.
%s

Endpoint : POST /api2/json/nodes/{node}/%s/{vmid}/migrate`, noun, moveHelp, ownershipHelp, kind),
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
			}
			if o.Target == "" {
				return &exitError{code: pve.ExitUsage, msg: "--target est obligatoire : vers quel nœud ?"}
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
			owner, err := newOwnership(cmd)
			if err != nil {
				return err
			}
			if o.Target == node {
				return &exitError{
					code: pve.ExitUsage,
					msg:  fmt.Sprintf("%s est déjà sur %s — une migration vers soi-même n'a pas de sens", args[0], node),
				}
			}

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:    node,
					Method:  "POST",
					Path:    pve.MigratePath(kind, node, vmid),
					Payload: o.Values(kind),
					Effect:  fmt.Sprintf("déplace %s de %s vers %s", target, node, o.Target),
					Rollback: fmt.Sprintf("pvecli %s migrate %s --target %s — une migration se refait dans l'autre sens,\n"+
						"           elle ne s'annule pas", cliGroup(kind), target, node),
					Verify: "le guest doit être vu sur " + o.Target,
				},

				// The pre-read IS the preconditions endpoint. Reading it after
				// the write would be an autopsy; reading it before is the
				// point of the story.
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					// Terraform declares node_name: a migration moves a guest
					// out from under its own state file. That makes it a
					// configuration write, not an execution-state change.
					if err := owner.check(vmid, st.Tags, opMigrate); err != nil {
						return service.State{}, err
					}
					// Then: does the destination even exist? On a single node
					// this is where the command stops, with the reason spelled
					// out rather than a 400 about an unknown parameter.
					//
					// The ownership guard comes first on purpose. « ce guest
					// appartient à Terraform » is true whether or not a target
					// exists, and it is the answer the operator has to act on.
					if err := checkMigrationTarget(ctx, client, node, o.Target); err != nil {
						return service.State{}, err
					}
					pre, err := client.MigratePreconditions(ctx, node, kind, vmid, o.Target)
					if err != nil {
						return service.State{}, err
					}
					if err := reportPreconditions(cmd, kind, o, pre); err != nil {
						return service.State{}, err
					}
					return guestState(st), nil
				},

				Write: func(ctx context.Context) (string, error) {
					return client.MigrateGuest(ctx, node, kind, vmid, o)
				},

				// The proof is the guest seen on the OTHER node, not the
				// exitstatus of the task.
				PostRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, o.Target, kind, vmid)
					if err != nil {
						return service.State{}, fmt.Errorf(
							"la tâche a réussi mais %s reste introuvable sur %s : %w", target, o.Target, err)
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s est maintenant sur %s\n", target, o.Target)
					return guestState(st), nil
				},
			})
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"vmid", target}, {"nœud", o.Target}, {"statut", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.StringVar(&o.Target, "target", "", "nœud de destination")
	f.StringVar(&o.TargetStorage, "target-storage", "", "stockage d'arrivée (ou « 1 » pour garder le même nom)")
	f.IntVar(&o.BWLimit, "bwlimit", 0, "plafond de bande passante, en Kio/s")
	if kind == pve.TypeLXC {
		f.BoolVar(&o.Restart, "restart", false, "arrête, déplace et redémarre le conteneur")
		f.IntVar(&o.Timeout, "timeout", 0, "secondes accordées à l'arrêt")
	} else {
		f.BoolVar(&o.Online, "online", false, "migration à chaud, sans arrêter la VM")
		f.BoolVar(&o.WithLocalDisks, "with-local-disks", false, "recopie les disques que la cible ne voit pas")
	}
	addWriteFlags(c)
	addOwnershipFlag(c)
	addRenderFlags(c)
	return c
}

// checkMigrationTarget refuses before any write when the destination cannot
// exist, and — on the single-node lab this project runs against — explains WHY
// rather than letting the node answer something obscure.
func checkMigrationTarget(ctx context.Context, client *pve.Client, node, target string) error {
	nodes, err := client.Nodes(ctx)
	if err != nil {
		return err
	}

	others := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Node == target {
			if n.Status != "online" {
				return fmt.Errorf("le nœud %q est %s — une migration exige une cible en ligne", target, n.Status)
			}
			return nil
		}
		if n.Node != node {
			others = append(others, n.Node)
		}
	}

	if len(nodes) <= 1 {
		return &exitError{
			code: pve.ExitUsage,
			msg: fmt.Sprintf(""+
				"migration impossible : %s est le SEUL nœud de ce cluster.\n\n"+
				"Une migration déplace un guest d'un nœud vers un autre. Il n'y a pas\n"+
				"d'autre nœud ici, donc il n'y a nulle part où aller — ce n'est pas un\n"+
				"droit qui manque ni un paramètre à corriger.\n\n"+
				"Ce qu'il faudrait, dans l'ordre :\n"+
				"  1. un second nœud, joint au même cluster (« pvecm add ») ;\n"+
				"  2. un stockage partagé visible des deux côtés — sinon chaque disque\n"+
				"     local devra être recopié (--with-local-disks) ;\n"+
				"  3. de quoi relire l'état des deux côtés :  pvecli cluster status\n\n"+
				"La commande reste ici, prête, pour le jour où le second nœud arrive.",
				node),
		}
	}

	if len(others) == 0 {
		return fmt.Errorf("le nœud %q n'existe pas dans ce cluster", target)
	}
	return fmt.Errorf("le nœud %q n'existe pas dans ce cluster. Les cibles possibles :\n  %s",
		target, strings.Join(others, "\n  "))
}

// reportPreconditions turns the node's answer into the sentence an operator
// needs, and refuses the migrations that would fail anyway.
func reportPreconditions(cmd *cobra.Command, kind pve.GuestType, o pve.MigrateOptions, pre *pve.MigratePrecheck) error {
	err := cmd.ErrOrStderr()

	if reason, refused := pre.NotAllowedNode[o.Target]; refused {
		return fmt.Errorf("le nœud %s refuse ce guest : %s", o.Target, reason)
	}

	if len(pre.LocalResources) > 0 {
		return fmt.Errorf(
			"ce guest est cloué sur ce nœud par des ressources locales :\n  %s\n"+
				"Un périphérique physique ne suit pas la migration. Il faut le détacher d'abord",
			strings.Join(pre.LocalResources, "\n  "))
	}

	disks := pre.MovableDisks()
	if len(disks) > 0 {
		total := int64(0)
		names := make([]string, 0, len(disks))
		for _, d := range disks {
			total += d.Size
			names = append(names, fmt.Sprintf("%s (%s, %s)", d.VolID, d.DriveName, output.Bytes(d.Size)))
		}
		_, _ = fmt.Fprintf(err,
			"%d disque(s) local(aux), %s à recopier — le nœud cible ne voit pas ces volumes :\n  %s\n",
			len(disks), output.Bytes(total), strings.Join(names, "\n  "))

		if kind == pve.TypeQEMU && !o.WithLocalDisks {
			return fmt.Errorf(
				"ces disques ne suivront pas sans --with-local-disks.\n" +
					"Le nœud refuserait la migration ; la refuser ici évite d'attendre pour l'apprendre")
		}
	}

	if pre.Running {
		switch {
		case kind == pve.TypeLXC && !o.Restart:
			return fmt.Errorf(
				"%s tourne, et un conteneur ne migre pas à chaud.\n"+
					"  --restart  l'arrête, le déplace et le redémarre — c'est une interruption de service",
				"le conteneur")
		case kind == pve.TypeQEMU && !o.Online:
			_, _ = fmt.Fprintln(err,
				"la VM tourne et --online est absent : elle sera arrêtée pendant la migration.")
		}
	}

	if len(pre.AllowedNodes) > 0 {
		_, _ = fmt.Fprintf(err, "nœuds acceptés par le nœud source : %s\n",
			strings.Join(pre.AllowedNodes, ", "))
	}
	return nil
}
