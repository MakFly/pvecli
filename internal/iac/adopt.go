package iac

import (
	"fmt"
	"sort"
	"strings"
)

// Adopt renders the Terraform blocks that bring an existing guest under
// Terraform's control, without recreating it.
//
// It renders text and returns it. It does not write a .tf file, and that is a
// design decision rather than a missing feature: importing is an exercise in
// reverse engineering, where the code has to describe EXACTLY what already
// exists before the state will accept it. A tool that wrote the file would
// invite `terraform apply` on a block nobody read — and the first apply on a
// wrong import block destroys and recreates the resource it was meant to save.
func Adopt(l Live, isContainer bool) string {
	resourceType := TypeVM
	if isContainer {
		resourceType = TypeContainer
	}
	name := ResourceName(l.Name, l.VMID)

	var b strings.Builder
	fmt.Fprintf(&b, `# Généré par « pvecli iac adopt %d ». À RELIRE, pas à appliquer tel quel.
#
#   1. colle ces deux blocs dans main.tf
#   2. terraform plan    → ajuste le code jusqu'à « No changes »
#   3. terraform apply   → la ressource entre dans le state, sans être recréée
#   4. tague-la pour que la garde de propriété la protège :
#        pvecli %s set %d --tags lab,terraform,managed --force-unmanaged
#
# L'étape 2 est la seule qui compte. L'import n'enregistre la ressource que si
# le code décrit EXACTEMENT l'existant ; tant que « plan » propose un
# changement, c'est le code qui est faux, pas le nœud. Un apply lancé trop tôt
# détruit et recrée la ressource qu'on voulait justement préserver.

`, l.VMID, cliFamily(isContainer), l.VMID)

	// The import block's id is the provider's own addressing scheme, not PVE's.
	// bpg/proxmox expects "<node>/<vmid>" — a bare vmid is accepted by the
	// parser and then fails at read time with a message about a missing node.
	fmt.Fprintf(&b, "import {\n  to = %s.%s\n  id = \"%s/%d\"\n}\n\n", resourceType, name, l.Node, l.VMID)

	fmt.Fprintf(&b, "resource %q %q {\n", resourceType, name)
	fmt.Fprintf(&b, "  vm_id     = %d\n", l.VMID)
	fmt.Fprintf(&b, "  node_name = %q\n", l.Node)
	// The two resources are close enough to invite a shared code path and
	// different enough to punish one. Checked against the provider's own schema
	// (`terraform providers schema -json`), not against what looked symmetric:
	//
	//	VM         name          on_boot
	//	conteneur  initialization { hostname }   start_on_boot
	//
	// The first version of this function wrote `hostname` at the top level of a
	// container and `terraform validate` refused it — which is why the
	// generated block is validated against the real schema in the tests.
	if l.Name != "" && !isContainer {
		fmt.Fprintf(&b, "  name      = %q\n", l.Name)
	}
	if l.OnBoot != nil {
		key := "on_boot"
		if isContainer {
			key = "start_on_boot"
		}
		fmt.Fprintf(&b, "  %-9s = %t\n", key, *l.OnBoot)
	}
	if len(l.Tags) > 0 {
		fmt.Fprintf(&b, "  tags      = [%s]\n", quoteList(normalise(l.Tags)))
	}

	if l.Cores > 0 {
		fmt.Fprintf(&b, "\n  cpu {\n    cores = %d\n  }\n", l.Cores)
	}
	if l.Memory > 0 {
		fmt.Fprintf(&b, "\n  memory {\n    dedicated = %d\n  }\n", l.Memory)
	}

	for _, iface := range sortedDiskKeys(l.Disks) {
		d := l.Disks[iface]
		if isContainer {
			fmt.Fprintf(&b, "\n  disk {\n    datastore_id = %q\n    size         = %d\n  }\n", d.Datastore, d.SizeGiB)
			continue
		}
		fmt.Fprintf(&b, "\n  disk {\n    datastore_id = %q\n    interface    = %q\n    size         = %d\n  }\n",
			d.Datastore, iface, d.SizeGiB)
	}

	for i, n := range l.Networks {
		if isContainer {
			fmt.Fprintf(&b, "\n  network_interface {\n    name   = \"eth%d\"\n    bridge = %q\n  }\n", i, n.Bridge)
			continue
		}
		fmt.Fprintf(&b, "\n  network_device {\n    bridge = %q\n", n.Bridge)
		if n.Model != "" {
			fmt.Fprintf(&b, "    model  = %q\n", n.Model)
		}
		if n.VLAN != 0 {
			fmt.Fprintf(&b, "    vlan_id = %d\n", n.VLAN)
		}
		b.WriteString("  }\n")
	}

	b.WriteString(missingPieces(l, isContainer))
	b.WriteString("}\n")
	return b.String()
}

// missingPieces names, in the generated file, what pvecli deliberately did not
// guess.
//
// These are the attributes that decide whether `terraform plan` converges. The
// cloud-init drive above all: every VM cloned from a cloud-init template
// carries an ide2 volume that lives under `initialization`, not under `disk`.
// Omit it and the first plan proposes to delete it — which reads like a bug in
// pvecli and is in fact the import doing its job.
func missingPieces(l Live, isContainer bool) string {
	var b strings.Builder
	b.WriteString("\n  # À COMPLÉTER À LA MAIN — pvecli ne devine pas ces blocs :\n")
	if isContainer {
		b.WriteString("  #   operating_system { template_file_id = … }   obligatoire, et illisible depuis l'API\n")
		if l.Name != "" {
			fmt.Fprintf(&b, "  #   initialization { hostname = %q }   le nom d'un conteneur se déclare ICI,\n", l.Name)
			b.WriteString("  #                                        pas au premier niveau comme pour une VM\n")
		}
		return b.String()
	}
	if _, hasCloudInit := l.Disks["ide2"]; hasCloudInit {
		b.WriteString("  #   initialization { … }   ce guest porte un lecteur cloud-init (ide2). Il se\n")
		b.WriteString("  #                          déclare ici, PAS dans un bloc « disk ». Sans lui,\n")
		b.WriteString("  #                          le premier plan proposera de le supprimer.\n")
	}
	b.WriteString("  #   agent { enabled = … }  si l'agent QEMU est activé côté PVE\n")
	b.WriteString("  #   PAS de bloc « clone » : la ressource existe déjà, la cloner la recréerait\n")
	return b.String()
}

// sortedDiskKeys returns the disk interfaces in a stable order, minus the ones
// the provider declares elsewhere.
func sortedDiskKeys(disks map[string]LiveDisk) []string {
	keys := make([]string, 0, len(disks))
	for k := range disks {
		// ide2 is the cloud-init drive, handled by `initialization`.
		if k == "ide2" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ResourceName derives a Terraform identifier from a guest name.
//
// Terraform labels obey the same rules as Ansible group names — letters,
// digits, underscores, and no leading digit — so the two share a sanitiser. A
// guest called « lab-app-01 » becomes lab_app_01; one with no name at all falls
// back to its vmid, which is the only identifier PVE guarantees.
func ResourceName(name string, vmid int) string {
	if n := GroupName(name); n != "" {
		return n
	}
	return fmt.Sprintf("guest_%d", vmid)
}

func quoteList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, i := range items {
		quoted = append(quoted, fmt.Sprintf("%q", i))
	}
	return strings.Join(quoted, ", ")
}

func cliFamily(isContainer bool) string {
	if isContainer {
		return "lxc"
	}
	return "vm"
}
