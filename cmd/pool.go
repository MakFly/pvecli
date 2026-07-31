package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/MakFly/pvecli/internal/service"
	"github.com/spf13/cobra"
)

func newPoolCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "pool",
		Short: "Regroupe des ressources — et simplifie les ACL",
		Long: `Crée des pools de ressources et gère leurs membres.

Un pool ressemble à un dossier de rangement. Ce n'en est pas un : c'est un
CHEMIN D'AUTORISATION. Chaque pool existe aussi comme « /pool/<id> » dans le
modèle d'ACL, et un rôle attribué là couvre tous ses membres, présents et à
venir :

  pvecli pool create prod
  pvecli pool add prod --vmid 210,211
  pvecli access acl set --path /pool/prod --role PVEVMAdmin --token …

Les deux VM sont couvertes, et celle qu'on ajoutera demain le sera aussi sans
retoucher l'ACL. C'est la seule raison de créer un pool ; le regroupement
visuel dans l'interface web n'en est qu'un effet.

Une VM n'appartient qu'à un seul pool à la fois. « pool add » sur une VM déjà
placée est refusé, sauf --allow-move : PVE préfère un refus à un déplacement
silencieux de ce que quelqu'un d'autre avait rangé.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(
		newPoolListCmd(), newPoolShowCmd(), newPoolCreateCmd(),
		newPoolRemoveCmd(), newPoolAddCmd(), newPoolTakeOutCmd(),
	)
	return c
}

func newPoolListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les pools (GET /pools)",
		Long: `Liste les pools sur lesquels tu as Pool.Audit.

Un pool auquel tu n'as pas accès n'apparaît pas : la liste est filtrée par tes
droits, elle n'est pas l'inventaire complet du nœud. Un pool « manquant » est
donc d'abord une question d'ACL.

Endpoint : GET /api2/json/pools`,
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

			pools, err := client.Pools(cmd.Context())
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"POOL", "CHEMIN ACL", "COMMENTAIRE"}}
			for _, p := range pools {
				rows.Cells = append(rows.Cells, []string{
					p.PoolID, pve.PoolACLPath(p.PoolID), firstNonEmpty(p.Comment, "—"),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, pools, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newPoolShowCmd() *cobra.Command {
	var kind string

	c := &cobra.Command{
		Use:   "show <poolid>",
		Short: "Liste les membres d'un pool (GET /pools?poolid=…)",
		Long: `Détaille un pool et ce qu'il contient.

L'identifiant du pool voyage en PARAMÈTRE, pas en segment de chemin. C'est la
forme de PVE 9 : « GET /pools/{poolid} » existe encore mais le nœud la déclare
lui-même dépréciée, « no support for nested pools ». Un pool imbriqué s'appelle
« parent/enfant » — précisément ce qu'un segment d'URL ne peut pas porter.

Endpoint : GET /api2/json/pools?poolid={poolid}`,
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

			pool, err := client.Pool(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"MEMBRE", "TYPE", "NŒUD", "STATUT"}}
			for _, m := range pool.Members {
				if kind != "" && m.Type != kind {
					continue
				}
				rows.Cells = append(rows.Cells, []string{
					m.Label(), m.Type, firstNonEmpty(m.Node, "—"), firstNonEmpty(m.Status, "—"),
				})
			}

			if err := output.Render(cmd.OutOrStdout(), opts, pool, rows); err != nil {
				return err
			}
			if opts.Format == output.Table {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nChemin ACL : %s — un rôle attribué là couvre les %d membre(s) ci-dessus.\n",
					pve.PoolACLPath(pool.PoolID), len(pool.Members))
			}
			return nil
		},
	}

	c.Flags().StringVar(&kind, "type", "", "filtre les membres : qemu, lxc, storage")
	addRenderFlags(c)
	return c
}

func newPoolCreateCmd() *cobra.Command {
	var comment string

	c := &cobra.Command{
		Use:   "create <poolid>",
		Short: "Crée un pool vide (POST /pools)",
		Long: `Crée un pool. Il naît vide : les membres s'ajoutent avec « pool add ».

Écriture synchrone — pas d'UPID, rien à suivre. La preuve est la relecture.

