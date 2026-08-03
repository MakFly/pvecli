package iac

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DeclarationFile is what `pvecli vm declare` maintains inside the Terraform
// directory.
//
// The `.auto.tfvars.json` suffix is load-bearing: Terraform loads any file
// matching it without being told to, so declaring a VM never means editing HCL.
// That separation is the point of the whole design -- the module is CODE, read
// once by a human and versioned; the VMs are DATA. Resizing a VM from 8 to
// 16 GiB is then a number in a data file, not a resource to rewrite.
const DeclarationFile = "pvecli.auto.tfvars.json"

// VM is one declared guest. The json tags are the object attribute names of
// `variable "vms"` in pvecli-vms.tf; the two must move together.
type VM struct {
	VMID     int      `json:"vmid"`
	Cores    int      `json:"cores"`
	Memory   int      `json:"memory"`
	Disk     int      `json:"disk,omitempty"`
	IP       string   `json:"ip,omitempty"`
	Gateway  string   `json:"gateway,omitempty"`
	Node     string   `json:"node,omitempty"`
	Template int      `json:"template,omitempty"`
	User     string   `json:"user,omitempty"`
	Services []string `json:"services"`
	Tags     []string `json:"tags"`
}

// LXC is one declared container. The json tags are the object attribute names
// of `variable "lxcs"` in pvecli-lxc.tf; the two must move together.
//
// No User field: the container resource's user_account block takes SSH keys
// only, not a username -- unlike a cloud-init VM, there is no second account
// to name.
type LXC struct {
	VMID    int    `json:"vmid"`
	Cores   int    `json:"cores"`
	Memory  int    `json:"memory"`
	Disk    int    `json:"disk,omitempty"`
	IP      string `json:"ip,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Node    string `json:"node,omitempty"`
	// Template is the CTID to clone. Required, unlike VM.Template: there is no
	// shared default template for containers to fall back on.
	Template int `json:"template"`
	// Unprivileged is never omitted: it is a bool, so omitempty would drop
	// "false" from the JSON exactly when it matters most, and Terraform's
	// optional(bool, true) would then silently turn a privileged container
	// back into an unprivileged one.
	Unprivileged bool     `json:"unprivileged"`
	Services     []string `json:"services"`
	Tags         []string `json:"tags"`
}

// Declaration is the whole file.
type Declaration struct {
	VMs  map[string]VM  `json:"vms"`
	LXCs map[string]LXC `json:"lxcs"`
}

// OwnerTags are put on every declared VM. "managed" is the ownership guard the
// CLI already honours: `pvecli vm set` refuses a managed guest and points at
// its owner. That refusal is wanted here -- a declared VM is changed by
// re-declaring it, not by a one-off write that the next `apply` would revert.
var OwnerTags = []string{"managed", "pvecli"}

// hostname is what both Terraform (map key) and Ansible (inventory host) accept
// without quoting, and what PVE accepts as a guest name.
var hostname = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// DeclarationPath is where the declaration lives for a given Terraform dir.
func DeclarationPath(dir string) string { return filepath.Join(dir, DeclarationFile) }

// LoadDeclaration reads the declaration. A missing file is an empty
// declaration, not an error: the first `vm declare` in a directory has nothing
// to read and must still work.
func LoadDeclaration(dir string) (*Declaration, error) {
	raw, err := os.ReadFile(DeclarationPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return &Declaration{VMs: map[string]VM{}, LXCs: map[string]LXC{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", DeclarationPath(dir), err)
	}

	var d Declaration
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf(`%s est illisible : %w

Ce fichier est écrit par « pvecli vm declare » et « pvecli lxc declare ». S'il
a été édité à la main et qu'il est cassé, la déclaration entière est perdue
pour Terraform — répare-le avant d'aller plus loin, plutôt que de laisser une
commande le réécrire`,
			DeclarationPath(dir), err)
	}
	if d.VMs == nil {
		d.VMs = map[string]VM{}
	}
	if d.LXCs == nil {
		d.LXCs = map[string]LXC{}
	}
	return &d, nil
}

// Render serialises the declaration. Go sorts map keys when marshalling, and
// tags are sorted on the way in, so the same declaration always renders to the
// same bytes -- otherwise every `iac plan` would report a spurious change.
func (d *Declaration) Render() ([]byte, error) {
	if d.VMs == nil {
		d.VMs = map[string]VM{}
	}
	if d.LXCs == nil {
		d.LXCs = map[string]LXC{}
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Save writes the declaration atomically. A half-written file would be read by
// the next `terraform plan` as a syntax error at best, and as a missing VM --
// therefore a VM to destroy -- at worst.
func (d *Declaration) Save(dir string) error {
	raw, err := d.Render()
	if err != nil {
		return err
	}
	path := DeclarationPath(dir)
	tmp, err := os.CreateTemp(dir, ".pvecli-declare-*")
	if err != nil {
		return fmt.Errorf("fichier temporaire dans %s : %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("écriture de %s : %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// KindVM and KindLXC name the two kinds of guest a vmid can belong to. They are
// constants because the call sites, the refusal message and the tests all read
// them: a bare "vm" literal in three places drifts without anyone noticing.
const (
	KindVM  = "vm"
	KindLXC = "lxc"
)

// VMIDOwner tells which already-declared guest holds vmid. Proxmox has ONE vmid
// namespace for QEMU and LXC together -- a VM 220 and a container 220 cannot
// coexist -- and the declaration is the only place that clash can be named
// before `terraform apply` reports it as an opaque API error.
//
// The exception is the (name, kind) PAIR, not the name alone: VMs and LXCs are
// two separate maps and may legally hold the same key, so skipping by name
// would let `vm declare app-01 --vmid 300` slip past an LXC named app-01
// already holding 300 -- exactly the cross-type collision this exists to catch.
//
// Both maps are scanned in sorted order. A hand-edited file can already hold
// the same vmid twice, and Go's random map iteration would then make the
// refusal name a different owner on each run.
func (d *Declaration) VMIDOwner(vmid int, exceptName, exceptKind string) (owner, kind string, ok bool) {
	for _, name := range sortedKeys(d.VMs) {
		if name == exceptName && exceptKind == KindVM {
			continue
		}
		if d.VMs[name].VMID == vmid {
			return name, KindVM, true
		}
	}
	for _, name := range sortedKeys(d.LXCs) {
		if name == exceptName && exceptKind == KindLXC {
			continue
		}
		if d.LXCs[name].VMID == vmid {
			return name, KindLXC, true
		}
	}
	return "", "", false
}

// sortedKeys works on a nil map too, which is what makes VMIDOwner safe on a
// zero-value Declaration -- LoadDeclaration guards against nil maps, a struct
// literal does not.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate refuses a declaration the module could not use, with the message
// that names the real mistake.
func (vm VM) Validate(name string) error {
	switch {
	case !hostname.MatchString(name):
		return fmt.Errorf("nom « %s » invalide : minuscules, chiffres et tirets, 63 caractères au plus", name)
	case vm.VMID < 100:
		return fmt.Errorf("vmid %d invalide : Proxmox réserve les identifiants en dessous de 100", vm.VMID)
	case vm.Cores < 1:
		return fmt.Errorf("cores %d invalide : au moins 1", vm.Cores)
	case vm.Memory < 512:
		// The trap the lab documented: `memory = 8` boots far enough to fail
		// incomprehensibly.
		return fmt.Errorf("memory %d : la valeur est en MIBIOCTETS. 8 Go s'écrit 8192, pas 8", vm.Memory)
	case vm.IP != "" && vm.IP != "dhcp" && !strings.Contains(vm.IP, "/"):
		return fmt.Errorf("ip « %s » : il faut un préfixe, ex. 192.168.1.220/24, ou « dhcp »", vm.IP)
	case vm.IP == "dhcp" && vm.Gateway != "":
		return errors.New("une passerelle avec « --ip dhcp » : l'API refuse la combinaison, le bail la fournit déjà")
	}
	return nil
}

// SetTags recomputes the tag list from the services. Sorted, because the tag
// list is compared as a set by the drift detector but written as a sequence by
// Terraform.
func (vm *VM) SetTags(serviceTags []string) {
	tags := append([]string{}, OwnerTags...)
	tags = append(tags, serviceTags...)
	sort.Strings(tags)
	vm.Tags = tags
}

// Validate refuses a declaration pvecli-lxc.tf could not use.
func (ct LXC) Validate(name string) error {
	switch {
	case !hostname.MatchString(name):
		return fmt.Errorf("nom « %s » invalide : minuscules, chiffres et tirets, 63 caractères au plus", name)
	case ct.VMID < 100:
		return fmt.Errorf("vmid %d invalide : Proxmox réserve les identifiants en dessous de 100", ct.VMID)
	case ct.Cores < 1:
		return fmt.Errorf("cores %d invalide : au moins 1", ct.Cores)
	case ct.Memory < 512:
		return fmt.Errorf("memory %d : la valeur est en MIBIOCTETS. 8 Go s'écrit 8192, pas 8", ct.Memory)
	case ct.Template < 100:
		return fmt.Errorf("template %d invalide : le ctid à cloner est obligatoire", ct.Template)
	case ct.IP != "" && ct.IP != "dhcp" && !strings.Contains(ct.IP, "/"):
		return fmt.Errorf("ip « %s » : il faut un préfixe, ex. 192.168.1.220/24, ou « dhcp »", ct.IP)
	case ct.IP == "dhcp" && ct.Gateway != "":
		return errors.New("une passerelle avec « --ip dhcp » : l'API refuse la combinaison, le bail la fournit déjà")
	}
	return nil
}

// SetTags mirrors VM.SetTags for containers.
func (ct *LXC) SetTags(serviceTags []string) {
	tags := append([]string{}, OwnerTags...)
	tags = append(tags, serviceTags...)
	sort.Strings(tags)
	ct.Tags = tags
}

// Change is one field that differs between two versions of a declaration.
type Change struct {
	Field  string
	Before string
	After  string
}

// Diff compares two versions of one VM, field by field. A textual diff of the
// rendered JSON would show braces moving; what an operator needs to see before
// confirming is "memory 8192 → 16384".
//
// A nil side means the VM is being added or removed.
func Diff(before, after *VM) []Change {
	var out []Change
	for _, f := range []struct {
		name string
		of   func(VM) string
	}{
		{"vmid", func(v VM) string { return numOrEmpty(v.VMID) }},
		{"cores", func(v VM) string { return numOrEmpty(v.Cores) }},
		{"memory", func(v VM) string { return withUnit(v.Memory, "Mio") }},
		{"disk", func(v VM) string { return withUnit(v.Disk, "Gio") }},
		{"ip", func(v VM) string { return v.IP }},
		{"gateway", func(v VM) string { return v.Gateway }},
		{"node", func(v VM) string { return v.Node }},
		{"template", func(v VM) string { return numOrEmpty(v.Template) }},
		{"user", func(v VM) string { return v.User }},
		{"services", func(v VM) string { return strings.Join(v.Services, ",") }},
		{"tags", func(v VM) string { return strings.Join(v.Tags, ",") }},
	} {
		var b, a string
		if before != nil {
			b = f.of(*before)
		}
		if after != nil {
			a = f.of(*after)
		}
		if b == a || (b == "" && a == "") {
			continue
		}
		out = append(out, Change{Field: f.name, Before: b, After: a})
	}
	return out
}

// DiffLXC mirrors Diff for containers.
func DiffLXC(before, after *LXC) []Change {
	var out []Change
	for _, f := range []struct {
		name string
		of   func(LXC) string
	}{
		{"vmid", func(c LXC) string { return numOrEmpty(c.VMID) }},
		{"cores", func(c LXC) string { return numOrEmpty(c.Cores) }},
		{"memory", func(c LXC) string { return withUnit(c.Memory, "Mio") }},
		{"disk", func(c LXC) string { return withUnit(c.Disk, "Gio") }},
		{"ip", func(c LXC) string { return c.IP }},
		{"gateway", func(c LXC) string { return c.Gateway }},
		{"node", func(c LXC) string { return c.Node }},
		{"template", func(c LXC) string { return numOrEmpty(c.Template) }},
		{"unprivileged", func(c LXC) string { return boolStr(c.Unprivileged) }},
		{"services", func(c LXC) string { return strings.Join(c.Services, ",") }},
		{"tags", func(c LXC) string { return strings.Join(c.Tags, ",") }},
	} {
		var b, a string
		if before != nil {
			b = f.of(*before)
		}
		if after != nil {
			a = f.of(*after)
		}
		if b == a || (b == "" && a == "") {
			continue
		}
		out = append(out, Change{Field: f.name, Before: b, After: a})
	}
	return out
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func numOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// withUnit keeps an unset field empty rather than rendering a bare unit. Diff
// compares the rendered strings, so " Mio" against " Mio" would be equal but
// " Mio" against "8192 Mio" would report an addition out of nothing.
func withUnit(n int, unit string) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s", n, unit)
}
