package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dev-toolings/pvecli/internal/output"
	"github.com/spf13/cobra"
)

func newStorageCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "storage",
		Short: "Explore les stockages et leur contenu",
		Long: `Liste les stockages et ce qu'ils contiennent.

La colonne CONTENT est la plus importante : c'est une contrainte de l'API, pas
une convention de nommage. Un disque de VM ne peut aller que sur un stockage
déclarant « images », un ISO que sur un « iso ». Le lab illustre exactement
cette séparation : local accepte iso/vztmpl/backup/import, local-lvm accepte
images/rootdir, et aucun des deux n'accepte les deux familles.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(
		newStorageListCmd(), newStorageContentCmd(),
		newStorageDownloadCmd(), newStorageUploadCmd(), newStorageRemoveCmd(),
		newStorageDefCmd(),
	)
	return c
}

func newStorageListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les stockages (GET /nodes/{node}/storage)",
		Args:    usage(cobra.NoArgs),
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

			stores, err := client.Storages(cmd.Context(), node)
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"NOM", "TYPE", "CONTENT", "ACTIF", "UTILISÉ", "TOTAL", "LIBRE"}}
			for _, s := range stores {
				rows.Cells = append(rows.Cells, []string{
					s.Storage, s.Type, s.Content, yesNo(s.Active == 1),
					output.Bytes(s.Used), output.Bytes(s.Total), output.Bytes(s.Avail),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, stores, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newStorageContentCmd() *cobra.Command {
	var content string

	c := &cobra.Command{
		Use:   "content <storage>",
		Short: "Liste le contenu d'un stockage",
		Long: `Liste les volumes d'un stockage.

Le champ VOLID est l'identifiant que tous les autres endpoints attendent — de
la forme « local:iso/debian.iso », pas un chemin de système de fichiers.
Confondre les deux est la première erreur de la story de création (PVX-025).

Endpoint : GET /api2/json/nodes/{node}/storage/{storage}/content`,
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
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			vols, err := client.StorageContent(cmd.Context(), node, args[0], content)
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"VOLID", "CONTENT", "FORMAT", "TAILLE", "VMID"}}
			for _, v := range vols {
				vmid := "—"
				if v.VMID != 0 {
					vmid = strconv.Itoa(v.VMID)
				}
				rows.Cells = append(rows.Cells, []string{
					v.VolID, v.Content, v.Format, output.Bytes(v.Size), vmid,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, vols, rows)
		},
	}

	c.Flags().StringVar(&content, "content", "",
		"filtre par type : iso, vztmpl, backup, images, rootdir, snippets")
	addRenderFlags(c)
	return c
}

func newClusterCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "cluster",
		Short: "Vue transversale du cluster",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newClusterStatusCmd(), newClusterResourcesCmd())
	return c
}

func newClusterStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "État du cluster (GET /cluster/status)",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			entries, err := client.ClusterStatus(cmd.Context())
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"NOM", "TYPE", "IP", "EN LIGNE", "LOCAL"}}
			for _, e := range entries {
				rows.Cells = append(rows.Cells, []string{
					e.Name, e.Type, e.IP, yesNo(e.Online == 1), yesNo(e.Local == 1),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, entries, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newClusterResourcesCmd() *cobra.Command {
	var kind string

	c := &cobra.Command{
		Use:   "resources",
		Short: "Inventaire global (GET /cluster/resources)",
		Long: `Inventaire transversal en un seul appel.

Savoir choisir l'endpoint agrégé est une compétence à part entière : un
« /cluster/resources » remplace souvent N appels « /nodes/{n}/… ». C'est le
socle que réutiliseront la complétion dynamique (PVX-053) et « iac drift »
(PVX-044).`,
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
			res, err := client.Resources(cmd.Context(), kind)
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"ID", "TYPE", "NOM", "NŒUD", "STATUT", "VMID", "TAGS"}}
			for _, r := range res {
				vmid := "—"
				if r.VMID != 0 {
					vmid = strconv.Itoa(r.VMID)
				}
				rows.Cells = append(rows.Cells, []string{
					r.ID, r.Type, firstNonEmpty(r.Name, r.Storage), r.Node, r.Status, vmid,
					strings.ReplaceAll(r.Tags, ";", ","),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts, res, rows)
		},
	}

	c.Flags().StringVar(&kind, "type", "", "filtre : vm, storage, node, sdn")
	addRenderFlags(c)
	return c
}

func newTaskCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "task",
		Short: "Consulte les tâches du nœud et leurs journaux",
		Long: `Liste, décrit et lit le journal des tâches.