Endpoint : POST /api2/json/pools`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			poolid := args[0]
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
				Target: poolid,
				Plan: service.Plan{
					Method:  "POST",
					Path:    pve.PoolPath(),
					Payload: pve.PoolCreateValues(poolid, comment),
					Effect: fmt.Sprintf("crée le pool %s, et avec lui le chemin d'ACL %s",
						poolid, pve.PoolACLPath(poolid)),
					Rollback: "pvecli pool rm " + poolid,
					Verify:   "relecture de GET /pools?poolid=" + poolid,
				},

				// The pre-read of a creation is the opposite question: does it
				// already exist? Answering « oui » here beats a 500 that says
				// « pool already exists » in Perl.
				PreRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.Pool(ctx, poolid); err == nil {
						return service.State{}, fmt.Errorf("le pool %s existe déjà", poolid)
					}
					return service.State{Exists: true, Status: "absent"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.CreatePool(ctx, poolid, comment)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					pool, err := client.Pool(ctx, poolid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{
						Exists: true, Status: "créé",
						Summary: fmt.Sprintf("pool %s — %d membre(s)", pool.PoolID, len(pool.Members)),
						Raw:     pool,
					}, nil
				},
			})
			if err != nil {
				return err
			}
			return renderPoolState(cmd, opts, result)
		},
	}

	c.Flags().StringVar(&comment, "comment", "", "description du pool")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func newPoolRemoveCmd() *cobra.Command {
	var force bool

	c := &cobra.Command{
		Use:     "rm <poolid>",
		Aliases: []string{"delete"},
		Short:   "Supprime un pool vide (DELETE /pools?poolid=…)",
		Long: `Supprime un pool. Le nœud n'accepte que les pools VIDES.

L'API n'a pas de « force » : delete_pool refuse tant qu'il reste une VM, un
stockage ou un sous-pool (PVE/API2/Pool.pm). Le --force de cette commande n'est
donc pas un drapeau passé au nœud, ce sont DEUX requêtes — sortir les membres,
puis supprimer. Le plan les affiche toutes les deux, parce que la première est
une écriture à part entière.

Supprimer un pool supprime aussi les ACL posées sur son chemin
(PVE::AccessControl::delete_pool_acl) : les droits qu'il portait disparaissent
avec lui.

Endpoint : DELETE /api2/json/pools?poolid={poolid}`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			poolid := args[0]
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			var members []pve.PoolMember

			plan := service.Plan{
				Method: "DELETE",
				Path:   pve.PoolPath() + "?poolid=" + poolid,
				Effect: fmt.Sprintf("supprime le pool %s et les ACL posées sur %s", poolid, pve.PoolACLPath(poolid)),
				Verify: "relecture de GET /pools",
				Rollback: "pvecli pool create " + poolid +
					" — mais les ACL qu'il portait sont à reposer une par une",
			}

			runner := newRunner(cmd, client)
			_, err = runner.Run(cmd.Context(), service.Mutation{
				Target:      poolid,
				Destructive: true,
				Plan:        plan,

				PreRead: func(ctx context.Context) (service.State, error) {
					pool, err := client.Pool(ctx, poolid)
					if err != nil {
						return service.State{}, err
					}
					members = pool.Members

					if len(members) > 0 && !force {
						return service.State{}, fmt.Errorf(
							"le pool %s n'est pas vide — %s.\n"+
								"Le nœud refuse de supprimer un pool peuplé.\n"+
								"  · sortir les membres puis supprimer :  pvecli pool rm %s --force\n"+
								"  · les sortir seulement :               pvecli pool remove %s --vmid …",
							poolid, memberList(members), poolid, poolid)
					}
					if len(members) > 0 {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"--force : %s seront d'abord sortis du pool par\n  PUT %s (delete=1)\n",
							memberList(members), pve.PoolPath())
					}
					return service.State{
						Exists: true, Status: "présent",
						Summary: fmt.Sprintf("pool %s — %d membre(s)", poolid, len(members)),
					}, nil
				},

				Write: func(ctx context.Context) (string, error) {
					if len(members) > 0 {
						if err := client.UpdatePool(ctx, emptyingChange(poolid, members)); err != nil {
							return "", err
						}
					}
					return "", client.DeletePool(ctx, poolid)
				},

				PostRead: func(ctx context.Context) (service.State, error) {
					if _, err := client.Pool(ctx, poolid); err == nil {
						return service.State{}, fmt.Errorf("le pool %s est toujours là après la suppression", poolid)
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "pool %s supprimé\n", poolid)
					return service.State{Exists: true, Status: "supprimé"}, nil
				},
			})
			return err
		},
	}

	c.Flags().BoolVar(&force, "force", false, "sort d'abord les membres du pool, puis le supprime")
	addWriteFlags(c)
	return c
}

// emptyingChange builds the update that takes every member out of a pool.
func emptyingChange(poolid string, members []pve.PoolMember) pve.PoolChange {
	change := pve.PoolChange{PoolID: poolid, Delete: true}
	for _, m := range members {
		if m.VMID != 0 {
			change.VMs = append(change.VMs, m.VMID)
			continue
		}
		if m.Storage != "" {
			change.Storage = append(change.Storage, m.Storage)
		}
	}
	return change
}

func memberList(members []pve.PoolMember) string {
	labels := make([]string, len(members))
	for i, m := range members {
		labels[i] = m.Label()
	}
	return strings.Join(labels, ", ")
}

func newPoolAddCmd() *cobra.Command     { return newPoolMemberCmd(false) }
func newPoolTakeOutCmd() *cobra.Command { return newPoolMemberCmd(true) }

// newPoolMemberCmd builds `pool add` and `pool remove`: the same PUT, with
// delete=1 or without it.
func newPoolMemberCmd(remove bool) *cobra.Command {
	var (
		vmids     []int
		storages  []string
		allowMove bool
	)

	use, short := "add <poolid>", "Ajoute des membres à un pool (PUT /pools)"
	long := `Ajoute des VM, des conteneurs ou des stockages à un pool.

