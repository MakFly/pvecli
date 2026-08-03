package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MakFly/pvecli/internal/catalog"
	"github.com/MakFly/pvecli/internal/iac"
	"github.com/MakFly/pvecli/internal/output"
)

type declareOpts struct {
	vmid, cores, memory, disk, template int
	ip, gateway, node, user             string
	with                                []string
	remove                              bool
	suggestID                           bool
}

func newVMDeclareCmd() *cobra.Command {
	var o declareOpts

	c := &cobra.Command{
		Use:   "declare <nom>",
		Short: "Déclare une VM et ses services dans " + iac.DeclarationFile,
		Long: `Écrit, met à jour ou retire une VM dans la déclaration que Terraform lit.

  <terraform_dir>/` + iac.DeclarationFile + `

Cette commande ne crée RIEN sur le nœud. Elle écrit une intention ; c'est
« pvecli iac apply » qui la réalise, et la relecture par l'API qui la prouve.
La séparation est volontaire : on peut déclarer, relire, corriger, et n'engager
le nœud qu'ensuite.

Ce qui n'est pas passé en option n'est pas touché. Redimensionner une VM
existante, c'est donc une seule option :

  pvecli vm declare app-01 --memory 16384
  pvecli iac apply

--with choisit les services du catalogue. Leurs dépendances sont ajoutées
d'office, et chacun pose un tag « svc_<id> » sur la VM — ce tag est ce que
« pvecli iac inventory » transforme en groupe Ansible, et donc ce qui décide
quels rôles seront joués. Sans --with, sur un terminal, la liste est proposée
à cocher.

  pvecli vm declare app-01 --vmid 220 --cores 2 --memory 8192 \
      --ip 192.168.1.220/24 --gateway 192.168.1.1 \
      --with docker,postgresql,cloudflared

--remove retire l'entrée. La VM ne disparaît qu'au « iac apply » suivant, où
Terraform la détruira — c'est une opération destructive, et elle se confirme en
retapant le nom.`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeclare(cmd, args[0], &o)
		},
	}

	f := c.Flags()
	f.IntVar(&o.vmid, "vmid", 0, "identifiant PVE de la VM (obligatoire à la création)")
	f.IntVar(&o.cores, "cores", 0, "nombre de cœurs")
	f.IntVar(&o.memory, "memory", 0, "mémoire en MIBIOCTETS — 8 Go s'écrit 8192")
	f.IntVar(&o.disk, "disk", 0, "taille du disque en Gio")
	f.IntVar(&o.template, "template", 0, "vmid du template à cloner (défaut : var.template_vm_id)")
	f.StringVar(&o.ip, "ip", "", "adresse : dhcp, ou 192.168.1.220/24")
	f.StringVar(&o.gateway, "gateway", "", "passerelle, si --ip est statique")
	f.StringVar(&o.node, "node", "", "nœud cible (défaut : var.node_name)")
	f.StringVar(&o.user, "user", "", "utilisateur cloud-init (défaut : ops)")
	f.StringSliceVar(&o.with, "with", nil, "services du catalogue, séparés par des virgules")
	f.BoolVar(&o.remove, "remove", false, "retire la VM de la déclaration")
	f.BoolVar(&o.suggestID, "suggest-id", false, suggestIDHelp)

	addWriteFlags(c)
	addRenderFlags(c)
	_ = c.RegisterFlagCompletionFunc("with", completeServices)
	return c
}

