package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/service"
	"github.com/spf13/cobra"
)

func newVMCloneCmd() *cobra.Command {
	var (
		o     pve.CloneOptions
		start bool
	)

	c := &cobra.Command{
		Use:   "clone <source-vmid>",
		Short: "Clone une VM ou un template (POST /nodes/{node}/qemu/{vmid}/clone)",
		Long: `Produit une nouvelle VM à partir d'une VM existante ou d'un template.

  --full   copie indépendante : les disques du clone lui appartiennent.
  (défaut) clone LIÉ : le clone partage les blocs de la source et ne stocke
           que ses différences. Il se crée en une seconde et ne coûte presque
           rien — mais détruire la source casse tous ses clones liés.

L'interface web masque cette contrainte ; elle est la raison d'être du drapeau.
Pour un template destiné à être cloné souvent, le clone lié est le bon choix ;
pour une VM qu'on veut pouvoir déplacer ou sauvegarder seule, --full.

La garde de propriété porte ici sur la SOURCE, pas sur le clone : le clone
n'existe pas encore, donc rien ne le possède. Cloner une VM gérée par Terraform
produit une ressource que le state ne connaît pas — et, en clone lié, épingle
les disques de la source, que Terraform se croit libre de détruire.

Endpoint : POST /api2/json/nodes/{node}/qemu/{vmid}/clone`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid source invalide : %q", args[0])}
			}
			if o.NewID == 0 {
				return &exitError{code: pve.ExitUsage, msg: "--newid est obligatoire"}
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

			params := o.Values(pve.TypeQEMU)
			target := strconv.Itoa(o.NewID)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target: target,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.ClonePath(pve.TypeQEMU, node, src),
					Payload:  params,
					Effect:   fmt.Sprintf("VM %d créée depuis %d (%s)", o.NewID, src, cloneKind(o.Full)),
					Rollback: fmt.Sprintf("pvecli vm rm %d", o.NewID),
					Verify:   fmt.Sprintf("relecture de la configuration de %d", o.NewID),
				},
				// Two conditions, both checked before anything is written: the
				// source must exist, and the destination vmid must be free.
				PreRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeQEMU, src)
					if err != nil {
						return service.State{}, fmt.Errorf("source %d illisible : %w", src, err)
					}
					if err := owner.check(src, cfg.String("tags"), opCloneSource); err != nil {
						return service.State{}, err
					}
					if _, err := client.GuestStatus(ctx, node, pve.TypeQEMU, o.NewID); err == nil {
						return service.State{}, fmt.Errorf("le vmid %d est déjà pris", o.NewID)
					}
					return service.State{Exists: true, Status: "destination libre"}, nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.CloneGuest(ctx, node, pve.TypeQEMU, src, o)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeQEMU, o.NewID)
					if err != nil {
						return service.State{}, err
					}
					return service.State{Exists: true, Status: "cloné", Raw: cfg}, nil
				},
			})
			if err != nil {
				return err
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if !dryRun && start {
				if _, err := client.SetGuestStatus(cmd.Context(), node, pve.TypeQEMU, o.NewID, pve.ActionStart, nil); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "démarrage demandé pour %d\n", o.NewID)
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"source", args[0]}, {"nouveau vmid", target},
				{"type", cloneKind(o.Full)}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}

	f := c.Flags()
	f.IntVar(&o.NewID, "newid", 0, "vmid de la nouvelle VM (obligatoire)")
	f.StringVar(&o.Name, "name", "", "nom de la nouvelle VM")
	f.StringVar(&o.Description, "description", "", "description")
	f.StringVar(&o.Pool, "pool", "", "pool de rattachement")
	f.StringVar(&o.Storage, "storage", "", "stockage cible (clone complet uniquement)")
	f.StringVar(&o.Target, "target", "", "nœud cible")
	f.BoolVar(&o.Full, "full", false, "clone complet plutôt que lié")
	f.BoolVar(&start, "start", false, "démarre le clone après création")
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

func cloneKind(full bool) string {
	if full {
		return "clone complet"
	}
	return "clone lié"
}

func newVMTemplateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "template <vmid>",
		Short: "Convertit une VM en template (POST /nodes/{node}/qemu/{vmid}/template)",
		Long: `Fige une VM préparée en modèle clonable.

IRRÉVERSIBLE. Une VM convertie ne peut plus démarrer telle quelle : ses disques
deviennent des images de base en lecture seule. Il n'existe aucune commande
inverse — le seul retour en arrière est de cloner le template et de travailler
sur le clone.

C'est pour cela que la confirmation est renforcée alors qu'il ne s'agit pas
d'une suppression : le niveau de confirmation suit la réversibilité de
l'opération, pas la brutalité du verbe.

La VM doit être arrêtée.
` + ownershipHelp + `

Endpoint : POST /api2/json/nodes/{node}/qemu/{vmid}/template`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmid, err := strconv.Atoi(args[0])
			if err != nil {
				return &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("vmid invalide : %q", args[0])}
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

			target := strconv.Itoa(vmid)
			runner := newRunner(cmd, client)

			result, err := runner.Run(cmd.Context(), service.Mutation{
				Target:      target,
				Destructive: true,
				Plan: service.Plan{
					Node:     node,
					Method:   "POST",
					Path:     pve.TemplatePath(node, vmid),
					Payload:  url.Values{},
					Effect:   fmt.Sprintf("la VM %d devient un template et ne pourra plus démarrer", vmid),
					Rollback: "aucun — cloner le template et travailler sur le clone",
					Verify:   fmt.Sprintf("template = 1 dans la configuration de %d", vmid),
				},
				PreRead: func(ctx context.Context) (service.State, error) {
					st, err := client.GuestStatus(ctx, node, pve.TypeQEMU, vmid)
					if err != nil {
						return service.State{}, err
					}
					if err := owner.check(vmid, st.Tags, opTemplate); err != nil {
						return service.State{}, err
					}
					if st.Status == "running" {
						return service.State{}, fmt.Errorf("la VM %d tourne — arrête-la d'abord :\n  pvecli vm shutdown %d", vmid, vmid)
					}
					return guestState(st), nil
				},
				Write: func(ctx context.Context) (string, error) {
					return client.TemplateVM(ctx, node, vmid)
				},
				// The proof is the flag itself: a template is a VM carrying
				// template=1, so the post-read has something precise to check.
				PostRead: func(ctx context.Context) (service.State, error) {
					cfg, err := client.GuestConfig(ctx, node, pve.TypeQEMU, vmid)
					if err != nil {
						return service.State{}, err
					}
					if cfg.String("template") != "1" {
						return service.State{}, fmt.Errorf("la conversion n'a pas pris : template = %q", cfg.String("template"))
					}
					return service.State{Exists: true, Status: "template", Raw: cfg}, nil
				},
			})
			if err != nil {
				return err
			}

			// The row reports what the post-read found, not what the command
			// intended: under --dry-run the pipeline returns the pre-read, and
			// printing "template 1" there would claim a conversion that never
			// happened.
			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
				{"vmid", target}, {"état", result.Status},
			}}
			return output.Render(cmd.OutOrStdout(), opts, result.Raw, rows)
		},
	}
	addOwnershipFlag(c)
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}
