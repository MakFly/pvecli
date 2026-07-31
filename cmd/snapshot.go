package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/MakFly/pvectl/internal/output"
	"github.com/MakFly/pvectl/internal/pve"
	"github.com/MakFly/pvectl/internal/service"
	"github.com/spf13/cobra"
)

func newSnapshotCmd(kind pve.GuestType) *cobra.Command {
	c := &cobra.Command{
		Use:   "snapshot",
		Short: "Gère les snapshots d'un guest",
		Long: `Crée, liste, restaure et supprime des snapshots.

Un snapshot n'est PAS une sauvegarde. C'est un point de retour local, qui vit
sur le même stockage que le disque qu'il protège : si ce stockage meurt, le
snapshot meurt avec lui. Une sauvegarde (lot M5) est une copie indépendante sur
un autre stockage. Confondre les deux est une erreur de PRA classique — et elle
ne se découvre qu'au moment où l'on en a besoin.

Le snapshot est le filet à poser avant une expérimentation ; la sauvegarde est
ce qui protège des pannes.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(
		newSnapshotListCmd(kind),
		newSnapshotCreateCmd(kind),
		newSnapshotRollbackCmd(kind),
		newSnapshotRemoveCmd(kind),
	)
	return c
}

// snapshotTarget parses the vmid argument shared by every snapshot subcommand.
func snapshotTarget(cmd *cobra.Command, arg string) (*pve.Client, string, int, error) {
	vmid, err := strconv.Atoi(arg)
	if err != nil {
		return nil, "", 0, &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", arg)}
	}
	client, err := newClient(cmd)
	if err != nil {
		return nil, "", 0, err
	}
	node, err := targetNode(cmd, nil)
	if err != nil {
		return nil, "", 0, err
	}
	return client, node, vmid, nil
}

func newSnapshotListCmd(kind pve.GuestType) *cobra.Command {
	c := &cobra.Command{
		Use:     "ls <vmid>",
		Aliases: []string{"list"},
		Short:   "Liste les snapshots (GET .../snapshot)",
		Args:    usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, node, vmid, err := snapshotTarget(cmd, args[0])
			if err != nil {
				return err
			}

			snaps, err := client.Snapshots(cmd.Context(), node, kind, vmid)
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"NOM", "PARENT", "MÉMOIRE", "DATE", "DESCRIPTION"}}
			for _, s := range snaps {
				name := s.Name
				if s.IsCurrent() {
					// PVE appends a synthetic node marking where the guest is
					// in the tree. Showing it as a snapshot would invite a
					// rollback to something that does not exist.
					name = "current (état actuel, pas un snapshot)"
				}
				rows.Cells = append(rows.Cells, []string{
					name, firstNonEmpty(s.Parent, "—"), yesNo(s.VMState == 1),
					output.Timestamp(s.SnapTime), s.Description,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, snaps, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newSnapshotCreateCmd(kind pve.GuestType) *cobra.Command {
	var (
		description string
		vmstate     bool
	)

	c := &cobra.Command{
		Use:   "create <vmid> <nom>",
		Short: "Prend un snapshot (POST .../snapshot)",
		Long: `Prend un snapshot du guest.

--vmstate sauvegarde aussi la mémoire vive : le retour arrière rend alors une
VM en cours d'exécution, exactement dans l'état où elle était. C'est plus
pratique et beaucoup plus volumineux — la taille du snapshot grandit de toute
la RAM allouée.

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/snapshot`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, node, vmid, err := snapshotTarget(cmd, args[0])
			if err != nil {
				return err
			}
			name := args[1]

			payload := url.Values{"snapname": {name}}
			if description != "" {
				payload.Set("description", description)
			}
			if vmstate {
				payload.Set("vmstate", "1")
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: fmt.Sprintf("%d/%s", vmid, name),
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.SnapshotPath(kind, node, vmid, "", ""),
					Payload:  payload,
					Effect:   fmt.Sprintf("snapshot « %s » du guest %d", name, vmid),
					Rollback: fmt.Sprintf("pvectl vm snapshot rm %d %s", vmid, name),
					Verify:   "relecture de la liste des snapshots",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					snaps, err := client.Snapshots(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					for _, s := range snaps {
						if s.Name == name {
							return service.State{}, fmt.Errorf("le snapshot « %s » existe déjà sur le guest %d", name, vmid)
						}
					}
					return guestState(st), nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.CreateSnapshot(ctx, node, kind, vmid, name, description, vmstate)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					snaps, err := client.Snapshots(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					for _, s := range snaps {
						if s.Name == name {
							return service.State{Exists: true, Status: "créé", Raw: snaps}, nil
						}
					}
					return service.State{}, fmt.Errorf("le snapshot « %s » est absent après création", name)
				},
			})
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"guest", args[0]}, {"snapshot", name}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	c.Flags().StringVar(&description, "description", "", "commentaire attaché au snapshot")
	c.Flags().BoolVar(&vmstate, "vmstate", false, "sauvegarde aussi la mémoire vive")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newSnapshotRollbackCmd(kind pve.GuestType) *cobra.Command {
	c := &cobra.Command{
		Use:   "rollback <vmid> <nom>",
		Short: "Restaure un snapshot (POST .../snapshot/{nom}/rollback)",
		Long: `Ramène le guest à l'état du snapshot.

DESTRUCTIF. Tout ce qui a été écrit dans le guest DEPUIS ce snapshot est perdu,
définitivement et sans avertissement du système invité : fichiers, bases de
données, journaux. Le retour arrière ne se négocie pas avec l'OS, il remplace
ses disques.

Les snapshots pris après celui-ci ne sont pas supprimés, mais deviennent des
branches parallèles.

Un snapshot PVE contient la configuration du guest en plus de ses disques : un
rollback réécrit donc cores, mémoire et tags. C'est une écriture de
configuration déguisée en restauration — d'où la garde ci-dessous, que
« snapshot create » n'a pas.
` + ownershipHelp + `

Endpoint : POST /api2/json/nodes/{node}/{type}/{vmid}/snapshot/{nom}/rollback`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, node, vmid, err := snapshotTarget(cmd, args[0])
			if err != nil {
				return err
			}
			owner, err := newOwnership(cmd)
			if err != nil {
				return err
			}
			name := args[1]

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target:      fmt.Sprintf("%d/%s", vmid, name),
				Destructive: true,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.SnapshotPath(kind, node, vmid, name, "rollback"),
					Effect:   fmt.Sprintf("guest %d ramené à « %s » — tout écrit depuis est perdu", vmid, name),
					Rollback: "aucun — sauf si un snapshot plus récent existe",
					Verify:   "relecture de status/current",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					snaps, err := client.Snapshots(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					for _, s := range snaps {
						if s.Name == name && !s.IsCurrent() {
							st, err := client.GuestStatus(ctx, node, kind, vmid)
							if err != nil {
								return service.State{}, err
							}
							if err := owner.check(vmid, st.Tags, opRollback); err != nil {
								return service.State{}, err
							}
							return guestState(st), nil
						}
					}
					return service.State{}, fmt.Errorf("le snapshot « %s » n'existe pas sur le guest %d — pvectl vm snapshot ls %d", name, vmid, vmid)
				},
				Write: func(ctx context.Context) (string, error) {
					return client.RollbackSnapshot(ctx, node, kind, vmid, name)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					return guestState(st), nil
				},
			})
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"guest", args[0]}, {"restauré depuis", name}, {"statut", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newSnapshotRemoveCmd(kind pve.GuestType) *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <vmid> <nom>",
		Aliases: []string{"delete"},
		Short:   "Supprime un snapshot (DELETE .../snapshot/{nom})",
		Long: `Supprime un snapshot.

DESTRUCTIF : le point de retour disparaît. L'état actuel du guest n'est pas
touché — c'est la possibilité de revenir en arrière qui est détruite.

Endpoint : DELETE /api2/json/nodes/{node}/{type}/{vmid}/snapshot/{nom}`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, node, vmid, err := snapshotTarget(cmd, args[0])
			if err != nil {
				return err
			}
			name := args[1]

			runner := newRunner(cmd, client)
			_, err = runner.Run(cmd.Context(), service.Mutation{
				Target:      fmt.Sprintf("%d/%s", vmid, name),
				Destructive: true,
				Plan: service.Plan{
					Node:     node,
					Method:   "DELETE",
					Path:     pve.SnapshotPath(kind, node, vmid, name, ""),
					Effect:   fmt.Sprintf("suppression du snapshot « %s » du guest %d", name, vmid),
					Rollback: "aucun",
					Verify:   "le snapshot doit avoir disparu de la liste",
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					snaps, err := client.Snapshots(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					for _, s := range snaps {
						if s.Name == name && !s.IsCurrent() {
							return service.State{Exists: true, Status: "présent"}, nil
						}
					}
					return service.State{}, fmt.Errorf("le snapshot « %s » n'existe pas sur le guest %d", name, vmid)
				},
				Write: func(ctx context.Context) (string, error) {
					return client.DeleteSnapshot(ctx, node, kind, vmid, name)
				},
				// The proof of a deletion is an absence.
				PostRead: func(ctx context.Context) (service.State, error) {
					snaps, err := client.Snapshots(ctx, node, kind, vmid)
					if err != nil {
						return service.State{}, err
					}
					for _, s := range snaps {
						if s.Name == name {
							return service.State{}, fmt.Errorf("le snapshot « %s » est toujours là", name)
						}
					}
					return service.State{Exists: false, Status: "supprimé"}, nil
				},
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "snapshot « %s » supprimé du guest %d.\n", name, vmid)
			return nil
		},
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}
