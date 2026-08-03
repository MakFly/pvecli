package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MakFly/pvecli/internal/catalog"
	"github.com/MakFly/pvecli/internal/iac"
	"github.com/MakFly/pvecli/internal/output"
)

type lxcDeclareOpts struct {
	vmid, cores, memory, disk, template int
	ip, gateway, node                   string
	unprivileged                        bool
	with                                []string
	remove                              bool
}

func newLXCDeclareCmd() *cobra.Command {
	var o lxcDeclareOpts

	c := &cobra.Command{
		Use:   "declare <nom>",
		Short: "Déclare un conteneur LXC et ses services dans " + iac.DeclarationFile,
		Long: `Écrit, met à jour ou retire un LXC dans la déclaration que Terraform lit.

  <terraform_dir>/` + iac.DeclarationFile + `  (clé « lxcs », symétrique à « vms »)

Même mécanique que « pvecli vm declare » : cette commande n'engage rien sur le
nœud, elle écrit une intention. C'est « pvecli iac apply » qui la réalise.

Contrairement à une VM, un LXC n'a pas de template par défaut partagé : chaque
déclaration nomme le ctid à cloner avec --template, parce qu'un conteneur
cloné à partir du mauvais gabarit ne prévient personne.

  pvecli lxc declare app-01 --vmid 221 --cores 2 --memory 2048 --template 200 \
      --ip 192.168.1.221/24 --gateway 192.168.1.1 \
      --with docker,postgresql,cloudflared

--remove retire l'entrée. Le conteneur ne disparaît qu'au « iac apply »
suivant — opération destructive, confirmée en retapant le nom.`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLXCDeclare(cmd, args[0], &o)
		},
	}

	f := c.Flags()
	f.IntVar(&o.vmid, "vmid", 0, "identifiant PVE du conteneur (obligatoire à la création)")
	f.IntVar(&o.cores, "cores", 0, "nombre de cœurs")
	f.IntVar(&o.memory, "memory", 0, "mémoire en MIBIOCTETS — 8 Go s'écrit 8192")
	f.IntVar(&o.disk, "disk", 0, "taille du disque en Gio")
	f.IntVar(&o.template, "template", 0, "ctid du conteneur à cloner (obligatoire à la création)")
	f.StringVar(&o.ip, "ip", "", "adresse : dhcp, ou 192.168.1.221/24")
	f.StringVar(&o.gateway, "gateway", "", "passerelle, si --ip est statique")
	f.StringVar(&o.node, "node", "", "nœud cible (défaut : var.node_name)")
	f.BoolVar(&o.unprivileged, "unprivileged", true, "conteneur non privilégié")
	f.StringSliceVar(&o.with, "with", nil, "services du catalogue, séparés par des virgules")
	f.BoolVar(&o.remove, "remove", false, "retire le LXC de la déclaration")

	addWriteFlags(c)
	addRenderFlags(c)
	_ = c.RegisterFlagCompletionFunc("with", completeServices)
	return c
}

