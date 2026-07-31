package iac

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// TerraformBin is the binary the state reader drives. It is not configurable:
// the whole point of decision D2 is that pvecli asks Terraform what the state
// says, rather than deciding for itself what a state file looks like.
const TerraformBin = "terraform"

// Resource types of the bpg/proxmox provider that describe a guest. Others
// exist (files, pools, users) and are none of this command's business.
const (
	TypeVM        = "proxmox_virtual_environment_vm"
	TypeContainer = "proxmox_virtual_environment_container"
)

// Declared is one Proxmox guest as Terraform declares it.
//
// The fields are exactly the ones drift comparison needs (PVX-044), and no
// more. Reading the whole state into a generic map and comparing everything
// would report a "drift" on every attribute Terraform computes after the fact —
// which is noise, not information.
type Declared struct {
	Address string `json:"address"`
	Type    string `json:"type"`

	VMID   int      `json:"vm_id"`
	Name   string   `json:"name,omitempty"`
	Node   string   `json:"node_name,omitempty"`
	Cores  int      `json:"cores,omitempty"`
	Memory int      `json:"memory,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	OnBoot *bool    `json:"on_boot,omitempty"`

	Disks    []DeclaredDisk `json:"disks,omitempty"`
	Networks []DeclaredNIC  `json:"networks,omitempty"`
}

// IsContainer reports whether this is an LXC rather than a QEMU guest.
func (d Declared) IsContainer() bool { return d.Type == TypeContainer }

// DeclaredDisk is one disk block of the resource.
type DeclaredDisk struct {
	Interface string `json:"interface,omitempty"`
	Datastore string `json:"datastore_id,omitempty"`
	// Size is in GiB, which is the unit the provider uses. PVE reports bytes;
	// the conversion happens once, in the comparison, and is commented there.
	Size int `json:"size,omitempty"`
}

// DeclaredNIC is one network_device block.
type DeclaredNIC struct {
	Bridge string `json:"bridge,omitempty"`
	Model  string `json:"model,omitempty"`
	VLAN   int    `json:"vlan_id,omitempty"`
}

// EmptyStateError distinguishes "terraform has nothing to say yet" from "the
// state could not be read". They look identical from the outside — no
// resources — and they call for opposite reactions: run apply, or investigate.
type EmptyStateError struct{ Dir string }

func (e *EmptyStateError) Error() string {
	return fmt.Sprintf("le state Terraform de %s est vide.\n\n"+
		"Ce n'est pas une erreur de lecture : terraform a répondu, et n'a aucune\n"+
		"ressource à déclarer. Le plus probable est qu'il n'a pas encore été appliqué :\n"+
		"  pvecli iac plan     puis     pvecli iac apply", e.Dir)
}

// ExitCode: an empty state is a state of affairs, not a failure. 0 would be a
// lie for a command asked to print resources, but this is not a broken run.
func (e *EmptyStateError) ExitCode() int { return 1 }

// showOutput is the part of `terraform show -json` this package reads.
//
// The shape is Terraform's documented JSON output format (format_version 1.x),
// not the state file's internal format — that is decision D2. The state file is
// a database Terraform owns; its layout has changed between minor versions
// before, and parsing it directly is how a tool breaks on an upgrade it did not
// participate in.
type showOutput struct {
	FormatVersion string `json:"format_version"`
	Values        *struct {
		RootModule module `json:"root_module"`
	} `json:"values"`
}

type module struct {
	Resources    []stateResource `json:"resources"`
	ChildModules []module        `json:"child_modules"`
}

type stateResource struct {
	Address   string          `json:"address"`
	Mode      string          `json:"mode"`
	Type      string          `json:"type"`
	Values    map[string]any  `json:"values"`
	Sensitive json.RawMessage `json:"sensitive_values"`
}

// ReadState asks Terraform what it believes, and never touches the state file.
//
// `terraform show -json` is the only command issued. It is read-only by
// construction — there is no flag that could make it write — and
// TestStateReadingNeverWritesTheState pins that down by asserting the exact
// argv, because a future refactor reaching for `terraform refresh` (which DOES
// write) would be an easy and invisible mistake.
func ReadState(ctx context.Context, dir string, stderr io.Writer) ([]Declared, error) {
	if err := CheckDir("iac.terraform_dir", dir); err != nil {
		return nil, err
	}
	tf := Tool{Name: TerraformBin, Dir: dir}
	if err := tf.Look(); err != nil {
		return nil, err
	}

	raw, err := tf.Output(ctx, stderr, "show", "-json")
	if err != nil {
		return nil, fmt.Errorf("lecture du state via « terraform show -json » dans %s : %w\n\n"+
			"Si terraform se plaint des providers, le dossier n'est pas initialisé :\n"+
			"  cd %s && terraform init", dir, err, dir)
	}

	var out showOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("sortie de « terraform show -json » illisible : %w", err)
	}
	// No `values` at all is what Terraform prints for a state that has never
	// been applied: {"format_version":"1.0"} and nothing else.
	if out.Values == nil {
		return nil, &EmptyStateError{Dir: dir}
	}

	declared := collect(out.Values.RootModule)
	if len(declared) == 0 {
		return nil, &EmptyStateError{Dir: dir}
	}
	sort.Slice(declared, func(i, j int) bool { return declared[i].VMID < declared[j].VMID })
	return declared, nil
}

// collect walks the module tree. Resources can live in child modules, and a
// reader that only looked at the root would silently report "orphan" for
// everything a module created.
func collect(m module) []Declared {
	var out []Declared
	for _, r := range m.Resources {
		// "data" sources describe what Terraform reads, not what it owns.
		if r.Mode != "managed" {
			continue
		}
		if r.Type != TypeVM && r.Type != TypeContainer {
			continue
		}
		out = append(out, declaredFrom(r))
	}
	for _, child := range m.ChildModules {
		out = append(out, collect(child)...)
	}
	return out
}

// declaredFrom reads the handful of attributes drift detection compares.
//
// Every value is read by name. Nothing is copied wholesale, which is what
// guarantees the criterion "no value marked sensitive is displayed": a
// sensitive attribute of the bpg provider — `initialization.user_account.
// password`, `initialization.user_account.keys` — is never among the names
// below, so it cannot reach the output by accident.
func declaredFrom(r stateResource) Declared {
	v := r.Values
	d := Declared{
		Address: r.Address,
		Type:    r.Type,
		VMID:    intOf(v["vm_id"]),
		Name:    stringOf(v["name"]),
		Node:    stringOf(v["node_name"]),
		Tags:    stringsOf(v["tags"]),
		OnBoot:  boolPtrOf(v["on_boot"]),
	}

	// The provider models cpu/memory/disk/network_device as nested BLOCKS,
	// which the JSON output renders as lists of objects — even when the schema
	// allows only one. `cpu.cores` is therefore values["cpu"][0]["cores"].
	if cpu := firstBlock(v["cpu"]); cpu != nil {
		d.Cores = intOf(cpu["cores"])
	}
	if mem := firstBlock(v["memory"]); mem != nil {
		d.Memory = intOf(mem["dedicated"])
	}
	for _, blk := range blocks(v["disk"]) {
		d.Disks = append(d.Disks, DeclaredDisk{
			Interface: stringOf(blk["interface"]),
			Datastore: stringOf(blk["datastore_id"]),
			Size:      intOf(blk["size"]),
		})
	}
	for _, blk := range blocks(v["network_device"]) {
		d.Networks = append(d.Networks, DeclaredNIC{
			Bridge: stringOf(blk["bridge"]),
			Model:  stringOf(blk["model"]),
			VLAN:   intOf(blk["vlan_id"]),
		})
	}

	// A container declares its cores and memory at the top level, not in a
	// block. The two resource types are close enough to be confusing and
	// different enough to need saying.
	if d.Cores == 0 {
		d.Cores = intOf(v["cpu_cores"])
	}
	sort.Strings(d.Tags)
	return d
}

func blocks(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func firstBlock(v any) map[string]any {
	if b := blocks(v); len(b) > 0 {
		return b[0]
	}
	return nil
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// intOf reads a number out of JSON, where every number arrives as a float64.
func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		// The provider sometimes renders a number as a string (memory in a
		// container). Parsing it here beats reporting a drift that is a type
		// difference and not a value difference.
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

func boolPtrOf(v any) *bool {
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

func stringsOf(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