L'interface web n'est qu'un client de la même API : chacun de tes clics laisse
une tâche que tu peux relire ici. C'est le meilleur moyen de découvrir quel
endpoint appeler pour reproduire une action de l'UI.`,
		Args: usage(cobra.NoArgs),
	}
	c.AddCommand(newTaskListCmd(), newTaskShowCmd(), newTaskLogCmd(), newTaskWaitCmd())
	return c
}

func newTaskListCmd() *cobra.Command {
	var (
		running bool
		limit   int
	)

	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste les tâches (GET /nodes/{node}/tasks)",
		Args:    usage(cobra.NoArgs),
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

			tasks, err := client.Tasks(cmd.Context(), node, running, limit)
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"UPID", "TYPE", "CIBLE", "UTILISATEUR", "DÉBUT", "DURÉE", "STATUT"}}
			for _, t := range tasks {
				duration := "en cours"
				if t.EndTime > 0 {
					duration = output.Uptime(t.EndTime - t.StartTime)
					if t.EndTime == t.StartTime {
						duration = "<1m"
					}
				}
				rows.Cells = append(rows.Cells, []string{
					shortUPID(t.UPID), t.Type, firstNonEmpty(t.ID, "—"), t.User,
					output.Timestamp(t.StartTime), duration,
					firstNonEmpty(t.ExitStatus, t.Status, "—"),
				})
			}
			// The table truncates the UPID; -o json always carries it whole,
			// because that is the form every other endpoint needs.
			return output.Render(cmd.OutOrStdout(), opts, tasks, rows)
		},
	}

	c.Flags().BoolVar(&running, "running", false, "ne garde que les tâches actives")
	c.Flags().IntVar(&limit, "limit", 25, "nombre maximum de tâches")
	addRenderFlags(c)
	return c
}

// shortUPID keeps the discriminating parts of a UPID for a table: its type and
// its target. The whole thing is 90 characters of mostly hexadecimal.
func shortUPID(upid string) string {
	parts := strings.Split(upid, ":")
	if len(parts) < 7 {
		return upid
	}
	return fmt.Sprintf("…%s:%s:%s", parts[2], parts[5], parts[6])
}

func newTaskShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show <upid>",
		Short: "État d'une tâche (GET /nodes/{node}/tasks/{upid}/status)",
		Args:  usage(cobra.ExactArgs(1)),
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

			t, err := client.TaskStatus(cmd.Context(), node, args[0])
			if err != nil {
				return err
			}

			rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}}
			for _, kv := range [][2]string{
				{"upid", t.UPID}, {"type", t.Type}, {"cible", t.ID}, {"utilisateur", t.User},
				{"statut", t.Status}, {"exitstatus", t.ExitStatus},
				{"début", output.Timestamp(t.StartTime)},
			} {
				if kv[1] != "" {
					rows.Cells = append(rows.Cells, []string{kv[0], kv[1]})
				}
			}
			return output.Render(cmd.OutOrStdout(), opts, t, rows)
		},
	}
	addRenderFlags(c)
	return c
}

func newTaskLogCmd() *cobra.Command {
	var tail int

	c := &cobra.Command{
		Use:   "log <upid>",
		Short: "Journal d'une tâche (GET /nodes/{node}/tasks/{upid}/log)",
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			lines, err := client.TaskLog(cmd.Context(), node, args[0], tail)
			if err != nil {
				return err
			}
			for _, l := range lines {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), l.Text)
			}
			return nil
		},
	}

	c.Flags().IntVar(&tail, "tail", 0, "ne montre que les N dernières lignes")
	return c
}