func runLXCDeclare(cmd *cobra.Command, name string, o *lxcDeclareOpts) error {
	eff, err := resolveConfig(cmd)
	if err != nil {
		return err
	}
	if err := iac.CheckDir("iac.terraform_dir", eff.IaC.TerraformDir); err != nil {
		return err
	}

	// PRE-READ — what the declaration says today.
	decl, err := iac.LoadDeclaration(eff.IaC.TerraformDir)
	if err != nil {
		return err
	}
	existing, exists := decl.LXCs[name]

	var before *iac.LXC
	if exists {
		copyOf := existing
		before = &copyOf
	}

	var after *iac.LXC
	if !o.remove {
		next, err := buildLXCDeclaration(cmd, name, o, before)
		if err != nil {
			return err
		}
		after = next
	} else if !exists {
		return fmt.Errorf("« %s » n'est pas dans la déclaration — rien à retirer", name)
	}

	// Same single vmid namespace as the VM side -- a container 220 and a VM 220
	// cannot coexist, and this is the last place to say so cheaply.
	if after != nil {
		if owner, kind, taken := decl.VMIDOwner(after.VMID, name, iac.KindLXC); taken {
			return vmidTaken(after.VMID, owner, kind)
		}
	}

	changes := iac.DiffLXC(before, after)
	errW := cmd.ErrOrStderr()
	path := iac.DeclarationPath(eff.IaC.TerraformDir)

	// PLAN — the field-level difference, not a paraphrase and not a text diff.
	_, _ = fmt.Fprintf(errW, "déclaration %s\n", path)
	_, _ = fmt.Fprintf(errW, "lxc         %s — %s\n", name, declareVerbLXC(before, after))
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(errW, "aucun changement : la déclaration dit déjà exactement cela.")
		return renderDeclaredLXC(cmd, name, after, path, false)
	}
	for _, ch := range changes {
		switch {
		case ch.Before == "":
			_, _ = fmt.Fprintf(errW, "  + %-12s %s\n", ch.Field, ch.After)
		case ch.After == "":
			_, _ = fmt.Fprintf(errW, "  - %-12s %s\n", ch.Field, ch.Before)
		default:
			_, _ = fmt.Fprintf(errW, "  ~ %-12s %s → %s\n", ch.Field, ch.Before, ch.After)
		}
	}
	_, _ = fmt.Fprintf(errW, "effet       la déclaration change ; le nœud, lui, ne bouge qu'au « pvecli iac apply »\n")

	// GATE.
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été écrit.")
		return renderDeclaredLXC(cmd, name, after, path, false)
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := (cliGate{cmd: cmd, yes: yes}).Allow(name, o.remove); err != nil {
		return err
	}

	// WRITE.
	if o.remove {
		delete(decl.LXCs, name)
	} else {
		decl.LXCs[name] = *after
	}
	if err := decl.Save(eff.IaC.TerraformDir); err != nil {
		return err
	}

	// POST-READ — re-read from disk.
	reread, err := iac.LoadDeclaration(eff.IaC.TerraformDir)
	if err != nil {
		return err
	}
	written, stillThere := reread.LXCs[name]
	if o.remove && stillThere {
		return fmt.Errorf("« %s » est toujours dans %s après écriture", name, path)
	}
	if !o.remove && !stillThere {
		return fmt.Errorf("« %s » est absent de %s après écriture", name, path)
	}

	if o.remove {
		_, _ = fmt.Fprintf(errW, "\nRetiré de la déclaration. Terraform le détruira au prochain :\n  pvecli iac plan && pvecli iac apply\n")
		return renderDeclaredLXC(cmd, name, nil, path, true)
	}
	_, _ = fmt.Fprintf(errW, "\nDéclaré. Pour le réaliser :\n  pvecli iac plan && pvecli iac apply\n")
	return renderDeclaredLXC(cmd, name, &written, path, true)
}

// buildLXCDeclaration mirrors buildDeclaration: only flags actually typed
// override what is already declared.
func buildLXCDeclaration(cmd *cobra.Command, name string, o *lxcDeclareOpts, before *iac.LXC) (*iac.LXC, error) {
	ct := iac.LXC{}
	if before != nil {
		ct = *before
	} else {
		ct.Disk, ct.IP, ct.Unprivileged = 8, "dhcp", true
	}

	f := cmd.Flags()
	set := func(name string, apply func()) {
		if f.Changed(name) {
			apply()
		}
	}
	set("vmid", func() { ct.VMID = o.vmid })
	set("cores", func() { ct.Cores = o.cores })
	set("memory", func() { ct.Memory = o.memory })
	set("disk", func() { ct.Disk = o.disk })
	set("template", func() { ct.Template = o.template })
	set("ip", func() { ct.IP = o.ip })
	set("gateway", func() { ct.Gateway = o.gateway })
	set("node", func() { ct.Node = o.node })
	set("unprivileged", func() { ct.Unprivileged = o.unprivileged })

	// Creation only, and only on a terminal -- see the identical guard in
	// buildDeclaration (VM side) for why.
	if before == nil && stdinIsTerminal() {
		if err := promptMissingLXCFields(cmd, &ct, f); err != nil {
			return nil, err
		}
	}

	if ct.IP == "dhcp" && !f.Changed("gateway") {
		ct.Gateway = ""
	}

	cat, err := catalog.Load()
	if err != nil {
		return nil, err
	}

	switch {
	case f.Changed("with"):
		services, err := cat.Resolve(o.with)
		if err != nil {
			return nil, err
		}
		ct.Services = serviceIDs(services)
		ct.SetTags(catalog.Tags(services))
	case before == nil:
		picked, err := pickServices(cmd, cat)
		if err != nil {
			return nil, err
		}
		services, err := cat.Resolve(picked)
		if err != nil {
			return nil, err
		}
		ct.Services = serviceIDs(services)
		ct.SetTags(catalog.Tags(services))
	default:
		services, err := cat.Resolve(ct.Services)
		if err != nil {
			return nil, err
		}
		ct.SetTags(catalog.Tags(services))
	}

	if before == nil {
		var missing []string
		for _, req := range []struct {
			flag string
			zero bool
		}{
			{"--vmid", ct.VMID == 0},
			{"--cores", ct.Cores == 0},
			{"--memory", ct.Memory == 0},
			{"--template", ct.Template == 0},
		} {
			if req.zero {
				missing = append(missing, req.flag)
			}
		}
		if len(missing) > 0 {
			return nil, &exitError{code: 2, msg: fmt.Sprintf(
				"« %s » n'existe pas encore dans la déclaration : %s %s obligatoire%s à la création",
				name, strings.Join(missing, ", "),
				plural(len(missing), "est", "sont"), plural(len(missing), "", "s"))}
		}
	}

	if err := ct.Validate(name); err != nil {
		return nil, &exitError{code: 2, msg: err.Error()}
	}
	return &ct, nil
}

