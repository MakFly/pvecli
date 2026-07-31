// Package iac bridges the Proxmox API to the two tools that actually declare
// the infrastructure: Terraform, which owns it, and Ansible, which configures
// it (PRD §5.4).
//
// Nothing here talks to PVE. The package takes facts that a caller has already
// read from the API and turns them into what the other tools consume — which is
// what lets every shape below be tested without a node.
package iac

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	yaml "go.yaml.in/yaml/v3"
)

// Host is one machine the generated inventory points Ansible at.
//
// IP is deliberately not called "address": it is the address the guest agent
// reported, not one PVE inferred. The distinction is the whole reason this
// command exists — see the comment on Inventory.
type Host struct {
	Name   string
	VMID   int
	Node   string
	IP     string
	User   string
	Groups []string
}

// Excluded is a guest that could not become a host, and why.
//
// Every exclusion is carried out of the generator rather than dropped, because
// an inventory that is quietly shorter than the fleet is the failure mode here:
// Ansible reports "ok=0 changed=0" for a host it never knew about, and that
// reads exactly like success.
type Excluded struct {
	VMID   int
	Name   string
	Reason string
}

// Inventory is one generation: what made it in, and what did not.
type Inventory struct {
	Hosts    []Host
	Excluded []Excluded
}

// UngroupedName is where hosts with no usable tag land under --group-by tag.
//
// Ansible has its own implicit `ungrouped`, and reusing the name is on purpose:
// an operator who sees it already knows what it means. What matters is that the
// host appears somewhere — a host filtered out of every group is a host that
// silently stops being configured.
const UngroupedName = "ungrouped"

// Add records a host, deriving nothing it was not given.
func (inv *Inventory) Add(h Host) { inv.Hosts = append(inv.Hosts, h) }

// Skip records a guest that did not make it into the inventory.
func (inv *Inventory) Skip(vmid int, name, reason string) {
	inv.Excluded = append(inv.Excluded, Excluded{VMID: vmid, Name: name, Reason: reason})
}

// Resolve puts the hosts in a stable order and disambiguates duplicate names.
//
// Two guests may legitimately carry the same name — PVE does not enforce
// uniqueness, only vmid is unique. In an inventory keyed by name, the second
// one would overwrite the first and disappear without a word. Suffixing the
// vmid keeps both, and the caller is told so it can say it out loud.
func (inv *Inventory) Resolve() (renamed []string) {
	sort.Slice(inv.Hosts, func(i, j int) bool { return inv.Hosts[i].VMID < inv.Hosts[j].VMID })

	seen := map[string]int{}
	for _, h := range inv.Hosts {
		seen[h.Name]++
	}
	for i, h := range inv.Hosts {
		if seen[h.Name] > 1 {
			inv.Hosts[i].Name = fmt.Sprintf("%s-%d", h.Name, h.VMID)
			renamed = append(renamed, fmt.Sprintf("%s → %s", h.Name, inv.Hosts[i].Name))
		}
	}
	return renamed
}

// GroupName turns a PVE tag into a name Ansible accepts.
//
// The two vocabularies do not agree. PVE allows a tag like `lab-apps`; Ansible
// rejects a group name containing a dash, because group names become Python
// identifiers in templates — `{{ groups.lab-apps }}` parses as a subtraction.
// Substituting an underscore is the only lossless move available, and the
// caller reports every substitution rather than performing it silently.
func GroupName(tag string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(tag) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := b.String()
	// A group must not start with a digit, for the same reason.
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name
}

// tree is the on-disk shape, and it is the shape of
// docs/infra/ansible/inventory.example.yml — not a variant of it. The generated
// file has to be droppable in place of the hand-written example.
type tree struct {
	All allNode `yaml:"all" json:"all"`
}

type allNode struct {
	Children map[string]group `yaml:"children" json:"children"`
}

type group struct {
	Hosts map[string]hostVars `yaml:"hosts" json:"hosts"`
}

type hostVars struct {
	AnsibleHost string `yaml:"ansible_host" json:"ansible_host"`
	AnsibleUser string `yaml:"ansible_user,omitempty" json:"ansible_user,omitempty"`
}

// build assembles the group→host tree.
func (inv *Inventory) build() tree {
	t := tree{All: allNode{Children: map[string]group{}}}
	for _, h := range inv.Hosts {
		groups := h.Groups
		if len(groups) == 0 {
			groups = []string{UngroupedName}
		}
		for _, g := range groups {
			if _, ok := t.All.Children[g]; !ok {
				t.All.Children[g] = group{Hosts: map[string]hostVars{}}
			}
			t.All.Children[g].Hosts[h.Name] = hostVars{AnsibleHost: h.IP, AnsibleUser: h.User}
		}
	}
	return t
}

// Header is the banner every generated inventory carries.
//
// It names the tool, forbids hand-editing, and dates itself — an inventory with
// no timestamp cannot be told from a stale one, and a stale inventory points
// Ansible at an address a DHCP lease has since moved.
func Header(endpoint string, at time.Time) string {
	return fmt.Sprintf("# GENERATED BY pvectl — DO NOT EDIT\n"+
		"# %s · %s\n"+
		"# régénère plutôt : pvectl iac inventory\n",
		at.UTC().Format(time.RFC3339), endpoint)
}

// RenderYAML produces the inventory file Ansible reads.
//
// The indent is forced to 2 rather than left at yaml.v3's default of 4: the
// generated file has to be droppable in place of
// docs/infra/ansible/inventory.example.yml, and a file that differs from the
// example in whitespace alone invites an operator to "fix" it by hand — which
// is precisely what the DO NOT EDIT banner is asking them not to do.
func (inv *Inventory) RenderYAML(header string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(header)

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(inv.build()); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// RenderJSON produces the same tree as JSON.
//
// JSON is a subset of YAML, so this is not a second format so much as the same
// document in a shape `ansible-inventory -i` also accepts — and one that jq can
// read, which is what makes the generation testable from a shell.
func (inv *Inventory) RenderJSON() ([]byte, error) {
	out, err := json.MarshalIndent(inv.build(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
