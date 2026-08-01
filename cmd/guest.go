package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MakFly/pvecli/internal/output"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

func newVMCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "vm",
		Short: "Inspecte les machines virtuelles QEMU",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newGuestListCmd(pve.TypeQEMU), newGuestShowCmd(pve.TypeQEMU))
	c.AddCommand(statusCommands(pve.TypeQEMU)...)
	c.AddCommand(newVMCreateCmd(), newGuestRemoveCmd(pve.TypeQEMU), newVMSetCmd())
	c.AddCommand(newVMCloneCmd(), newVMTemplateCmd())
	c.AddCommand(newSnapshotCmd(pve.TypeQEMU), newVMAgentCmd(), newVMIPCmd())
	c.AddCommand(newMigrateCmd(pve.TypeQEMU))
	c.AddCommand(newVMDeclareCmd())
	return c
}

func newLXCCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "lxc",
		Short: "Inspecte les conteneurs LXC",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newGuestListCmd(pve.TypeLXC), newGuestShowCmd(pve.TypeLXC))
	c.AddCommand(statusCommands(pve.TypeLXC)...)
	c.AddCommand(newSnapshotCmd(pve.TypeLXC))
	c.AddCommand(newLXCCreateCmd(), newGuestRemoveCmd(pve.TypeLXC), newLXCSetCmd(), newLXCCloneCmd())
	c.AddCommand(newMigrateCmd(pve.TypeLXC))
	c.AddCommand(newLXCExecCmd())
	c.AddCommand(newLXCFirewallCmd())
	return c
}

func newGuestCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "guest",
		Short: "Vue unifiée des VM et des conteneurs",
		Args:  usage(cobra.NoArgs),
	}
	c.AddCommand(newGuestListCmd(""))
	return c
}

// newGuestListCmd serves `vm ls`, `lxc ls` and `guest ls`: the three differ
// only by which families they fetch.
func newGuestListCmd(kind pve.GuestType) *cobra.Command {
	var (
		tag string
		all bool
	)

	label := map[pve.GuestType]string{
		pve.TypeQEMU: "les machines virtuelles QEMU (GET /nodes/{node}/qemu)",
		pve.TypeLXC:  "les conteneurs LXC (GET /nodes/{node}/lxc)",
		"":           "les VM et les conteneurs, fusionnés",
	}[kind]

	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "Liste " + label,
		Long: `Liste ` + label + `.

Un template est une VM portant un drapeau, pas un objet d'un autre type : la
colonne TEMPLATE existe pour qu'il ne soit jamais confondu avec une VM éteinte.
C'est cette nature qui rend le clonage possible (PVX-024).`,
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

			nodes, err := targetNodes(cmd, client, all)
			if err != nil {
				return err
			}

			// Initialised, not nil: an empty listing must serialise as [] so
			// that `pvecli vm ls -o json | jq '.[].name'` works on a lab with
			// no VMs. A nil slice encodes to null, and jq stops there.
			guests := []pve.Guest{}
			for _, node := range nodes {
				if kind == pve.TypeQEMU || kind == "" {
					vms, err := client.VMs(cmd.Context(), node)
					if err != nil {
						return err
					}
					guests = append(guests, vms...)
				}
				if kind == pve.TypeLXC || kind == "" {
					cts, err := client.Containers(cmd.Context(), node)
					if err != nil {
						return err
					}
					guests = append(guests, cts...)
				}
			}

			// Filtering client-side rather than through the API: /nodes/{n}/qemu
			// takes no tag parameter, and this is the shape the `managed` guard
			// of M6 will reuse.
			if tag != "" {
				kept := guests[:0]
				for _, g := range guests {
					if g.HasTag(tag) {
						kept = append(kept, g)
					}
				}
				guests = kept
			}

			return output.Render(cmd.OutOrStdout(), opts, guests, guestRows(guests, kind == ""))
		},
	}

	c.Flags().StringVar(&tag, "tag", "", "ne garde que les guests portant ce tag")
	c.Flags().BoolVar(&all, "all", false, "interroge tous les nœuds, pas seulement celui par défaut")
	addRenderFlags(c)
	return c
}

// targetNodes resolves which nodes to query.
func targetNodes(cmd *cobra.Command, client *pve.Client, all bool) ([]string, error) {
	if !all {
		node, err := targetNode(cmd, nil)
		if err != nil {
			return nil, err
		}
		return []string{node}, nil
	}
	nodes, err := client.Nodes(cmd.Context())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Node)
	}
	return names, nil
}

func guestRows(guests []pve.Guest, withType bool) output.Rows {
	rows := output.Rows{Headers: []string{"VMID", "NOM", "STATUT"}}
	if withType {
		rows.Headers = append(rows.Headers, "TYPE")
	}
	rows.Headers = append(rows.Headers, "CPU", "RAM", "DISQUE", "UPTIME", "TEMPLATE", "TAGS")

	for _, g := range guests {
		cells := []string{strconv.Itoa(g.VMID), g.Name, g.Status}
		if withType {
			cells = append(cells, string(g.Type))
		}
		cells = append(cells,
			fmt.Sprintf("%s (%d)", output.Ratio(g.CPU), g.CPUs),
			fmt.Sprintf("%s / %s", output.Bytes(g.Mem), output.Bytes(g.MaxMem)),
			output.Bytes(g.MaxDisk),
			output.Uptime(g.Uptime),
			yesNo(g.IsTemplate()),
			strings.Join(g.TagList(), ","),
		)
		rows.Cells = append(rows.Cells, cells)
	}
	return rows
}

