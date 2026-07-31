package cmd

import (
	"fmt"

	"github.com/MakFly/pvecli/internal/config"
	"github.com/MakFly/pvecli/internal/pve"
	"github.com/spf13/cobra"
)

// ManagedTag is the default tag marking a guest an IaC tool owns. The effective
// value comes from `iac.managed_tag`; this constant is what the help text can
// name at build time, before any configuration has been read.
const ManagedTag = config.DefaultManagedTag

// The ownership contract of PRD §5.4, and the exact line it draws.
//
// A guest carrying the managed tag has one owner, and it is not this CLI.
// Writing to it here does not fail — it succeeds, and leaves a Terraform state
// describing something that no longer exists. That divergence is invisible
// until the next `terraform plan`, which is what makes it expensive.
//
// The line is not "writes are forbidden": it is "the DECLARED CONFIGURATION is
// forbidden". Terraform declares how many cores a VM has, what it is named,
// which disks it carries. It does not declare whether the VM is running right
// now — `on_boot` is a declaration, `start` is not. So:
//
//	refused   set · clone · template · snapshot rollback · rm
//	allowed   start · stop · shutdown · reboot · reset · suspend · resume
//	allowed   every read, and snapshot create/rm
//
// snapshot rollback sits on the refused side and snapshot create does not,
// which looks arbitrary until you remember what a PVE snapshot stores: the
// guest's configuration alongside its disks. Rolling back rewrites cores,
// memory and tags to what they were — a configuration write wearing the
// clothes of a restore.
const ownershipHelp = `
GARDE DE PROPRIÉTÉ. Un guest portant le tag « ` + ManagedTag + ` » appartient à
Terraform. Cette commande modifie une configuration DÉCLARÉE : elle est donc
refusée, et renvoie vers le propriétaire. Les changements d'état d'exécution
(start, stop, shutdown) restent autorisés — Terraform ne déclare pas si une VM
tourne, seulement comment elle est faite.`

// managedOp is one guarded write: what pvecli was asked to do, and what the
// owner's equivalent is. The refusal names the alternative, because a guard
// that only says no teaches the operator to reach for --force-unmanaged.
type managedOp struct {
	verb  string
	owner string
}

var (
	opSetConfig = managedOp{
		"modifier sa configuration",
		"modifie la ressource dans main.tf, puis : terraform apply",
	}
	opDestroy = managedOp{
		"le détruire",
		"terraform destroy -target=…",
	}
	opTemplate = managedOp{
		"le convertir en template",
		"déclare le template dans main.tf — la conversion est irréversible",
	}
	opRollback = managedOp{
		"restaurer un snapshot",
		"un rollback réécrit la configuration figée dans le snapshot ;\n" +
			"     passe par terraform apply pour revenir à l'état déclaré",
	}
	opCloneSource = managedOp{
		"le cloner",
		"ajoute une ressource dans main.tf, puis : terraform apply",
	}
	// A migration is not an execution-state change, whatever it looks like:
	// the bpg provider declares `node_name`, so moving a guest to another node
	// makes the state describe a machine that is somewhere else.
	opMigrate = managedOp{
		"le déplacer sur un autre nœud",
		"change node_name dans main.tf, puis : terraform apply",
	}
)

// ownership carries the guard's inputs for one command invocation.
type ownership struct {
	tag   string
	force bool
	cmd   *cobra.Command
}

// newOwnership reads the effective managed tag and the --force-unmanaged flag.
//
// It resolves the configuration a second time — newClient already did — and
// that is deliberate: config.Load is a file read with no network, and threading
// an *Effective through every command constructor would buy microseconds at the
// cost of the property that makes this package testable, namely that no command
// depends on hidden global state.
func newOwnership(cmd *cobra.Command) (ownership, error) {
	eff, err := resolveConfig(cmd)
	if err != nil {
		return ownership{}, err
	}
	force, _ := cmd.Flags().GetBool("force-unmanaged")
	return ownership{tag: eff.IaC.ManagedTag, force: force, cmd: cmd}, nil
}

// check refuses a write on a guest owned by an IaC tool.
//
// It takes the tag string rather than a guest, because tags reach us in two
// shapes: GuestStatus.Tags from status/current, and the "tags" key of a
// GuestConfig. Both are the same semicolon-separated string.
func (o ownership) check(vmid int, tags string, op managedOp) error {
	if !pve.HasTag(tags, o.tag) {
		return nil
	}

	if o.force {
		// The override is not silent. Whoever used it has just made the state
		// stale, and the sentence that says so is worth more than the flag.
		_, _ = fmt.Fprintf(o.cmd.ErrOrStderr(),
			"--force-unmanaged : le guest %d appartient à Terraform et va être modifié quand même.\n"+
				"  le state décrit maintenant autre chose que le réel ; rattrape-le :\n"+
				"    terraform refresh    (ou : terraform plan, qui le dira aussi)\n",
			vmid)
		return nil
	}

	return fmt.Errorf(
		"le guest %d porte le tag « %s » : il appartient à Terraform, pas à toi.\n"+
			"  %s ici créerait une dérive que personne ne saura expliquer plus tard.\n"+
			"\n"+
			"  passe par son propriétaire :\n"+
			"     %s\n"+
			"\n"+
			"--force-unmanaged passe outre — il faudra alors « terraform refresh »\npour que le state rattrape ce que tu viens de changer",
		vmid, o.tag, op.verb, op.owner)
}

// addOwnershipFlag declares the escape hatch on a guarded command.
//
// It is separate from addWriteFlags because not every write targets a guest:
// `access acl set`, `access token create` and `backup run` are writes with
// nothing to own. A flag they cannot honour would be a lie in their help.
func addOwnershipFlag(c *cobra.Command) {
	c.Flags().Bool("force-unmanaged", false,
		"passe outre la garde « "+ManagedTag+" » — déconseillé, rend le state Terraform obsolète")
}