Une VM n'est que dans un pool à la fois. Si elle est déjà rangée ailleurs, le
nœud refuse — sauf --allow-move, qui la sort de son pool actuel. Le refus est
volontaire : déplacer sans le dire casserait l'ACL de quelqu'un d'autre.

Endpoint : PUT /api2/json/pools`
	if remove {
		use, short = "remove <poolid>", "Sort des membres d'un pool (PUT /pools, delete=1)"
		long = `Sort des membres d'un pool sans les détruire.

Le guest continue d'exister ; il perd seulement les droits que le chemin
« /pool/<id> » lui donnait. C'est une modification d'AUTORISATION, pas une
modification d'infrastructure — et c'est le sens de cette commande.

Endpoint : PUT /api2/json/pools (delete=1)`
	}

	c := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			poolid := args[0]
			if len(vmids) == 0 && len(storages) == 0 {
				return &exitError{code: pve.ExitUsage, msg: "rien à faire : passe --vmid et/ou --storage"}
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}

			change := pve.PoolChange{
				PoolID: poolid, VMs: vmids, Storage: storages,
				Delete: remove, AllowMove: allowMove,
			}

			verb, reverse := "ajoute", "pvecli pool remove "+poolid
			if remove {
				verb, reverse = "sort", "pvecli pool add "+poolid
			}

			runner := newRunner(cmd, client)
			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: poolid,
				Plan: service.Plan{
					Method:  "PUT",
					Path:    pve.PoolPath(),
					Payload: change.Values(),
					Effect: fmt.Sprintf("%s %s %s pool %s (chemin ACL %s)",
						verb, describeMembers(vmids, storages), preposition(remove), poolid,
						pve.PoolACLPath(poolid)),
					Rollback: reverse + " " + reverseFlags(vmids, storages),
					Verify:   "relecture de GET /pools?poolid=" + poolid,
				},

				PreRead: func(ctx context.Context) (service.State, error) {
					pool, err := client.Pool(ctx, poolid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{
						Exists: true, Status: "présent",
						Summary: fmt.Sprintf("pool %s — %d membre(s) avant", poolid, len(pool.Members)),
					}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return "", client.UpdatePool(ctx, change)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					pool, err := client.Pool(ctx, poolid)
					if err != nil {
						return service.State{}, err
					}
					return service.State{
						Exists: true, Status: "à jour",
						Summary: fmt.Sprintf("pool %s — %d membre(s)", poolid, len(pool.Members)),
						Raw:     pool,
					}, nil
				},
			})
			if err != nil {
				return err
			}
			return renderPoolState(cmd, opts, result)
		},
	}

	f := c.Flags()
	f.IntSliceVar(&vmids, "vmid", nil, "VMID ou CTID, séparés par des virgules")
	f.StringSliceVar(&storages, "storage", nil, "identifiants de stockage")
	if !remove {
		f.BoolVar(&allowMove, "allow-move", false, "sort le guest de son pool actuel au lieu de refuser")
	}
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func preposition(remove bool) string {
	if remove {
		return "du"
	}
	return "au"
}

func describeMembers(vmids []int, storages []string) string {
	parts := make([]string, 0, 2)
	if len(vmids) > 0 {
		ids := make([]string, len(vmids))
		for i, v := range vmids {
			ids[i] = strconv.Itoa(v)
		}
		parts = append(parts, "guest "+strings.Join(ids, ","))
	}
	if len(storages) > 0 {
		parts = append(parts, "stockage "+strings.Join(storages, ","))
	}
	return strings.Join(parts, " et ")
}

func reverseFlags(vmids []int, storages []string) string {
	parts := make([]string, 0, 2)
	if len(vmids) > 0 {
		ids := make([]string, len(vmids))
		for i, v := range vmids {
			ids[i] = strconv.Itoa(v)
		}
		parts = append(parts, "--vmid "+strings.Join(ids, ","))
	}
	if len(storages) > 0 {
		parts = append(parts, "--storage "+strings.Join(storages, ","))
	}
	return strings.Join(parts, " ")
}

// renderPoolState prints the post-read of a pool mutation: the members as the
// node reports them, never an echo of the request.
func renderPoolState(cmd *cobra.Command, opts output.Options, result *service.State) error {
	pool, ok := result.Raw.(*pve.Pool)
	if !ok {
		return nil
	}
	rows := output.Rows{Headers: []string{"MEMBRE", "TYPE", "NŒUD", "STATUT"}}
	for _, m := range pool.Members {
		rows.Cells = append(rows.Cells, []string{
			m.Label(), m.Type, firstNonEmpty(m.Node, "—"), firstNonEmpty(m.Status, "—"),
		})
	}
	return output.Render(cmd.OutOrStdout(), opts, pool, rows)
}