func yesNo(b bool) string {
	if b {
		return "oui"
	}
	return "—"
}

func newGuestShowCmd(kind pve.GuestType) *cobra.Command {
	var raw bool

	c := &cobra.Command{
		Use:   "show <vmid>",
		Short: "Décrit un guest : configuration et état courant",
		Long: `Combine la configuration et l'état courant en une seule vue.

Les clés « à options » de PVE (« virtio0: local-lvm:vm-100-disk-0,size=20G »)
sont parsées en structures plutôt qu'affichées brutes : les afficher telles
quelles reviendrait à montrer à l'opérateur la même énigme que l'API nous a
montrée.

--raw affiche la réponse API sans interprétation.`,
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

			cfg, err := client.GuestConfig(cmd.Context(), node, kind, vmid)
			if err != nil {
				return err
			}
			st, err := client.GuestStatus(cmd.Context(), node, kind, vmid)
			if err != nil {
				return err
			}

			if raw {
				return output.Render(cmd.OutOrStdout(), opts,
					map[string]any{"config": cfg, "status": st}, output.Rows{})
			}
			return output.Render(cmd.OutOrStdout(), opts,
				map[string]any{"config": cfg, "status": st}, guestDetailRows(kind, cfg, st))
		},
	}

	c.Flags().BoolVar(&raw, "raw", false, "affiche la réponse API telle quelle")
	addRenderFlags(c)
	return c
}

func guestDetailRows(kind pve.GuestType, cfg pve.GuestConfig, st *pve.GuestStatus) output.Rows {
	rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}}
	add := func(k, v string) {
		if v != "" {
			rows.Cells = append(rows.Cells, []string{k, v})
		}
	}

	add("vmid", strconv.Itoa(st.VMID))
	add("nom", firstNonEmpty(st.Name, cfg.String("name"), cfg.String("hostname")))
	add("statut", st.Status)
	if st.QMPStatus != "" && st.QMPStatus != st.Status {
		add("qmpstatus", st.QMPStatus)
	}
	add("verrou", st.Lock)
	add("uptime", output.Uptime(st.Uptime))
	add("cpu", fmt.Sprintf("%d vcpu", st.CPUs))
	add("mémoire", fmt.Sprintf("%s / %s", output.Bytes(st.Mem), output.Bytes(st.MaxMem)))
	add("tags", strings.ReplaceAll(firstNonEmpty(st.Tags, cfg.String("tags")), ";", ", "))
	add("os", cfg.String("ostype"))

	for _, key := range append(cfg.KeysWithPrefix("scsi"), append(cfg.KeysWithPrefix("virtio"),
		append(cfg.KeysWithPrefix("sata"), append(cfg.KeysWithPrefix("ide"), cfg.KeysWithPrefix("rootfs")...)...)...)...) {
		opt := pve.ParseOptionString(cfg.String(key))
		detail := opt.Value
		if size := opt.Get("size"); size != "" {
			detail += "  taille=" + size
		}
		add("disque "+key, detail)
	}

	for _, key := range cfg.KeysWithPrefix("net") {
		opt := pve.ParseOptionString(cfg.String(key))
		model, mac := "", opt.Value
		for _, k := range opt.Keys() {
			if k == "virtio" || k == "e1000" || k == "rtl8139" || k == "vmxnet3" {
				model, mac = k, opt.Get(k)
			}
		}
		// A container interface carries neither: its model is always veth, and
		// it names itself with `name=` — reading it like a VM interface prints
		// an empty column instead of the two facts that are there.
		if model == "" && mac == "" {
			model, mac = opt.Get("name"), opt.Get("hwaddr")
		}
		detail := strings.TrimSpace(fmt.Sprintf("%s %s", model, mac))
		if br := opt.Get("bridge"); br != "" {
			detail += "  pont=" + br
		}
		if ip := opt.Get("ip"); ip != "" {
			detail += "  ip=" + ip
		}
		add("réseau "+key, detail)
	}

	// cloud-init lives in ordinary config keys; surfacing it separately is
	// what makes PVX-027 legible.
	//
	// A container has no cloud-init at all: `nameserver` and `searchdomain` are
	// plain LXC keys applied at creation. Labelling them "cloud-init" would
	// send an operator looking for a drive that is not there.
	prefix := "cloud-init "
	if kind == pve.TypeLXC {
		prefix = ""
	}
	for _, key := range []string{"ciuser", "ipconfig0", "nameserver", "searchdomain", "sshkeys"} {
		if v := cfg.String(key); v != "" {
			if key == "sshkeys" {
				v = "(présentes)"
			}
			add(prefix+key, v)
		}
	}

	return rows
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