// promptMissingLXCFields mirrors promptMissingVMFields. --template has no
// optional counterpart here: unlike a VM there is no shared clone source to
// fall back on, so it loops like vmid/cores/memory rather than accepting a
// blank answer.
func promptMissingLXCFields(cmd *cobra.Command, ct *iac.LXC, f *pflag.FlagSet) error {
	var err error
	if !f.Changed("vmid") {
		if ct.VMID, err = promptInt(cmd, "vmid (identifiant PVE)", ct.VMID); err != nil {
			return err
		}
	}
	if !f.Changed("cores") {
		if ct.Cores, err = promptInt(cmd, "cores", ct.Cores); err != nil {
			return err
		}
	}
	if !f.Changed("memory") {
		if ct.Memory, err = promptInt(cmd, "memory en Mio (8 Go = 8192)", ct.Memory); err != nil {
			return err
		}
	}
	if !f.Changed("disk") {
		if ct.Disk, err = promptInt(cmd, "disk en Gio", ct.Disk); err != nil {
			return err
		}
	}
	if !f.Changed("template") {
		if ct.Template, err = promptInt(cmd, "template — ctid à cloner", ct.Template); err != nil {
			return err
		}
	}
	if !f.Changed("ip") {
		if ct.IP, err = promptString(cmd, "ip (dhcp, ou 192.168.1.221/24)", ct.IP); err != nil {
			return err
		}
	}
	if ct.IP != "dhcp" && !f.Changed("gateway") {
		if ct.Gateway, err = promptString(cmd, "gateway", ct.Gateway); err != nil {
			return err
		}
	}
	if !f.Changed("node") {
		if ct.Node, err = promptString(cmd, "node (vide = défaut du module)", ct.Node); err != nil {
			return err
		}
	}
	if !f.Changed("unprivileged") {
		if ct.Unprivileged, err = promptBool(cmd, "non privilégié", ct.Unprivileged); err != nil {
			return err
		}
	}
	return nil
}

func declareVerbLXC(before, after *iac.LXC) string {
	switch {
	case before == nil:
		return "création de la déclaration"
	case after == nil:
		return "retrait de la déclaration"
	default:
		return "mise à jour"
	}
}

// renderDeclaredLXC mirrors renderDeclared for containers.
func renderDeclaredLXC(cmd *cobra.Command, name string, ct *iac.LXC, path string, written bool) error {
	opts, err := renderOptions(cmd)
	if err != nil {
		return err
	}
	rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}}
	add := func(k, v string) {
		if v != "" {
			rows.Cells = append(rows.Cells, []string{k, v})
		}
	}
	add("déclaration", path)
	add("nom", name)
	if ct == nil {
		add("état", "absent de la déclaration")
		return output.Render(cmd.OutOrStdout(), opts, map[string]any{"name": name, "declared": false}, rows)
	}
	add("vmid", fmt.Sprintf("%d", ct.VMID))
	add("cœurs", fmt.Sprintf("%d", ct.Cores))
	add("mémoire", fmt.Sprintf("%d Mio", ct.Memory))
	if ct.Disk > 0 {
		add("disque", fmt.Sprintf("%d Gio", ct.Disk))
	}
	add("ip", ct.IP)
	add("passerelle", ct.Gateway)
	add("nœud", ct.Node)
	add("template", fmt.Sprintf("%d", ct.Template))
	add("non privilégié", fmt.Sprintf("%t", ct.Unprivileged))
	add("services", strings.Join(ct.Services, ", "))
	add("tags", strings.Join(ct.Tags, ", "))
	if !written {
		add("état", "déclaration inchangée")
	}
	return output.Render(cmd.OutOrStdout(), opts, ct, rows)
}