func runDeclare(cmd *cobra.Command, name string, o *declareOpts) error {
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
	existing, exists := decl.VMs[name]

	var before *iac.VM
	if exists {
		copyOf := existing
		before = &copyOf
	}

	// Résolu ici, juste après la pré-lecture : c'est le seul endroit qui voit
	// à la fois « exists » et le cas --remove, qui saute buildDeclaration.
	suggested, err := resolveSuggestedID(cmd, name, o.suggestID, cmd.Flags().Changed("vmid"), o.remove, exists, decl)
	if err != nil {
		return err
	}

	var after *iac.VM
	if !o.remove {
		next, err := buildDeclaration(cmd, name, o, before, suggested)
		if err != nil {
			return err
		}
		after = next
	} else if !exists {
		return fmt.Errorf("« %s » n'est pas dans la déclaration — rien à retirer", name)
	}

	// A vmid is unique across the whole cluster, VMs and containers together.
	// Refusing here is the difference between a clear message and an opaque
	// failure at `terraform apply`. --remove claims no vmid, so it is skipped.
	if after != nil {
		if owner, kind, taken := decl.VMIDOwner(after.VMID, name, iac.KindVM); taken {
			return vmidTaken(after.VMID, owner, kind)
		}
	}

	changes := iac.Diff(before, after)
	errW := cmd.ErrOrStderr()
	path := iac.DeclarationPath(eff.IaC.TerraformDir)

	// PLAN — the field-level difference, not a paraphrase and not a text diff.
	_, _ = fmt.Fprintf(errW, "déclaration %s\n", path)
	_, _ = fmt.Fprintf(errW, "vm          %s — %s\n", name, declareVerb(before, after))
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(errW, "aucun changement : la déclaration dit déjà exactement cela.")
		return renderDeclared(cmd, name, after, path, false)
	}
	for _, ch := range changes {
		switch {
		case ch.Before == "":
			_, _ = fmt.Fprintf(errW, "  + %-10s %s\n", ch.Field, ch.After)
		case ch.After == "":
			_, _ = fmt.Fprintf(errW, "  - %-10s %s\n", ch.Field, ch.Before)
		default:
			_, _ = fmt.Fprintf(errW, "  ~ %-10s %s → %s\n", ch.Field, ch.Before, ch.After)
		}
	}
	_, _ = fmt.Fprintf(errW, "effet       la déclaration change ; le nœud, lui, ne bouge qu'au « pvecli iac apply »\n")

	// GATE.
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		_, _ = fmt.Fprintln(errW, "--dry-run : rien n'a été écrit.")
		return renderDeclared(cmd, name, after, path, false)
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := (cliGate{cmd: cmd, yes: yes}).Allow(name, o.remove); err != nil {
		return err
	}

	// WRITE.
	if o.remove {
		delete(decl.VMs, name)
	} else {
		decl.VMs[name] = *after
	}
	if err := decl.Save(eff.IaC.TerraformDir); err != nil {
		return err
	}

	// POST-READ — re-read from disk. What gets shown is the file, not what we
	// believe we wrote into it.
	reread, err := iac.LoadDeclaration(eff.IaC.TerraformDir)
	if err != nil {
		return err
	}
	written, stillThere := reread.VMs[name]
	if o.remove && stillThere {
		return fmt.Errorf("« %s » est toujours dans %s après écriture", name, path)
	}
	if !o.remove && !stillThere {
		return fmt.Errorf("« %s » est absent de %s après écriture", name, path)
	}

	if o.remove {
		_, _ = fmt.Fprintf(errW, "\nRetirée de la déclaration. Terraform la détruira au prochain :\n  pvecli iac plan && pvecli iac apply\n")
		return renderDeclared(cmd, name, nil, path, true)
	}
	_, _ = fmt.Fprintf(errW, "\nDéclarée. Pour la réaliser :\n  pvecli iac plan && pvecli iac apply\n")
	return renderDeclared(cmd, name, &written, path, true)
}

// buildDeclaration merges the flags actually given onto what is already
// declared. Only flags the operator typed override -- that is what makes
// `declare app-01 --memory 16384` a resize instead of a reset to defaults.
//
// suggestedVMID is 0 unless --suggest-id was resolved by the caller (its
// refusals guarantee before == nil whenever it is non-zero: --suggest-id on
// an existing entry is refused before buildDeclaration is even called).
func buildDeclaration(cmd *cobra.Command, name string, o *declareOpts, before *iac.VM, suggestedVMID int) (*iac.VM, error) {
	vm := iac.VM{}
	if before != nil {
		vm = *before
	} else {
		// Creation defaults. They are only applied when there is nothing to
		// preserve.
		vm.Disk, vm.IP, vm.User = 20, "dhcp", "ops"
	}

	f := cmd.Flags()
	set := func(name string, apply func()) {
		if f.Changed(name) {
			apply()
		}
	}
	set("vmid", func() { vm.VMID = o.vmid })
	set("cores", func() { vm.Cores = o.cores })
	set("memory", func() { vm.Memory = o.memory })
	set("disk", func() { vm.Disk = o.disk })
	set("template", func() { vm.Template = o.template })
	set("ip", func() { vm.IP = o.ip })
	set("gateway", func() { vm.Gateway = o.gateway })
	set("node", func() { vm.Node = o.node })
	set("user", func() { vm.User = o.user })

	if suggestedVMID != 0 {
		// Posé AVANT le bloc assistant qui suit : c'est ce qui fait apparaître
		// le vmid suggéré comme défaut de promptInt (« [235] »), sans changer
		// sa signature -- et ce qui fait tenir la garde « obligatoire à la
		// création » côté --yes, où l'assistant est sauté.
		vm.VMID = suggestedVMID
	}

	// Creation only, and only on a terminal: a redeclaration keeps the
	// "only what's typed moves" rule untouched, and a script piping into this
	// command must never block on a question it did not ask for.
	if before == nil && stdinIsTerminal() {
		if err := promptMissingVMFields(cmd, &vm, f); err != nil {
			return nil, err
		}
	}

	// Switching a static VM to DHCP must drop the gateway it no longer needs --
	// a stale one is refused by the API at apply time. But only the INHERITED
	// one: a --gateway typed in the same breath as --ip dhcp is a contradiction,
	// and Validate says so rather than quietly picking a winner.
	if vm.IP == "dhcp" && !f.Changed("gateway") {
		vm.Gateway = ""
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
		vm.Services = serviceIDs(services)
		vm.SetTags(catalog.Tags(services))
	case before == nil:
		// A brand new VM with no --with: offer the list rather than silently
		// producing a bare guest the operator did not mean to ask for.
		picked, err := pickServices(cmd, cat)
		if err != nil {
			return nil, err
		}
		services, err := cat.Resolve(picked)
		if err != nil {
			return nil, err
		}
		vm.Services = serviceIDs(services)
		vm.SetTags(catalog.Tags(services))
	default:
		// Untouched: re-derive the tags anyway, so an older declaration written
		// before a tag rule changed converges on the next write.
		services, err := cat.Resolve(vm.Services)
		if err != nil {
			return nil, err
		}
		vm.SetTags(catalog.Tags(services))
	}

	if before == nil {
		var missing []string
		for _, req := range []struct {
			flag string
			zero bool
		}{
			{"--vmid", vm.VMID == 0},
			{"--cores", vm.Cores == 0},
			{"--memory", vm.Memory == 0},
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

	if err := vm.Validate(name); err != nil {
		return nil, &exitError{code: 2, msg: err.Error()}
	}
	return &vm, nil
}

// promptMissingVMFields asks, one at a time, for every field the operator did
// not pass as a flag. Order follows the flags' own declaration order, which is
// also roughly the order a human decides them in: identity, then sizing, then
// network.
func promptMissingVMFields(cmd *cobra.Command, vm *iac.VM, f *pflag.FlagSet) error {
	var err error
	if !f.Changed("vmid") {
		if vm.VMID, err = promptInt(cmd, "vmid (identifiant PVE)", vm.VMID); err != nil {
			return err
		}
	}
	if !f.Changed("cores") {
		if vm.Cores, err = promptInt(cmd, "cores", vm.Cores); err != nil {
			return err
		}
	}
	if !f.Changed("memory") {
		if vm.Memory, err = promptInt(cmd, "memory en Mio (8 Go = 8192)", vm.Memory); err != nil {
			return err
		}
	}
	if !f.Changed("disk") {
		if vm.Disk, err = promptInt(cmd, "disk en Gio", vm.Disk); err != nil {
			return err
		}
	}
	if !f.Changed("template") {
		if vm.Template, err = promptIntOptional(cmd, "template — vmid à cloner (vide = défaut du module)", vm.Template); err != nil {
			return err
		}
	}
	if !f.Changed("ip") {
		if vm.IP, err = promptString(cmd, "ip (dhcp, ou 192.168.1.220/24)", vm.IP); err != nil {
			return err
		}
	}
	if vm.IP != "dhcp" && !f.Changed("gateway") {
		if vm.Gateway, err = promptString(cmd, "gateway", vm.Gateway); err != nil {
			return err
		}
	}
	if !f.Changed("node") {
		if vm.Node, err = promptString(cmd, "node (vide = défaut du module)", vm.Node); err != nil {
			return err
		}
	}
	if !f.Changed("user") {
		if vm.User, err = promptString(cmd, "utilisateur cloud-init", vm.User); err != nil {
			return err
		}
	}
	return nil
}

// vmidTaken is the refusal both declare commands share. It names the kind of
// the owner, not just its name: "déjà pris par app-01" would leave the operator
// looking for a VM that is in fact a container.
func vmidTaken(vmid int, owner, kind string) error {
	held := "la vm"
	if kind == iac.KindLXC {
		held = "le lxc"
	}
	return &exitError{code: 2, msg: fmt.Sprintf(
		"vmid %d est déjà pris par %s « %s » — un vmid Proxmox est unique, VM et LXC confondus",
		vmid, held, owner)}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func serviceIDs(services []catalog.Service) []string {
	out := make([]string, 0, len(services))
	for _, s := range services {
		out = append(out, s.ID)
	}
	return out
}

func declareVerb(before, after *iac.VM) string {
	switch {
	case before == nil:
		return "création de la déclaration"
	case after == nil:
		return "retrait de la déclaration"
	default:
		return "mise à jour"
	}
}

// renderDeclared prints the entry as it now stands on disk.
func renderDeclared(cmd *cobra.Command, name string, vm *iac.VM, path string, written bool) error {
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
	if vm == nil {
		add("état", "absente de la déclaration")
		return output.Render(cmd.OutOrStdout(), opts, map[string]any{"name": name, "declared": false}, rows)
	}
	add("vmid", fmt.Sprintf("%d", vm.VMID))
	add("cœurs", fmt.Sprintf("%d", vm.Cores))
	add("mémoire", fmt.Sprintf("%d Mio", vm.Memory))
	if vm.Disk > 0 {
		add("disque", fmt.Sprintf("%d Gio", vm.Disk))
	}
	add("ip", vm.IP)
	add("passerelle", vm.Gateway)
	add("nœud", vm.Node)
	add("utilisateur", vm.User)
	add("services", strings.Join(vm.Services, ", "))
	add("tags", strings.Join(vm.Tags, ", "))
	if !written {
		add("état", "déclaration inchangée")
	}
	return output.Render(cmd.OutOrStdout(), opts, vm, rows)
}

// completeServices offers the catalogue at the Tab key.
func completeServices(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cat, err := catalog.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var out []string
	for _, s := range cat.Services {
		if strings.HasPrefix(s.ID, toComplete) {
			out = append(out, s.ID+"\t"+s.Summary)
		}
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}
